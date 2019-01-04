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

func relay(transparentConn, cryptoConn net.Conn, encrypt, decrypt func([]byte) []byte) {
	wg := &sync.WaitGroup{}
	exitFlag := new(int32)
	wg.Add(2)
	go redirect(transparentConn, cryptoConn, encrypt, wg, exitFlag)
	redirect(cryptoConn, transparentConn, decrypt, wg, exitFlag)
	wg.Wait()
}

func redirect(dst, src net.Conn, fn func([]byte) []byte, wg *sync.WaitGroup, exitFlag *int32) {
	var buf = make([]byte, 4<<20 /*4M*/)
	var n int
	var err error
	for {
		if n, err = src.Read(buf); err != nil {
			break
		}
		if _, err = dst.Write(fn(buf[:n])); err != nil {
			break
		}
	}

	if err != io.EOF && (atomic.LoadInt32(exitFlag) == 0) {
		glog.V(1).Infof("%s<>%s -> %s<>%s: %s", src.RemoteAddr(), src.LocalAddr(), dst.LocalAddr(), dst.RemoteAddr(), err)
	}
	atomic.AddInt32(exitFlag, 1)

	// wakeup all conn goroutine
	now := time.Now()
	dst.SetDeadline(now)
	src.SetDeadline(now)
	wg.Done()
}
