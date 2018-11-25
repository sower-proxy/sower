package proxy

import (
	"crypto/tls"
	"net"

	"github.com/golang/glog"
	"github.com/lucas-clemente/quic-go"
)

func StartClient(server string) {
	connCh := listenLocal([]string{":80", ":443"})

	for {
		sess, err := quic.DialAddr(server, &tls.Config{InsecureSkipVerify: true}, nil)
		if err != nil {
			glog.Fatalf("connect to remote(%s) fail:%s\n", server, err)
		}
		glog.Infoln("new session to", sess.RemoteAddr())

		for conn := range connCh {
			if err := openStream(conn, sess); err != nil {
				break
			}
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

func openStream(conn net.Conn, sess quic.Session) error {
	defer conn.Close()
	conn.(*net.TCPConn).SetKeepAlive(true)

	glog.V(1).Infoln("new request from", conn.RemoteAddr())
	stream, err := sess.OpenStream()
	if err != nil {
		glog.Warningf("connect to remote(%s) fail:%s\n", sess.RemoteAddr(), err)
		return err
	}
	defer stream.Close()

	relay(&streamConn{stream, sess}, conn)
	return nil
}
