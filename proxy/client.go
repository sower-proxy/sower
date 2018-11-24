package proxy

import (
	"net"

	"github.com/golang/glog"
	"github.com/lucas-clemente/quic-go"
)

func StartClient(server string) {
	connCh := listenLocal([]string{":80", ":443"})
	sess, err := quic.DialAddr(server, nil, nil)
	if err != nil {
		glog.Fatalf("connect to remote(%s) fail:%s\n", server, err)
	}

	for {
		go func(conn net.Conn) {
			glog.V(1).Infoln("new request to", conn.RemoteAddr())
			defer conn.Close()
			conn.(*net.TCPConn).SetKeepAlive(true)

			stream, err := sess.OpenStream()
			if err != nil {
				glog.Warningf("connect to remote(%s) fail:%s\n", server, err)
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
