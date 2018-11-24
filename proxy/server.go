package proxy

import (
	"net"

	"github.com/golang/glog"
	"github.com/lucas-clemente/quic-go"
	"github.com/wweir/sower/parser"
)

func StartServer(port string) {
	ln, err := quic.ListenAddr(":"+port, nil, nil)
	if err != nil {
		glog.Fatalln(err)
	}

	for {
		sess, err := ln.Accept()
		if err != nil {
			glog.Errorln(err)
		}
		go acceptSession(sess)
	}
}

func acceptSession(sess quic.Session) {
	for {
		stream, err := sess.AcceptStream()
		if err != nil {
			glog.Errorln(err)
		}
		go acceptStream(stream, sess)
	}
}

func acceptStream(stream quic.Stream, sess quic.Session) {
	defer stream.Close()

	conn, addr, err := parser.ParseAddr(&streamConn{stream, sess})
	if err != nil {
		glog.Warningln(err)
		return
	}
	glog.V(1).Infoln(addr)

	rc, err := net.Dial("tcp", addr)
	if err != nil {
		glog.Warningln(err)
		return
	}
	defer rc.Close()
	rc.(*net.TCPConn).SetKeepAlive(true)

	relay(rc, conn)
}
