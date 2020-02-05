package main

import (
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wweir/sower/conf"
	"github.com/wweir/sower/dns"
	"github.com/wweir/sower/internal/socks5"
	"github.com/wweir/sower/mux"
	"github.com/wweir/utils/log"
)

func main() {
	go dns.ServeDNS()

	go proxy(conf.Conf.Upstream.Socks5, conf.Conf.Downstream.ServeIP, "80", mux.ParseHTTP)
	go proxy(conf.Conf.Upstream.Socks5, conf.Conf.Downstream.ServeIP, "443", mux.ParseHTTPS)

	for port, target := range conf.Conf.Router.PortMapping {
		go proxy(conf.Conf.Upstream.Socks5, conf.Conf.Downstream.ServeIP, port,
			func(conn net.Conn) (net.Conn, string, error) {
				return conn, target, nil
			})
	}

	select {}
}

func proxy(socks5Addr, serveIP, port string, mux func(net.Conn) (net.Conn, string, error)) {
	ln, err := net.Listen("tcp", net.JoinHostPort(serveIP, port))
	if err != nil {
		log.Fatalw("listen", "ip", serveIP, "port", port, "err", err)
	}

	log.Infow("start proxy", "port", port)
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Fatalw("listen", "ip", serveIP, "port", port, "err", err)
		}

		if conn, target, err := mux(conn); err != nil {
			log.Errorw("mux parse", "target", target, "err", err)
			continue
		} else {
			go relay(socks5Addr, target, conn)
		}
	}
}

func relay(socks5Addr, target string, conn net.Conn) {
	var socks5Conn net.Conn
	if c, err := net.Dial("tcp", socks5Addr); err != nil {
		log.Errorw("dial socks5", "addr", socks5Addr, "err", err)
		return
	} else if host, port, err := net.SplitHostPort(target); err != nil {
		log.Errorw("dial socks5", "target", target, "err", err)
		return
	} else {
		socks5Conn = socks5.ToSocks5(c, host, port)
	}

	wg := &sync.WaitGroup{}
	exitFlag := new(int32)
	wg.Add(2)
	go redirect(conn, socks5Conn, wg, exitFlag)
	redirect(socks5Conn, conn, wg, exitFlag)
	wg.Wait()
}

func redirect(dst, src net.Conn, wg *sync.WaitGroup, exitFlag *int32) {
	io.Copy(dst, src)

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
