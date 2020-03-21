package proxy

import (
	"bufio"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wweir/sower/util"
)

func ParseHTTP(conn net.Conn) (net.Conn, string, error) {
	teeConn := &util.TeeConn{Conn: conn}
	defer teeConn.Stop()

	resp, err := http.ReadRequest(bufio.NewReader(teeConn))
	if err != nil {
		return teeConn, "", err
	}

	if _, _, err := net.SplitHostPort(resp.Host); err != nil {
		resp.Host = net.JoinHostPort(resp.Host, "80")
	}

	return teeConn, resp.Host, nil
}

func ParseHTTPS(conn net.Conn) (net.Conn, string, error) {
	teeConn := &util.TeeConn{Conn: conn}
	defer teeConn.Stop()

	var domain string
	tls.Server(teeConn, &tls.Config{
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			domain = hello.ServerName
			return nil, nil
		},
	}).Handshake()

	return teeConn, net.JoinHostPort(domain, "443"), nil
}

func withDefaultPort(addr string, port string) (address, host string) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return net.JoinHostPort(addr, port), addr
	}
	return addr, host
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
