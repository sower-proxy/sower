package proxy

import (
	"net"
	"strings"

	"github.com/golang/glog"
	"github.com/wweir/sower/proxy/parser"
	"github.com/wweir/sower/proxy/shadow"
	"github.com/wweir/sower/proxy/transport"
)

func StartServer(tran transport.Transport, port, cipher, password string) {
	if port == "" {
		glog.Fatalln("port must set")
	}
	if !strings.Contains(port, ":") {
		port = ":" + port
	}

	connCh, err := tran.Listen(port)
	if err != nil {
		glog.Fatalf("listen %v fail: %s", port, err)
	}

	glog.Infoln("Server started.")
	for {
		conn := <-connCh
		conn = shadow.Shadow(conn, cipher, password)

		go handle(conn)
	}
}

func handle(conn net.Conn) {
	conn, addr, err := parser.ParseAddr(conn)
	if err != nil {
		conn.Close()
		glog.Warningln(err)
		return
	}
	glog.V(1).Infof("new conn from %s to %s", conn.RemoteAddr(), addr)

	rc, err := net.Dial("tcp", addr)
	if err != nil {
		conn.Close()
		glog.Warningln(err)
		return
	}
	rc.(*net.TCPConn).SetKeepAlive(true)

	relay(rc, conn)
}
