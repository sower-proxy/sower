package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"time"

	"github.com/pkg/errors"
	"github.com/sower-proxy/conns/relay"
	"github.com/sower-proxy/conns/reread"
	"github.com/sower-proxy/deferlog/log"
	"github.com/wweir/sower/router"
	"github.com/wweir/sower/transport"
	"github.com/wweir/sower/transport/socks5"
	"github.com/wweir/sower/transport/sower"
	"github.com/wweir/sower/transport/trojan"
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
					log.Warn().Err(err).Msg("dial fallback dns failed, use default dns setting")
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
		log.Fatal().
			Str("type", conf.Remote.Type).
			Msg("unknown proxy type")
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
		log.Fatal().Err(err).
			Msg("serve socks5")
	}

	go ServeHTTP(ln, r)
	start := time.Now()
	reread := reread.New(conn)
	defer reread.Close()

	req, err := http.ReadRequest(bufio.NewReader(reread))
	if err != nil {
		log.Error().Err(err).Msg("read http request")
		return
	}

	rc, err := r.ProxyDial("tcp", req.Host, 80)
	if err != nil {
		log.Error().Err(err).
			Str("host", req.Host).
			Interface("req", req.URL).
			Msg("dial proxy")
		return
	}
	defer rc.Close()

	reread.Stop().Reread()
	err = relay.Relay(reread, rc)
	log.DebugWarn(err).
		Str("host", req.Host).
		Dur("spend", time.Since(start)).
		Msg("serve http")
}

func ServeHTTPS(ln net.Listener, r *router.Router) {
	conn, err := ln.Accept()
	if err != nil {
		log.Fatal().Err(err).
			Msg("serve socks5")
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
		log.Error().Err(err).
			Str("host", domain).
			Msg("dial proxy")
		return
	}
	defer rc.Close()

	reread.Stop().Reread()
	err = relay.Relay(reread, rc)
	log.DebugWarn(err).
		Str("host", domain).
		Dur("spend", time.Since(start)).
		Msg("serve http")
}

func ServeSocks5(ln net.Listener, r *router.Router) {
	conn, err := ln.Accept()
	if err != nil {
		log.Fatal().Err(err).
			Msg("serve socks5")
	}
	go ServeSocks5(ln, r)
	defer conn.Close()

	reread := reread.New(conn)

	byte1 := make([]byte, 1)
	if n, err := reread.Read(byte1); err != nil || n != 1 {
		log.Error().Err(err).Msg("read first byte")
		return
	}
	reread.Reread()

	if byte1[0] == 5 {
		reread.Stop()
		if addr, err := socks5.New().Unwrap(reread); err != nil {
			log.Error().Err(err).Msg("read socks5 request")
		} else {
			host, port := addr.(*socks5.AddrHead).Addr()
			r.RouteHandle(reread, host, port)
		}
		return
	}

	req, err := http.ReadRequest(bufio.NewReader(reread))
	if err != nil {
		log.Error().Err(err).Msg("read http request")
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
