package util

import (
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pkg/errors"
)

func RelayTo(conn net.Conn, addr string) (time.Duration, error) {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, "80")
	}

	start := time.Now()
	rc, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return time.Since(start), errors.Wrapf(err, "dial %s", addr)
	}
	defer rc.Close()

	Relay(conn, rc)
	return time.Since(start), nil
}

func Relay(conn1, conn2 net.Conn) {
	wg := &sync.WaitGroup{}
	exitFlag := new(int32)
	wg.Add(2)
	go redirect(conn2, conn1, wg, exitFlag)
	redirect(conn1, conn2, wg, exitFlag)
	wg.Wait()
}
func redirect(dst, src net.Conn, wg *sync.WaitGroup, exitFlag *int32) {

	// io.Copy(dst, io.TeeReader(src, os.Stdout))
	io.Copy(dst, src)

	if atomic.CompareAndSwapInt32(exitFlag, 0, 1) {
		// wakeup blocked goroutine
		now := time.Now()
		src.SetDeadline(now)
		dst.SetDeadline(now)
	}

	wg.Done()
}
