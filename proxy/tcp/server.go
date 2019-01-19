package tcp

import (
	"net"

	"github.com/golang/glog"
)

type server struct {
}

func NewServer() *server {
	return &server{}
}

func (s *server) Listen(port string) (<-chan net.Conn, error) {
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
