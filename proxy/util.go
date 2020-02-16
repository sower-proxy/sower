package proxy

import (
	"crypto/tls"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wweir/sower/internal/http"
	"github.com/wweir/sower/internal/socks5"
)

func dial(serverAddr string, password []byte, tgtType byte, domain string, port uint16) (net.Conn, error) {
	if addr, ok := socks5.IsSocks5Schema(serverAddr); ok {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			return nil, err
		}
		return socks5.ToSocks5(conn, domain, port), nil
	}

	conn, err := tls.Dial("tcp", net.JoinHostPort(serverAddr, "443"), &tls.Config{})
	if err != nil {
		return nil, err
	}
	return http.NewTgtConn(conn, password, tgtType, domain, port), nil
}

func relay(conn1, conn2 net.Conn) {
	wg := &sync.WaitGroup{}
	exitFlag := new(int32)
	wg.Add(2)
	go redirect(conn2, conn1, wg, exitFlag)
	redirect(conn1, conn2, wg, exitFlag)
	wg.Wait()
}

func redirect(dst, src net.Conn, wg *sync.WaitGroup, exitFlag *int32) {
	io.Copy(dst, src)

	if atomic.CompareAndSwapInt32(exitFlag, 0, 1) {
		// wakeup blocked goroutine
		now := time.Now()
		src.SetDeadline(now)
		dst.SetDeadline(now)
	}

	wg.Done()
}
