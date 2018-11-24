package proxy

import (
	"log"
	"net"

	"github.com/lucas-clemente/quic-go"
	"github.com/wweir/sower/parser"
)

func StartServer(port string) {
	ln, err := quic.ListenAddr(":"+port, nil, nil)
	if err != nil {
		log.Fatalln(err)
	}

	for {
		sess, err := ln.Accept()
		if err != nil {
			log.Println(err)
		}
		go acceptSession(sess)
	}
}

func acceptSession(sess quic.Session) {
	for {
		stream, err := sess.AcceptStream()
		if err != nil {
			log.Println(err)
		}
		go acceptStream(stream, sess)
	}
}

func acceptStream(stream quic.Stream, sess quic.Session) {
	defer stream.Close()

	conn, addr, err := parser.ParseAddr(&streamConn{stream, sess})
	if err != nil {
		log.Panicln(err)
		return
	}
	log.Println(addr)

	rc, err := net.Dial("tcp", addr)
	if err != nil {
		log.Println(err)
		return
	}
	defer rc.Close()
	rc.(*net.TCPConn).SetKeepAlive(true)

	relay(rc, conn)
}
