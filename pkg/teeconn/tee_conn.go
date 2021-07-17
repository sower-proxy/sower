package teeconn

import (
	"io"
	"net"
)

type Conn struct {
	net.Conn

	buf    []byte
	offset int
	stop   bool // read
	err    error
}

func New(c net.Conn) *Conn {
	return &Conn{Conn: c}
}

func (t *Conn) Reread() {
	t.offset = 0
}
func (t *Conn) Reset() {
	t.buf = []byte{}
	t.offset = 0
}
func (t *Conn) Stop() *Conn {
	t.stop = true
	return t
}

func (t *Conn) Read(b []byte) (n int, err error) {
	length := len(t.buf) - t.offset
	if length > 0 {
		n = copy(b, t.buf[t.offset:])
		t.offset += n
		return n, t.err
	}

	n, t.err = t.Conn.Read(b)
	if !t.stop {
		t.buf = append(t.buf, b[:n]...)
		t.offset += n
	}

	return n, t.err
}

func (t *Conn) Write(b []byte) (n int, err error) {
	if t.stop {
		return t.Conn.Write(b)
	}

	return 0, io.ErrShortWrite
}
