package proxy

import (
	"crypto/tls"
	"net"
	"sync/atomic"
	"time"

	"github.com/golang/glog"
	"github.com/lucas-clemente/quic-go"
)

func StartClient(server string) {
	connCh := listenLocal([]string{":80", ":443"})
	reDialCh := make(chan net.Conn, 10)
	var conn net.Conn

	for {
		sess, err := quic.DialAddr(server, &tls.Config{InsecureSkipVerify: true}, nil)
		if err != nil {
			if sess, err = quic.DialAddr(server, &tls.Config{InsecureSkipVerify: true}, nil); err != nil {
				glog.Errorf("connect to remote(%s) fail:%s\n", server, err)
				continue
			}
		}
		glog.Infoln("new session to", sess.RemoteAddr())

		for { // session rotate logic
			select {
			case conn = <-connCh:
			case conn = <-reDialCh:
			}

			if !openStream(conn, sess, reDialCh) {
				sess.Close()
				break
			}
		}
	}
}

func openStream(conn net.Conn, sess quic.Session, reDialCh chan<- net.Conn) bool {
	glog.V(1).Infoln("new request from", conn.RemoteAddr())

	sessStat := new(int32) // 0:waiting 1:ok 2:timeout
	go func() {
		stream, err := sess.OpenStream()
		if err != nil {
			glog.Warningf("connect to remote(%s) fail:%s\n", sess.RemoteAddr(), err)
			reDialCh <- conn
			return
		}
		defer stream.Close()

		if !atomic.CompareAndSwapInt32(sessStat, 0, 1) {
			reDialCh <- conn // timeout
		}

		conn.(*net.TCPConn).SetKeepAlive(true)
		relay(&streamConn{stream, sess}, conn)
		conn.Close()
	}()

	// wait timeout: 10ms * 100 => 1s
	for i := 0; i < 100; i++ {
		if atomic.LoadInt32(sessStat) == 1 {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}

	return !atomic.CompareAndSwapInt32(sessStat, 0, 2)
}

func listenLocal(ports []string) <-chan net.Conn {
	connCh := make(chan net.Conn, 10)
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
