package proxy

import (
	"net"

	"github.com/golang/glog"
	"github.com/wweir/sower/crypto"
	"github.com/wweir/sower/proxy/kcp"
	"github.com/wweir/sower/proxy/quic"
	"github.com/wweir/sower/proxy/tcp"
)

type Client interface {
	Dial(server string) (net.Conn, error)
}

func StartClient(netType, server, password string) {
	var connCh = listenLocal([]string{":80", ":443"})
	var client Client
	switch netType {
	case QUIC.String():
		client = quic.NewClient()
	case KCP.String():
		client = kcp.NewClient()
	case TCP.String():
		client = tcp.NewClient()
	}

	cryptor, err := crypto.NewCrypto(password)
	if err != nil {
		glog.Fatalln(err)
	}

	for {
		conn := <-connCh
		glog.V(1).Infof("new conn from (%s)", conn.RemoteAddr())

		rc, err := client.Dial(server)
		if err != nil {
			conn.Close()
			glog.Errorln(err)
			continue
		}

		encrypt, decrypt := cryptor.Crypto()

		go relay(conn, rc, encrypt, decrypt)
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
