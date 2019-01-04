package proxy

import (
	"io"
	"net"
	"sync"
	"sync/atomic"
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
	exitFlag := new(int32)
	wg.Add(2)
	go redirect(conn2, conn1, wg, exitFlag)
	redirect(conn1, conn2, wg, exitFlag)
	wg.Wait()
}

func redirect(dst, src net.Conn, wg *sync.WaitGroup, exitFlag *int32) {
	if _, err := io.Copy(dst, src); err != io.EOF && (atomic.LoadInt32(exitFlag) == 0) {
		glog.V(1).Infof("%s<>%s -> %s<>%s: %s", src.RemoteAddr(), src.LocalAddr(), dst.LocalAddr(), dst.RemoteAddr(), err)
	}
	atomic.AddInt32(exitFlag, 1)

	// wakeup all conn goroutine
	now := time.Now()
	dst.SetDeadline(now)
	src.SetDeadline(now)
	wg.Done()
}
