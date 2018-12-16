package proxy

import (
	"net"

	"github.com/golang/glog"
	"github.com/wweir/sower/proxy/quic"
)

type Client interface {
	Dial(server string) (net.Conn, error)
}

func StartClient(netType, server string) {
	var connCh = listenLocal([]string{":80", ":443"})
	var client Client
	switch netType {
	case QUIC.String():
		client = quic.NewClient()
	case KCP.String():
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
