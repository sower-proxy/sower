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
	connCh := listenLocal(listenIP, []string{"80", "443"})
	client := NewClient(netType)
	resolved := false

	glog.Infoln("Client started.")
	for {
		conn := <-connCh
		glog.V(1).Infof("new conn from (%s) to (%s)", conn.RemoteAddr(), server)

		if !resolved {
			if addr, err := net.ResolveTCPAddr("tcp", server); err != nil {
				glog.Fatalln(err)
			} else {
				server = addr.String()
				resolved = true
			}
		}

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
			ln, err := net.Listen("tcp", net.JoinHostPort(listenIP, port))
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
