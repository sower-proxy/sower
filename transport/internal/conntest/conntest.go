package conntest

import (
	"bytes"
	"io"
	"net"
	"time"
)

// ChunkConn is a net.Conn that reads data in fixed-size chunks.
type ChunkConn struct {
	Reader io.Reader
	Writes bytes.Buffer
}

func NewChunkConn(data []byte, chunkSize int) *ChunkConn {
	return &ChunkConn{
		Reader: &io.LimitedReader{
			R: &chunkReader{
				data:  data,
				chunk: chunkSize,
			},
			N: int64(len(data)),
		},
	}
}

func (c *ChunkConn) Read(p []byte) (int, error)       { return c.Reader.Read(p) }
func (c *ChunkConn) Write(p []byte) (int, error)      { return c.Writes.Write(p) }
func (c *ChunkConn) Close() error                     { return nil }
func (c *ChunkConn) LocalAddr() net.Addr              { return DummyAddr("local") }
func (c *ChunkConn) RemoteAddr() net.Addr             { return DummyAddr("remote") }
func (c *ChunkConn) SetDeadline(time.Time) error      { return nil }
func (c *ChunkConn) SetReadDeadline(time.Time) error  { return nil }
func (c *ChunkConn) SetWriteDeadline(time.Time) error { return nil }

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

type DummyAddr string

func (a DummyAddr) Network() string { return "tcp" }
func (a DummyAddr) String() string  { return string(a) }

// MockConn is a net.Conn backed by a static byte slice for reading.
type MockConn struct {
	Reader io.Reader
	Writes bytes.Buffer
}

func NewMockConn(data []byte) *MockConn {
	return &MockConn{Reader: bytes.NewReader(data)}
}

func (c *MockConn) Read(p []byte) (int, error)       { return c.Reader.Read(p) }
func (c *MockConn) Write(p []byte) (int, error)      { return c.Writes.Write(p) }
func (c *MockConn) Close() error                     { return nil }
func (c *MockConn) LocalAddr() net.Addr              { return DummyAddr("local") }
func (c *MockConn) RemoteAddr() net.Addr             { return DummyAddr("remote") }
func (c *MockConn) SetDeadline(time.Time) error      { return nil }
func (c *MockConn) SetReadDeadline(time.Time) error  { return nil }
func (c *MockConn) SetWriteDeadline(time.Time) error { return nil }
