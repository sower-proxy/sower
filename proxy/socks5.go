package proxy

import (
	"net"

	"github.com/wweir/sower/transport"
	"github.com/wweir/util-go/log"
)

func StartSocks5Proxy(listenAddr, serverAddr string, password []byte) {
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatalw("socks5 proxy", "addr", listenAddr, "err", err)
	}

	serveSocks5(ln, serverAddr, password)
}

func serveSocks5(ln net.Listener, serverAddr string, password []byte) {
	conn, err := ln.Accept()
	if err != nil {
		log.Fatalw("socks5 proxy", "err", err)
	}
	go serveSocks5(ln, serverAddr, password)

	tgtAddr, err := transport.ParseSocks5(conn)
	if err != nil {
		log.Errorw("socks5 proxy", "err", err)
		return
	}

	rc, err := transport.Dial(tgtAddr, func(host string) (string, []byte) {
		return serverAddr, password
	})
	if err != nil {
		log.Errorw("socks5 proxy", "err", err)
		return
	}

	relay(conn, rc)
	conn.Close()
	rc.Close()
}
