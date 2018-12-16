package parser

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

func ParseAddr(conn net.Conn) (teeConn *TeeConn, addr string, err error) {
	teeConn = &TeeConn{Conn: conn}
	teeConn.StartOrReset()
	defer teeConn.Stop()

	buf := make([]byte, 1)
	if n, err := teeConn.Read(buf); err != nil || n != 1 {
		return teeConn, "", fmt.Errorf("Read conn fail: %v, readed: %d %v", err, n, buf)
	}

	teeConn.StartOrReset()

	// https
	if buf[0] == 0x16 { // SSL handleshake
		host, _, err := extractSNI(io.Reader(teeConn))
		if err != nil {
			return teeConn, "", err
		}
		if strings.Contains(host, ":") {
			return teeConn, host, nil
		}
		return teeConn, host + ":443", nil
	}

	// http
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
