package proxy

import (
	"net"
	"strings"

	"github.com/golang/glog"
	"github.com/wweir/sower/proxy/kcp"
	"github.com/wweir/sower/proxy/quic"
	"github.com/wweir/sower/proxy/tcp"
	"github.com/wweir/sower/shadow"
)

type Client interface {
	Dial(server string) (net.Conn, error)
}

func NewClient(netType string) Client {
	switch netType {
	case QUIC.String():
		return quic.NewClient()
	case KCP.String():
		return kcp.NewClient()
	case TCP.String():
		return tcp.NewClient()
	default:
		glog.Fatalln("invalid net type: " + netType)
		return nil
	}
}

func StartClient(netType, server, cipher, password, listenIP string) {
	connCh := listenLocal(listenIP, []string{":80", ":443"})
	client := NewClient(netType)
	if idx := strings.Index(server, ":"); idx > 0 {
		ips, err := net.LookupIP(server[:idx])
		if err != nil || len(ips) == 0 {
			glog.Fatalln(err, ips)
		}
		server = ips[0].String() + server[idx:]
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

func listenLocal(listenIP string, ports []string) <-chan net.Conn {
	connCh := make(chan net.Conn, 10)
	for i := range ports {
		go func(port string) {
			ln, err := net.Listen("tcp", listenIP+port)
			if err != nil {
				glog.Fatalln(err)
			}

			for {
				conn, err := ln.Accept()
				if err != nil {
					glog.Errorln("accept", listenIP+port, "fail:", err)
					continue
				}

				conn.(*net.TCPConn).SetKeepAlive(true)
				connCh <- conn
			}
		}(ports[i])
	}

	glog.Infoln("listening ports:", ports)
	return connCh
}
