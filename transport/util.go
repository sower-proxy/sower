package transport

import (
	"crypto/tls"
	"net"
	"strconv"

	"github.com/wweir/sower/util"
)

func Dial(address, target string, password []byte, shouldProxy func(string) bool) (net.Conn, error) {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return nil, err
	}

	if !shouldProxy(host) {
		return net.Dial("tcp", target)
	}

	p, err := strconv.Atoi(port)
	if err != nil {
		return nil, err
	}

	if addr, ok := IsSocks5Schema(address); ok {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			return nil, err
		}
		if conn, err = ToSocks5(conn, host, uint16(p)); err != nil {
			conn.Close()
			return nil, err
		}
		return conn, nil
	}

	address, _ = util.WithDefaultPort(address, "443")
	// tls.Config is same as golang http pkg default behavior
	return DialTlsProxyConn(address, host, uint16(p), &tls.Config{}, password)
}
