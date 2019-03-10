package proxy

import (
	"net"

	"github.com/golang/glog"
	"github.com/wweir/sower/proxy/shadow"
	"github.com/wweir/sower/proxy/transport"
)

func StartClient(tran transport.Transport, server, cipher, password, listenIP string) {
	connCh := listenLocal(listenIP, []string{"80", "443"})
	resolved := false

	glog.Infoln("Client started.")
	for {
		conn := <-connCh

		if !resolved {
			if addr, err := net.ResolveTCPAddr("tcp", server); err != nil {
				glog.Errorln(err)
			} else {
				server = addr.String()
				resolved = true
			}
		}
		glog.V(1).Infof("new conn from (%s) to (%s)", conn.RemoteAddr(), server)

		rc, err := tran.Dial(server)
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
