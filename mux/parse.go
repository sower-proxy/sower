package mux

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/wweir/sower/util"
)

func ParseHTTP(conn net.Conn) (net.Conn, string, error) {
	teeConn := &util.TeeConn{Conn: conn}
	teeConn.StartOrReset()
	defer teeConn.Stop()

	b := bufio.NewReader(teeConn)
	resp, err := http.ReadRequest(b)
	if err != nil {
		return teeConn, "", err
	}
	if strings.Contains(resp.Host, ":") {
		return teeConn, resp.Host, nil
	}
	return teeConn, resp.Host + ":80", nil
}

func ParseHTTPS(conn net.Conn) (net.Conn, string, error) {
	teeConn := &util.TeeConn{Conn: conn}
	teeConn.StartOrReset()
	defer teeConn.Stop()

	host, _, err := extractSNI(io.Reader(teeConn))
	if err != nil {
		return teeConn, "", err
	}
	if strings.Contains(host, ":") {
		return teeConn, host, nil
	}
	return teeConn, host + ":443", nil
}
