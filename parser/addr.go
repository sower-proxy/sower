package parser

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

type TeeConn struct {
	net.Conn
	buf    []byte
	offset int
	Tee    bool // read
}

func (t *TeeConn) Reset() {
	t.offset = 0
}

func (t *TeeConn) Read(b []byte) (n int, err error) {
	length := len(t.buf) - t.offset
	if length > 0 {
		n = copy(b, t.buf[t.offset:])
		t.offset += n
		return
	}

	n, err = t.Conn.Read(b)
	if t.Tee {
		t.buf = append(t.buf, b[:n]...)
		t.offset += n
	}
	return n, err
}

func ParseAddr(conn net.Conn) (teeConn *TeeConn, addr string, err error) {
	teeConn = &TeeConn{Conn: conn, Tee: true}
	defer func() {
		teeConn.Reset()
		teeConn.Tee = false
	}()

	buf := make([]byte, 1)
	if n, err := teeConn.Read(buf); err != nil || n != 1 {
		return teeConn, "", fmt.Errorf("Read conn fail: %v, readed: %d %v", err, n, buf)
	}

	teeConn.Reset()

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
