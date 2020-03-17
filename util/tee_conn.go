package util

import (
	"io"
	"net"
)

type TeeConn struct {
	net.Conn
	buf         []byte
	offset      int
	stop        bool // read
	EnableWrite bool
}

func (t *TeeConn) Reread() {
	t.offset = 0
}
func (t *TeeConn) Reset() {
	t.buf = []byte{}
	t.offset = 0
}
func (t *TeeConn) Stop() {
	t.offset = 0
	t.stop = true
}

func (t *TeeConn) Read(b []byte) (n int, err error) {
	length := len(t.buf) - t.offset
	if length > 0 {
		n = copy(b, t.buf[t.offset:])
		t.offset += n
		return
	}

	n, err = t.Conn.Read(b)
	if !t.stop {
		t.buf = append(t.buf, b[:n]...)
		t.offset += n
	}
	return n, err
}

func (t *TeeConn) Write(b []byte) (n int, err error) {
	if t.stop || t.EnableWrite {
		return t.Conn.Write(b)
	}

	return 0, io.EOF
}
