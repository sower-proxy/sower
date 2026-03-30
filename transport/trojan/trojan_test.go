package trojan

import (
	"bytes"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

type chunkConn struct {
	reader io.Reader
	writes bytes.Buffer
}

func newChunkConn(data []byte, chunkSize int) *chunkConn {
	return &chunkConn{
		reader: &chunkReader{
			data:  data,
			chunk: chunkSize,
		},
	}
}

func (c *chunkConn) Read(p []byte) (int, error)       { return c.reader.Read(p) }
func (c *chunkConn) Write(p []byte) (int, error)      { return c.writes.Write(p) }
func (c *chunkConn) Close() error                     { return nil }
func (c *chunkConn) LocalAddr() net.Addr              { return dummyAddr("local") }
func (c *chunkConn) RemoteAddr() net.Addr             { return dummyAddr("remote") }
func (c *chunkConn) SetDeadline(time.Time) error      { return nil }
func (c *chunkConn) SetReadDeadline(time.Time) error  { return nil }
func (c *chunkConn) SetWriteDeadline(time.Time) error { return nil }

type chunkReader struct {
	data  []byte
	chunk int
	pos   int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}

	n := r.chunk
	if n > len(p) {
		n = len(p)
	}
	remain := len(r.data) - r.pos
	if n > remain {
		n = remain
	}
	copy(p, r.data[r.pos:r.pos+n])
	r.pos += n
	return n, nil
}

type dummyAddr string

func (a dummyAddr) Network() string { return "tcp" }
func (a dummyAddr) String() string  { return string(a) }

func TestWrapRejectsTooLongHost(t *testing.T) {
	err := New("123").Wrap(newChunkConn(nil, 1), strings.Repeat("a", 256), 443)
	if err == nil {
		t.Fatal("expected error for too long host")
	}
}

func TestUnwrapReadsFullHeader(t *testing.T) {
	conn := newChunkConn(nil, 1)
	if err := New("123").Wrap(conn, "example.com", 443); err != nil {
		t.Fatalf("wrap failed: %v", err)
	}

	addr, err := New("123").Unwrap(newChunkConn(conn.writes.Bytes(), 5))
	if err != nil {
		t.Fatalf("unwrap failed: %v", err)
	}
	if got := addr.String(); got != "example.com:443" {
		t.Fatalf("unexpected addr: %s", got)
	}
}

func TestUnwrapRejectsInvalidCommand(t *testing.T) {
	conn := newChunkConn(nil, 1)
	if err := New("123").Wrap(conn, "example.com", 443); err != nil {
		t.Fatalf("wrap failed: %v", err)
	}

	data := append([]byte(nil), conn.writes.Bytes()...)
	data[58] = 0x03

	if _, err := New("123").Unwrap(newChunkConn(data, len(data))); err == nil {
		t.Fatal("expected invalid command error")
	}
}

func TestUnwrapRejectsInvalidCRLF(t *testing.T) {
	conn := newChunkConn(nil, 1)
	if err := New("123").Wrap(conn, "example.com", 443); err != nil {
		t.Fatalf("wrap failed: %v", err)
	}

	data := append([]byte(nil), conn.writes.Bytes()...)
	data[len(data)-1] = 0x00

	if _, err := New("123").Unwrap(newChunkConn(data, len(data))); err == nil {
		t.Fatal("expected invalid CRLF error")
	}
}
