package transport

import (
	"net"
	"time"

	"github.com/golang/glog"
)

type tcp struct {
	DialTimeout time.Duration
	isSocks5    bool
}

func init() {
	transports["TCP"] = &tcp{
		DialTimeout: 5 * time.Second,
	}
	transports["SOCKS5"] = &tcp{
		DialTimeout: 5 * time.Second,
		isSocks5:    true,
	}
}

func (t *tcp) Dial(server string) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", server, t.DialTimeout)
	if err != nil {
		return nil, err
	}

	conn.(*net.TCPConn).SetKeepAlive(true)
	return conn, nil
}

func (t *tcp) Listen(port string) (<-chan net.Conn, error) {
	if t.isSocks5 {
		panic("not support run as socks5 server")
	}

	ln, err := net.Listen("tcp", port)
	if err != nil {
		return nil, err
	}

	connCh := make(chan net.Conn)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				glog.Fatalln("TCP listen:", err)
			}

			conn.(*net.TCPConn).SetKeepAlive(true)
			connCh <- conn
		}
	}()
	return connCh, nil
}
