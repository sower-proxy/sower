package proxy

import (
	"io"
	"net"
	"sync"
	"time"

	"github.com/golang/glog"
)

//go:generate stringer -type=netType $GOFILE
type netType int

const (
	QUIC netType = iota
	KCP
	TCP
)

func relay(conn1, conn2 net.Conn) {
	wg := &sync.WaitGroup{}
	wg.Add(2)
	go redirect(conn2, conn1, wg)
	redirect(conn1, conn2, wg)
	wg.Wait()
}

func redirect(dst, src net.Conn, wg *sync.WaitGroup) {
	if _, err := io.Copy(dst, src); err != nil {
		glog.V(1).Infof("%s<>%s -> %s<>%s: %s", src.RemoteAddr(), src.LocalAddr(), dst.LocalAddr(), dst.RemoteAddr(), err)
	}

	now := time.Now()
	src.SetReadDeadline(now)
	dst.SetWriteDeadline(now)
	wg.Done()
}
