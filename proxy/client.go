package proxy

import (
	"net"

	"github.com/golang/glog"
	"github.com/wweir/sower/proxy/parser"
	"github.com/wweir/sower/proxy/shadow"
	"github.com/wweir/sower/proxy/socks5"
	"github.com/wweir/sower/proxy/transport"
)

func StartClient(tran transport.Transport, isSocks5 bool, server, cipher, password, listenIP string) {
	conn80 := listenLocal(listenIP, "80")
	conn443 := listenLocal(listenIP, "443")
	var isHttp bool
	var conn net.Conn

	glog.Infoln("Client started.")
	for {
		select {
		case conn = <-conn80:
			isHttp = true
		case conn = <-conn443:
			isHttp = false
		}

		resolveAddr(&server)
		glog.V(1).Infof("new conn from (%s) to (%s)", conn.RemoteAddr(), server)

		rc, err := tran.Dial(server)
		if err != nil {
			conn.Close()
			glog.Errorln(err)
			continue
		}

		switch {
		case isSocks5 && isHttp:
			c, host, port, err := parser.ParseHttpAddr(conn)
			if err != nil {
				c.Close()
				rc.Close()
				glog.Errorln(err)
				continue
			}

			conn = c
			rc = socks5.ToSocks5(rc, host, port)

		case isSocks5 && !isHttp:
			c, host, err := parser.ParseHttpsHost(conn)
			if err != nil {
				c.Close()
				rc.Close()
				glog.Errorln(err)
				continue
			}

			conn = c
			rc = socks5.ToSocks5(rc, host, "443")

		case !isSocks5 && isHttp:
			rc = shadow.Shadow(rc, cipher, password)
			rc = parser.NewHttpProtocol(rc)

		case !isSocks5 && !isHttp:
			rc = shadow.Shadow(rc, cipher, password)
			rc = parser.NewHttpsProtocol(rc, "443")
		}

		go relay(conn, rc)
	}
}

func listenLocal(listenIP string, port string) <-chan net.Conn {
	connCh := make(chan net.Conn, 10)
	go func() {
		ln, err := net.Listen("tcp", net.JoinHostPort(listenIP, port))
		if err != nil {
			glog.Fatalln(err)
		}

		for {
			conn, err := ln.Accept()
			if err != nil {
				glog.Errorln("accept", listenIP+port, "fail:", err)
				continue
			}

			conn.(*net.TCPConn).SetKeepAlive(true)
			connCh <- conn
		}
	}()

	glog.Infoln("listening port:", port)
	return connCh
}
