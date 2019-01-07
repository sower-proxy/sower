package quic

import (
	"net"

	"github.com/golang/glog"
	quic "github.com/lucas-clemente/quic-go"
	"github.com/pkg/errors"
)

type server struct {
	conf *quic.Config
}

func NewServer() *server {
	return &server{
		// conf: &quic.Config{
		// 	HandshakeTimeout:   5 * time.Second,
		// 	MaxIncomingStreams: 1024,
		// 	KeepAlive:          true,
		// },
	}
}

func (s *server) Listen(port string) (<-chan net.Conn, error) {
	ln, err := quic.ListenAddr(port, mockTlsPem(), s.conf)
	if err != nil {
		return nil, errors.WithStack(err)
	}

	connCh := make(chan net.Conn)
	go func() {
		for {
			sess, err := ln.Accept()
			if err != nil {
				glog.Fatalln(err)
			}
			go accept(sess, connCh)
		}
	}()
	return connCh, nil
}

func accept(sess quic.Session, connCh chan<- net.Conn) {
	glog.V(1).Infoln("new session from ", sess.RemoteAddr())
	defer sess.Close()

	for {
		stream, err := sess.AcceptStream()
		if err != nil {
			glog.Errorln(err)
			return
		}

		connCh <- &streamConn{stream, sess}
	}
}
