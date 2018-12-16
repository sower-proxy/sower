package parser

import "net"

type TeeConn struct {
	net.Conn
	buf    []byte
	offset int
	tee    bool // read
}

func (t *TeeConn) StartOrReset() {
	t.offset = 0
	t.tee = true
}
func (t *TeeConn) Stop() {
	t.offset = 0
	t.tee = false
}

func (t *TeeConn) Read(b []byte) (n int, err error) {
	length := len(t.buf) - t.offset
	if length > 0 {
		n = copy(b, t.buf[t.offset:])
		t.offset += n
		return
	}

	n, err = t.Conn.Read(b)
	if t.tee {
		t.buf = append(t.buf, b[:n]...)
		t.offset += n
	}
	return n, err
}
