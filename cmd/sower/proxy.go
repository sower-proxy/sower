package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/pkg/errors"
	"github.com/sower-proxy/conns/relay"
	"github.com/sower-proxy/conns/reread"
	"github.com/sower-proxy/sower/router"
	"github.com/sower-proxy/sower/transport"
	"github.com/sower-proxy/sower/transport/socks5"
	"github.com/sower-proxy/sower/transport/sower"
	"github.com/sower-proxy/sower/transport/trojan"
)

func GenProxyDial(proxyType, proxyHost, proxyPassword, dns string) router.ProxyDialFn {
	var proxy transport.Transport
	var dialFn func() (net.Conn, error)

	dialer := &net.Dialer{
		Resolver: &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				d := net.Dialer{}
				c, err := d.DialContext(ctx, "udp", net.JoinHostPort(dns, "53"))
				if err != nil {
					slog.Warn("dial fallback dns failed, use default dns setting", "error", err)
					c, err = d.DialContext(ctx, network, address)
				}
				return c, err
			},
		},
	}
	switch conf.Remote.Type {
	case "sower":
		proxy = sower.New(conf.Remote.Password)
		tlsCfg := &tls.Config{}
		dialFn = func() (net.Conn, error) {
			return tls.DialWithDialer(dialer, "tcp", net.JoinHostPort(proxyHost, "443"), tlsCfg)
		}

	case "trojan":
		proxy = trojan.New(conf.Remote.Password)
		tlsCfg := &tls.Config{}
		dialFn = func() (net.Conn, error) {
			return tls.DialWithDialer(dialer, "tcp", net.JoinHostPort(proxyHost, "443"), tlsCfg)
		}

	case "socks5":
		proxy = socks5.New()
		dialFn = func() (net.Conn, error) {
			return dialer.Dial("tcp", proxyHost)
		}

	default:
		slog.Error("unknown proxy type", "type", conf.Remote.Type)
		os.Exit(1)
	}

	return func(network, host string, port uint16) (net.Conn, error) {
		if host == "" || port == 0 {
			return nil, errors.Errorf("invalid addr(%s:%d)", host, port)
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
	}
}

func ServeHTTP(ln net.Listener, r *router.Router) {
	conn, err := ln.Accept()
	if err != nil {
		slog.Error("serve socks5", "error", err)
		os.Exit(1)
	}

	go ServeHTTP(ln, r)
	start := time.Now()
	reread := reread.New(conn)
	defer reread.Close()

	req, err := http.ReadRequest(bufio.NewReader(reread))
	if err != nil {
		slog.Error("read http request", "error", err)
		return
	}

	rc, err := r.ProxyDial("tcp", req.Host, 80)
	if err != nil {
		slog.Error("dial proxy", "error", err, "host", req.Host, "req", req.URL)
		return
	}
	defer rc.Close()

	reread.Stop().Reread()
	err = relay.Relay(reread, rc)
	if err != nil {
		slog.Debug("serve http", "error", err, "host", req.Host, "spend", time.Since(start))
	}
}

func ServeHTTPS(ln net.Listener, r *router.Router) {
	conn, err := ln.Accept()
	if err != nil {
		slog.Error("serve socks5", "error", err)
		os.Exit(1)
	}

	go ServeHTTPS(ln, r)
	start := time.Now()
	reread := reread.New(conn)
	defer reread.Close()

	var domain string
	tls.Server(reread, &tls.Config{
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			domain = hello.ServerName
			return nil, nil
		},
	}).Handshake()

	rc, err := r.ProxyDial("tcp", domain, 443)
	if err != nil {
		slog.Error("dial proxy", "error", err, "host", domain)
		return
	}
	defer rc.Close()

	reread.Stop().Reread()
	err = relay.Relay(reread, rc)
	if err != nil {
		slog.Debug("serve http", "error", err, "host", domain, "spend", time.Since(start))
	}
}

func ServeSocks5(ln net.Listener, r *router.Router) {
	conn, err := ln.Accept()
	if err != nil {
		slog.Error("serve socks5", "error", err)
		os.Exit(1)
	}
	go ServeSocks5(ln, r)
	defer conn.Close()

	reread := reread.New(conn)

	byte1 := make([]byte, 1)
	if n, err := reread.Read(byte1); err != nil || n != 1 {
		slog.Error("read first byte", "error", err)
		return
	}
	reread.Reread()

	if byte1[0] == 5 {
		reread.Stop()
		if addr, err := socks5.New().Unwrap(reread); err != nil {
			slog.Error("read socks5 request", "error", err)
		} else {
			host, port := addr.(*socks5.AddrHead).Addr()
			r.RouteHandle(reread, host, port)
		}
		return
	}

	req, err := http.ReadRequest(bufio.NewReader(reread))
	if err != nil {
		slog.Error("read http request", "error", err)
		return
	}

	host, port, err := router.ParseHostPort(req.Host, req.URL)
	if err != nil {
		reread.Stop().Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
		return
	}

	if req.Method == http.MethodConnect {
		// https
		reread.Stop().Reset()
		reread.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
	} else {
		// http
		reread.Stop().Reread()
	}

	r.RouteHandle(reread, host, port)
}
