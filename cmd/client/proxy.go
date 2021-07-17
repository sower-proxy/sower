package main

import (
	"bufio"
	"crypto/tls"
	"net"
	"net/http"
	"time"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/wweir/sower/pkg/teeconn"
	"github.com/wweir/sower/router"
	"github.com/wweir/sower/transport"
	"github.com/wweir/sower/transport/sower"
	"github.com/wweir/sower/transport/trojan"
	"github.com/wweir/sower/util"
)

func GenProxyDial(proxyType, proxyHost, proxyPassword string) router.ProxyDialFn {
	var (
		proxyAddr = net.JoinHostPort(proxyHost, "443")
		tlsCfg    = &tls.Config{}
		proxy     transport.Transport
	)
	switch conf.Remote.Type {
	case "sower":
		proxy = sower.New(conf.Remote.Password)
	case "trojan":
		proxy = trojan.New(conf.Remote.Password)
	default:
		log.Fatal().
			Str("type", conf.Remote.Type).
			Msg("unknown proxy type")
	}

	return func(network, host string, port uint16) (net.Conn, error) {
		if host == "" || port == 0 {
			return nil, errors.Errorf("invalid addr(%s:%d)", host, port)
		}

		c, err := tls.Dial("tcp", proxyAddr, tlsCfg)
		if err != nil {
			return nil, err
		}

		if err := proxy.Wrap(c, host, port); err != nil {
			return nil, err
		}

		return c, nil
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
	teeconn := teeconn.New(conn)
	defer teeconn.Close()

	req, err := http.ReadRequest(bufio.NewReader(teeconn))
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

	teeconn.Stop().Reread()
	util.Relay(teeconn, rc)
	log.Info().
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
	teeconn := teeconn.New(conn)
	defer teeconn.Close()

	var domain string
	tls.Server(teeconn, &tls.Config{
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

	teeconn.Stop().Reread()
	util.Relay(teeconn, rc)
	log.Info().
		Str("host", domain).
		Dur("spend", time.Since(start)).
		Msg("serve http")
}
