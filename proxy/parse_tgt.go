package proxy

import (
	"bufio"
	"crypto/tls"
	"net"
	"net/http"

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
