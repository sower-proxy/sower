package main

import (
	"bufio"
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	utls "github.com/refraction-networking/utls"
	"github.com/sower-proxy/conns/relay"
	"github.com/sower-proxy/conns/reread"
	"github.com/sower-proxy/sower/internal/admin"
	"github.com/sower-proxy/sower/pkg/upstreamtls"
	"github.com/sower-proxy/sower/router"
	"github.com/sower-proxy/sower/transport"
	"github.com/sower-proxy/sower/transport/socks5"
	"github.com/sower-proxy/sower/transport/sower"
)

const (
	proxyDialTimeout = 10 * time.Second
	proxyReadTimeout = 15 * time.Second

	tlsRecordHeaderLen       = 5
	tlsRecordTypeHandshake   = 22
	tlsHandshakeTypeHello    = 1
	maxTLSPlaintextRecordLen = 64 * 1024
)

func GenProxyDial(proxyType, proxyHost, proxyPassword, dns string, tlsOptions upstreamtls.Options, stats *admin.Stats) (router.ProxyDialFn, error) {
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
					if stats != nil {
						stats.RecordProxyError("dns", fmt.Sprintf("fallback dial %s: %v", dns, err))
					}
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

// hostOnly strips the port from a host:port string, returning the input
// unchanged when it has no port.
func hostOnly(addr string) string {
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	return addr
}

// splitHostPort splits host:port, falling back to defPort when addr has no
// port or the port is invalid.
func splitHostPort(addr string, defPort uint16) (string, uint16) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return addr, defPort
	}
	p, err := strconv.Atoi(portStr)
	if err != nil || p < 1 || p > 65535 {
		return host, defPort
	}
	return host, uint16(p)
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

func ServeHTTP(ctx context.Context, ln net.Listener, r *router.Router, stats *admin.Stats) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if shouldRetryAccept(ctx, "http", err, stats) {
				continue
			}
			return wrapAcceptErr(ctx, "http", err)
		}
		go handleHTTPConn(conn, r, stats)
	}
}

func ServeHTTPS(ctx context.Context, ln net.Listener, r *router.Router, stats *admin.Stats) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if shouldRetryAccept(ctx, "https", err, stats) {
				continue
			}
			return wrapAcceptErr(ctx, "https", err)
		}
		go handleHTTPSConn(conn, r, stats)
	}
}

func ServeSocks5(ctx context.Context, ln net.Listener, r *router.Router, stats *admin.Stats) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if shouldRetryAccept(ctx, "socks5", err, stats) {
				continue
			}
			return wrapAcceptErr(ctx, "socks5", err)
		}
		go handleSocks5Conn(conn, r, stats)
	}
}

func handleHTTPConn(conn net.Conn, r *router.Router, stats *admin.Stats) {
	start := time.Now()
	conn = stats.WrapConn(conn, "http")
	rereadConn := reread.New(conn)
	defer rereadConn.Close()

	_ = rereadConn.SetDeadline(time.Now().Add(proxyReadTimeout))
	br := bufio.NewReader(rereadConn)
	req, err := http.ReadRequest(br)
	if err != nil {
		slog.Error("read http request", "error", err)
		return
	}
	_ = rereadConn.SetDeadline(time.Time{})

	stats.BindConn(conn, hostOnly(req.Host))
	defPort := uint16(80)
	if req.Method == http.MethodConnect {
		// CONNECT targets use the port from the request line, defaulting to 443.
		defPort = 443
	}
	host, port := splitHostPort(req.Host, defPort)
	rc, err := r.DialProxyOnly("tcp", host, port)
	if err != nil {
		slog.Error("dial proxy", "error", err, "host", req.Host, "req", req.URL)
		stats.RecordProxyError("dial", fmt.Sprintf("%s: %v", hostOnly(req.Host), err))
		return
	}
	defer rc.Close()

	if req.Method == http.MethodConnect {
		// Acknowledge CONNECT before tunneling raw bytes; the request head
		// must not be replayed to the target, which expects TLS data.
		rereadConn.Stop().Reset()
		// The bufio reader may already have pulled bytes past the request
		// head (a client that pipelines tunnel data without waiting for the
		// 200). Replay those before relaying, or the tunnel's first bytes
		// would be lost and the target would see a truncated TLS stream.
		if buffered := br.Buffered(); buffered > 0 {
			if _, err := io.CopyN(rc, br, int64(buffered)); err != nil {
				slog.Debug("flush pipelined connect bytes", "error", err, "host", host, "port", port)
				return
			}
		}
		if _, err := rereadConn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
			slog.Debug("write connect response", "error", err, "host", host, "port", port)
			return
		}
	} else {
		rereadConn.Stop().Reread()
	}
	err = relay.Relay(rereadConn, rc)
	if err != nil {
		slog.Debug("serve http", "error", err, "host", req.Host, "spend", time.Since(start))
	}
}

func handleHTTPSConn(conn net.Conn, r *router.Router, stats *admin.Stats) {
	start := time.Now()
	conn = stats.WrapConn(conn, "https")
	rereadConn := reread.New(conn)
	defer rereadConn.Close()

	_ = rereadConn.SetDeadline(time.Now().Add(proxyReadTimeout))
	domain, err := peekTLSClientHelloServerName(rereadConn)
	if err != nil {
		slog.Debug("read tls client hello", "error", err)
		return
	}
	if domain == "" {
		slog.Debug("tls client hello missing server name")
		return
	}
	_ = rereadConn.SetDeadline(time.Time{})

	stats.BindConn(conn, domain)
	rc, err := r.DialProxyOnly("tcp", domain, 443)
	if err != nil {
		slog.Error("dial proxy", "error", err, "host", domain)
		stats.RecordProxyError("dial", fmt.Sprintf("%s: %v", domain, err))
		return
	}
	defer rc.Close()

	rereadConn.Stop().Reread()
	err = relay.Relay(rereadConn, rc)
	if err != nil {
		slog.Debug("serve https", "error", err, "host", domain, "spend", time.Since(start))
	}
}

func peekTLSClientHelloServerName(conn io.Reader) (string, error) {
	clientHello, err := readTLSClientHello(conn)
	if err != nil {
		return "", err
	}

	hello := utls.UnmarshalClientHello(clientHello)
	if hello == nil {
		return "", fmt.Errorf("parse tls client hello")
	}
	return hello.ServerName, nil
}

func readTLSClientHello(conn io.Reader) ([]byte, error) {
	clientHello := make([]byte, 0, 1024)

	for {
		header := make([]byte, tlsRecordHeaderLen)
		if _, err := io.ReadFull(conn, header); err != nil {
			return nil, fmt.Errorf("read tls record header: %w", err)
		}
		if header[0] != tlsRecordTypeHandshake {
			return nil, fmt.Errorf("unexpected tls record type %d", header[0])
		}

		recordLen := int(header[3])<<8 | int(header[4])
		if recordLen <= 0 || recordLen > maxTLSPlaintextRecordLen {
			return nil, fmt.Errorf("invalid tls record length %d", recordLen)
		}

		recordBody := make([]byte, recordLen)
		if _, err := io.ReadFull(conn, recordBody); err != nil {
			return nil, fmt.Errorf("read tls record body: %w", err)
		}
		clientHello = append(clientHello, recordBody...)

		if len(clientHello) < 4 {
			continue
		}
		if clientHello[0] != tlsHandshakeTypeHello {
			return nil, fmt.Errorf("unexpected tls handshake type %d", clientHello[0])
		}

		helloLen := int(clientHello[1])<<16 | int(clientHello[2])<<8 | int(clientHello[3])
		if helloLen == 0 {
			return nil, fmt.Errorf("empty tls client hello")
		}
		if len(clientHello) < 4+helloLen {
			continue
		}

		return clientHello[:4+helloLen], nil
	}
}

func handleSocks5Conn(conn net.Conn, r *router.Router, stats *admin.Stats) {
	conn = stats.WrapConn(conn, "socks5")
	defer conn.Close()

	rereadConn := reread.New(conn)
	_ = rereadConn.SetDeadline(time.Now().Add(proxyReadTimeout))

	byte1 := make([]byte, 1)
	if n, err := rereadConn.Read(byte1); err != nil || n != 1 {
		slog.Error("read first byte", "error", err)
		return
	}
	rereadConn.Reread()

	if byte1[0] == 5 {
		rereadConn.Stop()
		server := socks5.New()
		addr, err := server.ReadRequest(rereadConn)
		if err != nil {
			slog.Error("read socks5 request", "error", err)
			return
		}
		// The handshake is complete: clear the deadline before dialing and
		// relaying. Keeping it through the handshake bounds slowloris
		// clients that stall mid-negotiation.
		_ = rereadConn.SetDeadline(time.Time{})

		host, port := addr.(*socks5.AddrHead).Addr()
		stats.BindConn(conn, host)
		rc, err := r.DialSmart("tcp", host, port)
		if err != nil {
			if !stderrors.Is(err, router.ErrBlocked) {
				stats.RecordProxyError("dial", fmt.Sprintf("%s: %v", host, err))
			}
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

	br := bufio.NewReader(rereadConn)
	req, err := http.ReadRequest(br)
	if err != nil {
		slog.Error("read http request", "error", err)
		return
	}
	// Handshake complete; clear the deadline before dialing.
	_ = rereadConn.SetDeadline(time.Time{})

	host, port, err := router.ParseHostPort(req.Host, req.URL)
	if err != nil {
		rereadConn.Stop().Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
		return
	}

	stats.BindConn(conn, host)
	rc, err := r.DialSmart("tcp", host, port)
	if err != nil {
		if !stderrors.Is(err, router.ErrBlocked) {
			stats.RecordProxyError("dial", fmt.Sprintf("%s: %v", host, err))
		}
		writeHTTPProxyError(rereadConn.Stop(), err)
		return
	}
	defer rc.Close()

	if req.Method == http.MethodConnect {
		rereadConn.Stop().Reset()
		// Replay bytes the bufio reader already pulled past the request
		// head (pipelined tunnel data) before relaying raw bytes.
		if buffered := br.Buffered(); buffered > 0 {
			if _, err := io.CopyN(rc, br, int64(buffered)); err != nil {
				slog.Debug("flush pipelined connect bytes", "error", err, "host", host, "port", port)
				return
			}
		}
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

func shouldRetryAccept(ctx context.Context, protocol string, err error, stats *admin.Stats) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	if ne, ok := err.(net.Error); ok && ne.Temporary() {
		slog.Warn("temporary accept failed", "protocol", protocol, "error", err)
		if stats != nil {
			stats.RecordProxyError("accept", fmt.Sprintf("%s listener: %v", protocol, err))
		}
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
