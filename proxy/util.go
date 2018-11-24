package proxy

import (
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lucas-clemente/quic-go"
)

type streamConn struct {
	quic.Stream
	sess quic.Session
}

func (s *streamConn) LocalAddr() net.Addr {
	return s.sess.LocalAddr()
}

func (s *streamConn) RemoteAddr() net.Addr {
	return s.sess.RemoteAddr()
}

func relay(conn1, conn2 net.Conn) {
	wg := &sync.WaitGroup{}
	exitFlag := new(int32)
	wg.Add(2)
	go redirect(conn1, conn2, wg, exitFlag)
	redirect(conn2, conn1, wg, exitFlag)
	wg.Wait()
}

func redirect(conn1, conn2 net.Conn, wg *sync.WaitGroup, exitFlag *int32) {
	if _, err := io.Copy(conn2, conn1); err != nil && (atomic.LoadInt32(exitFlag) == 0) {
		log.Printf("%s<>%s -> %s<>%s: %s", conn1.RemoteAddr(), conn1.LocalAddr(), conn2.LocalAddr(), conn2.RemoteAddr(), err)
	}

	// wakeup all conn goroutine
	atomic.AddInt32(exitFlag, 1)
	now := time.Now()
	conn1.SetDeadline(now)
	conn2.SetDeadline(now)
	wg.Done()
}
