package sower

import (
	"bytes"
	"encoding/binary"
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
		reader: &io.LimitedReader{
			R: &chunkReader{
				data:  data,
				chunk: chunkSize,
			},
			N: int64(len(data)),
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
	err := New("123").Wrap(newChunkConn(nil, 1), strings.Repeat("a", maxDomainLength+1), 443)
	if err == nil {
		t.Fatal("expected error for too long host")
	}
}

func TestRoundTripWithMaxLengthHost(t *testing.T) {
	host := strings.Repeat("a", maxDomainLength)
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	done := make(chan error, 1)
	go func() {
		done <- New("123").Wrap(client, host, 443)
	}()

	addr, err := New("123").Unwrap(server)
	if err != nil {
		t.Fatalf("unwrap failed: %v", err)
	}
	if got := addr.String(); got != net.JoinHostPort(host, "443") {
		t.Fatalf("unexpected addr: %s", got)
	}
	if err := <-done; err != nil {
		t.Fatalf("wrap failed: %v", err)
	}
}

func TestUnwrapReadsFullHeader(t *testing.T) {
	target := [maxDomainLength]byte{}
	copy(target[:], "example.com")

	buf := bytes.NewBuffer(nil)
	if err := binary.Write(buf, binary.BigEndian, &Head{
		Cmd:      0x80,
		Checksum: sumChecksum(target, []byte("123")),
		Port:     443,
		TgtAddr:  target,
	}); err != nil {
		t.Fatalf("build header: %v", err)
	}

	addr, err := New("123").Unwrap(newChunkConn(buf.Bytes(), 7))
	if err != nil {
		t.Fatalf("unwrap failed: %v", err)
	}
	if got := addr.String(); got != "example.com:443" {
		t.Fatalf("unexpected addr: %s", got)
	}
}
