package proxy

import (
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang/glog"
	"github.com/wweir/sower/proxy/parser"
	"github.com/wweir/sower/proxy/socks5"
)

// race safe
var resolved = false

func resolveAddr(server *string) {
	if !resolved {
		if addr, err := net.ResolveTCPAddr("tcp", *server); err != nil {
			glog.Errorln(err)
		} else {
			*server = addr.String()
			resolved = true
		}
	}
}

func buildSocks5Conn(c, relay net.Conn) (net.Conn, net.Conn, error) {
	conn, addr, err := parser.ParseAddr(relay)
	if err != nil {
		c.Close()
		relay.Close()
		return nil, nil, err
	}

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		c.Close()
		relay.Close()
		return nil, nil, err
	}

	return socks5.ToSocks5(c, host, port), conn, nil
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
	if _, err := io.Copy(dst, src); err != nil {
		glog.V(1).Infof("%s<>%s -> %s<>%s: %s", src.RemoteAddr(), src.LocalAddr(), dst.LocalAddr(), dst.RemoteAddr(), err)
	}

	if atomic.CompareAndSwapInt32(exitFlag, 0, 1) {
		// wakeup blocked goroutine
		now := time.Now()
		src.SetDeadline(now)
		dst.SetDeadline(now)
	} else {
		src.Close()
		dst.Close()
	}

	wg.Done()
}
