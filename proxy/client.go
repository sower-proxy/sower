package proxy

import (
	"net"

	"github.com/golang/glog"
	"github.com/wweir/sower/proxy/kcp"
	"github.com/wweir/sower/proxy/quic"
	"github.com/wweir/sower/proxy/tcp"
	"github.com/wweir/sower/shadow"
)

type Client interface {
	Dial(server string) (net.Conn, error)
}

func StartClient(netType, server, cipher, password string) {
	var connCh = listenLocal([]string{":80", ":443"})

	var client Client
	switch netType {
	case QUIC.String():
		client = quic.NewClient()
	case KCP.String():
		client = kcp.NewClient()
	case TCP.String():
		client = tcp.NewClient()
	default:
		glog.Fatalln("invalid net type: " + netType)
	}

	glog.Infoln("Client started.")
	for {
		conn := <-connCh
		glog.V(1).Infof("new conn from (%s) to (%s)", conn.RemoteAddr(), server)

		rc, err := client.Dial(server)
		if err != nil {
			conn.Close()
			glog.Errorln(err)
			continue
		}

		rc = shadow.Shadow(rc, cipher, password)

		go relay(conn, rc)
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
