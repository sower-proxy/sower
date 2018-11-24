package proxy

import (
	"log"
	"net"

	"github.com/lucas-clemente/quic-go"
)

func StartClient(server string) {
	connCh := listenLocal([]string{":80", ":443"})
	sess, err := quic.DialAddr(server, nil, nil)
	if err != nil {
		log.Printf("connect to remote(%s) fail:%s\n", server, err)
		return
	}

	for {
		go func(conn net.Conn) {
			defer conn.Close()
			conn.(*net.TCPConn).SetKeepAlive(true)

			stream, err := sess.OpenStream()
			if err != nil {
				log.Printf("connect to remote(%s) fail:%s\n", server, err)
				return
			}
			defer stream.Close()

			relay(&streamConn{stream, sess}, conn)
		}(<-connCh)
	}
}

func listenLocal(ports []string) <-chan net.Conn {
	connCh := make(chan net.Conn)
	for i := range ports {
		go func(port string) {
			ln, err := net.Listen("tcp", port)
			if err != nil {
				log.Fatalln(err)
			}

			for {
				conn, err := ln.Accept()
				if err != nil {
					log.Println("accept", port, "fail:", err)
				}

				connCh <- conn
			}
		}(ports[i])
	}

	return connCh
}
