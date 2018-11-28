package proxy

import (
	"crypto/tls"
	"net"
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
				time.Sleep(2 * time.Second)
				continue
			}
		}
		glog.Infoln("new session to", sess.RemoteAddr())

		for { // session rotate logic
			select {
			case conn = <-connCh:
			case conn = <-reDialCh:
			}

			// sync action to reuse sigle sess
			if !openStream(conn, sess, reDialCh) {
				sess.Close()
				break
			}
		}
	}
}

func openStream(conn net.Conn, sess quic.Session, reDialCh chan<- net.Conn) bool {
	glog.V(1).Infoln("new request from", conn.RemoteAddr())

	okCh := make(chan struct{})
	go func() {
		stream, err := sess.OpenStream()
		if err != nil {
			glog.Warningf("connect to remote(%s) fail:%s\n", sess.RemoteAddr(), err)
			reDialCh <- conn
			close(okCh)
			return
		}
		defer stream.Close()

		select {
		case okCh <- struct{}{}:
		default:
		}
		close(okCh)

		conn.(*net.TCPConn).SetKeepAlive(true)
		relay(&streamConn{stream, sess}, conn)
	}()

	select {
	case _, ok := <-okCh: // true means close on error
		return ok
	case <-time.After(time.Second):
		return false
	}
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
					continue
				}

				connCh <- conn
			}
		}(ports[i])
	}

	glog.Infoln("listening ports:", ports)
	return connCh
}
