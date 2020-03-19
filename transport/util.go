package transport

import (
	"crypto/tls"
	"net"
	"strconv"
)

func Dial(address, target string, password []byte) (net.Conn, error) {
	if addr, ok := IsSocks5Schema(address); ok {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			return nil, err
		}
		if conn, err = ToSocks5(conn, target); err != nil {
			conn.Close()
			return nil, err
		}
		return conn, nil
	}

	host, port, err := net.SplitHostPort(target)
	if err != nil {
		port = "443"
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		return nil, err
	}

	return DialTlsProxyConn(net.JoinHostPort(address, "443"), host, uint16(p), &tls.Config{
		ServerName: address,
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{"http/1.1", "h2"},
	}, password)
}
