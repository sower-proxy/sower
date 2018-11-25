package proxy

import (
	"crypto/tls"
	"net"

	"github.com/golang/glog"
	"github.com/lucas-clemente/quic-go"
)

func StartClient(server string) {
	connCh := listenLocal([]string{":80", ":443"})

	for conn := range connCh {
		sess, err := quic.DialAddr(server, &tls.Config{InsecureSkipVerify: true}, nil)
		if err != nil {
			glog.Errorf("connect to remote(%s) fail:%s\n", server, err)
			continue
		}
		glog.Infoln("new session to", sess.RemoteAddr())

		reDial := false // no cas action, no need add lock
		for !reDial {
			go openStream(conn, sess, &reDial)
			conn = <-connCh
		}

		sess.Close()
	}
}

func listenLocal(ports []string) <-chan net.Conn {
	connCh := make(chan net.Conn)
	for i := range ports {
		go func(port string) {
			ln, err := net.Listen("tcp", port)
			if err != nil {
				glog.Fatalln(err)
			}

			for {
				conn, err := ln.Accept()
				if err != nil {
					glog.Errorln("accept", port, "fail:", err)
				}

				connCh <- conn
			}
		}(ports[i])
	}

	glog.Infoln("listening ports:", ports)
	return connCh
}

func openStream(conn net.Conn, sess quic.Session, reDial *bool) {
	defer conn.Close()
	conn.(*net.TCPConn).SetKeepAlive(true)

	glog.V(1).Infoln("new request from", conn.RemoteAddr())
	stream, err := sess.OpenStream()
	if err != nil {
		glog.Warningf("connect to remote(%s) fail:%s\n", sess.RemoteAddr(), err)
		*reDial = true
		return
	}
	defer stream.Close()

	relay(&streamConn{stream, sess}, conn)
}
