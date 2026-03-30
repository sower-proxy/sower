package main

import (
	"bufio"
	"context"
	"crypto/tls"
	stderrors "errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/sower-proxy/conns/relay"
	"github.com/sower-proxy/conns/reread"
	"github.com/sower-proxy/sower/pkg/upstreamtls"
	"github.com/sower-proxy/sower/router"
	"github.com/sower-proxy/sower/transport"
	"github.com/sower-proxy/sower/transport/socks5"
	"github.com/sower-proxy/sower/transport/sower"
	"github.com/sower-proxy/sower/transport/trojan"
)

const (
	proxyDialTimeout = 10 * time.Second
	proxyReadTimeout = 15 * time.Second
)

func GenProxyDial(proxyType, proxyHost, proxyPassword, dns string, tlsOptions upstreamtls.Options) (router.ProxyDialFn, error) {
	var proxy transport.Transport
	var dialFn func() (net.Conn, error)

	dialer := &net.Dialer{
		Timeout:   proxyDialTimeout,
		KeepAlive: 30 * time.Second,
		Resolver: &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				d := net.Dialer{Timeout: proxyDialTimeout}
				c, err := d.DialContext(ctx, "udp", net.JoinHostPort(dns, "53"))
				if err != nil {
					slog.Warn("dial fallback dns failed, use default dns setting", "error", err)
					c, err = d.DialContext(ctx, network, address)
				}
				return c, err
			},
		},
	}

	switch proxyType {
	case "sower":
		tlsDialFn, err := newTLSDialFn(dialer, proxyHost, tlsOptions)
		if err != nil {
			return nil, err
		}
		proxy = sower.New(proxyPassword)
		dialFn = tlsDialFn
	case "trojan":
		tlsDialFn, err := newTLSDialFn(dialer, proxyHost, tlsOptions)
		if err != nil {
			return nil, err
		}
		proxy = trojan.New(proxyPassword)
		dialFn = tlsDialFn
	case "socks5":
		proxy = socks5.New()
		dialFn = func() (net.Conn, error) {
			return dialer.Dial("tcp", proxyHost)
		}
	default:
		return nil, fmt.Errorf("unknown proxy type %q", proxyType)
	}

	return func(network, host string, port uint16) (net.Conn, error) {
		if host == "" || port == 0 {
			return nil, fmt.Errorf("invalid addr(%s:%d)", host, port)
		}

		conn, err := dialFn()
		if err != nil {
			return nil, err
		}

		if err := proxy.Wrap(conn, host, port); err != nil {
			conn.Close()
			return nil, err
		}

		return conn, nil
	}, nil
}

func newTLSDialFn(dialer *net.Dialer, proxyHost string, tlsOptions upstreamtls.Options) (func() (net.Conn, error), error) {
	dialAddr, err := upstreamDialAddr(proxyHost, "443")
	if err != nil {
		return nil, err
	}
	if tlsOptions.ClientHello != "" {
		if err := upstreamtls.ValidateClientHello(tlsOptions.ClientHello); err != nil {
			return nil, err
		}
	}

	return func() (net.Conn, error) {
		return upstreamtls.Dial(dialer, "tcp", dialAddr, tlsOptions)
	}, nil
}

func upstreamDialAddr(addr, defaultPort string) (string, error) {
	if host, port, err := net.SplitHostPort(addr); err == nil {
		if host == "" || port == "" {
			return "", fmt.Errorf("invalid remote address %q", addr)
		}
		return net.JoinHostPort(host, port), nil
	}

	if addr == "" {
		return "", fmt.Errorf("empty remote address")
	}
	if strings.HasPrefix(addr, "[") || strings.HasSuffix(addr, "]") {
		return "", fmt.Errorf("invalid remote address %q", addr)
	}
	if strings.Count(addr, ":") == 1 {
		return "", fmt.Errorf("missing or invalid port in %q", addr)
	}
	return net.JoinHostPort(addr, defaultPort), nil
}

func ServeHTTP(ctx context.Context, ln net.Listener, r *router.Router) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if shouldRetryAccept(ctx, "http", err) {
				continue
			}
			return wrapAcceptErr(ctx, "http", err)
		}
		go handleHTTPConn(conn, r)
	}
}

func ServeHTTPS(ctx context.Context, ln net.Listener, r *router.Router) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if shouldRetryAccept(ctx, "https", err) {
				continue
			}
			return wrapAcceptErr(ctx, "https", err)
		}
		go handleHTTPSConn(conn, r)
	}
}

func ServeSocks5(ctx context.Context, ln net.Listener, r *router.Router) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if shouldRetryAccept(ctx, "socks5", err) {
				continue
			}
			return wrapAcceptErr(ctx, "socks5", err)
		}
		go handleSocks5Conn(conn, r)
	}
}

func handleHTTPConn(conn net.Conn, r *router.Router) {
	start := time.Now()
	rereadConn := reread.New(conn)
	defer rereadConn.Close()

	_ = rereadConn.SetDeadline(time.Now().Add(proxyReadTimeout))
	req, err := http.ReadRequest(bufio.NewReader(rereadConn))
	if err != nil {
		slog.Error("read http request", "error", err)
		return
	}
	_ = rereadConn.SetDeadline(time.Time{})

	rc, err := r.ProxyDial("tcp", req.Host, 80)
	if err != nil {
		slog.Error("dial proxy", "error", err, "host", req.Host, "req", req.URL)
		return
	}
	defer rc.Close()

	rereadConn.Stop().Reread()
	err = relay.Relay(rereadConn, rc)
	if err != nil {
		slog.Debug("serve http", "error", err, "host", req.Host, "spend", time.Since(start))
	}
}

func handleHTTPSConn(conn net.Conn, r *router.Router) {
	start := time.Now()
	rereadConn := reread.New(conn)
	defer rereadConn.Close()

	_ = rereadConn.SetDeadline(time.Now().Add(proxyReadTimeout))

	var domain string
	err := tls.Server(rereadConn, &tls.Config{
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			domain = hello.ServerName
			return nil, nil
		},
	}).Handshake()
	if err != nil {
		slog.Debug("tls handshake failed", "error", err)
		return
	}
	if domain == "" {
		slog.Debug("tls handshake missing server name")
		return
	}
	_ = rereadConn.SetDeadline(time.Time{})

	rc, err := r.ProxyDial("tcp", domain, 443)
	if err != nil {
		slog.Error("dial proxy", "error", err, "host", domain)
		return
	}
	defer rc.Close()

	rereadConn.Stop().Reread()
	err = relay.Relay(rereadConn, rc)
	if err != nil {
		slog.Debug("serve https", "error", err, "host", domain, "spend", time.Since(start))
	}
}

func handleSocks5Conn(conn net.Conn, r *router.Router) {
	defer conn.Close()

	rereadConn := reread.New(conn)
	_ = rereadConn.SetDeadline(time.Now().Add(proxyReadTimeout))

	byte1 := make([]byte, 1)
	if n, err := rereadConn.Read(byte1); err != nil || n != 1 {
		slog.Error("read first byte", "error", err)
		return
	}
	rereadConn.Reread()
	_ = rereadConn.SetDeadline(time.Time{})

	if byte1[0] == 5 {
		rereadConn.Stop()
		server := socks5.New()
		addr, err := server.ReadRequest(rereadConn)
		if err != nil {
			slog.Error("read socks5 request", "error", err)
			return
		}

		host, port := addr.(*socks5.AddrHead).Addr()
		rc, err := r.Dial("tcp", host, port)
		if err != nil {
			if replyErr := server.WriteReply(rereadConn, routeSocks5ReplyCode(err)); replyErr != nil {
				slog.Debug("write socks5 failure reply", "error", replyErr, "host", host, "port", port)
			}
			return
		}
		defer rc.Close()

		if err := server.WriteReply(rereadConn, socks5.RepSucceeded); err != nil {
			slog.Debug("write socks5 success reply", "error", err, "host", host, "port", port)
			return
		}
		_ = relay.Relay(rereadConn, rc)
		return
	}

	req, err := http.ReadRequest(bufio.NewReader(rereadConn))
	if err != nil {
		slog.Error("read http request", "error", err)
		return
	}

	host, port, err := router.ParseHostPort(req.Host, req.URL)
	if err != nil {
		rereadConn.Stop().Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
		return
	}

	rc, err := r.Dial("tcp", host, port)
	if err != nil {
		writeHTTPProxyError(rereadConn.Stop(), err)
		return
	}
	defer rc.Close()

	if req.Method == http.MethodConnect {
		rereadConn.Stop().Reset()
		if _, err := rereadConn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
			slog.Debug("write connect response", "error", err, "host", host, "port", port)
			return
		}
	} else {
		rereadConn.Stop().Reread()
	}

	if err := relay.Relay(rereadConn, rc); err != nil {
		slog.Debug("serve proxy request", "error", err, "host", host, "port", port, "method", req.Method)
	}
}

func shouldRetryAccept(ctx context.Context, protocol string, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	if ne, ok := err.(net.Error); ok && ne.Temporary() {
		slog.Warn("temporary accept failed", "protocol", protocol, "error", err)
		time.Sleep(200 * time.Millisecond)
		return true
	}
	return false
}

func wrapAcceptErr(ctx context.Context, protocol string, err error) error {
	if err == nil || ctx.Err() != nil {
		return nil
	}
	return fmt.Errorf("accept %s connection: %w", protocol, err)
}

func writeHTTPProxyError(conn net.Conn, err error) {
	status := "502 Bad Gateway"
	if stderrors.Is(err, router.ErrBlocked) {
		status = "403 Forbidden"
	}
	if _, writeErr := conn.Write([]byte("HTTP/1.1 " + status + "\r\n\r\n")); writeErr != nil {
		slog.Debug("write http proxy error", "error", writeErr, "status", status)
	}
}

func routeSocks5ReplyCode(err error) byte {
	if stderrors.Is(err, router.ErrBlocked) {
		return socks5.RepConnectionNotAllowed
	}
	return socks5.RepGeneralFailure
}
