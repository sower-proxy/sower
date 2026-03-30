package socks5

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

type mockConn struct {
	reader io.Reader
	writes bytes.Buffer
}

func newMockConn(data []byte) *mockConn {
	return &mockConn{reader: bytes.NewReader(data)}
}

func (c *mockConn) Read(p []byte) (int, error)       { return c.reader.Read(p) }
func (c *mockConn) Write(p []byte) (int, error)      { return c.writes.Write(p) }
func (c *mockConn) Close() error                     { return nil }
func (c *mockConn) LocalAddr() net.Addr              { return dummyAddr("local") }
func (c *mockConn) RemoteAddr() net.Addr             { return dummyAddr("remote") }
func (c *mockConn) SetDeadline(time.Time) error      { return nil }
func (c *mockConn) SetReadDeadline(time.Time) error  { return nil }
func (c *mockConn) SetWriteDeadline(time.Time) error { return nil }

type dummyAddr string

func (a dummyAddr) Network() string { return "tcp" }
func (a dummyAddr) String() string  { return string(a) }

func TestUnwrapRejectsZeroMethodsWithoutPanic(t *testing.T) {
	conn := newMockConn([]byte{0x05, 0x00})
	if _, err := New().Unwrap(conn); err == nil {
		t.Fatal("expected error for zero auth methods")
	}
}

func TestUnwrapAcceptsNoAuthWhenNotFirstMethod(t *testing.T) {
	req := []byte{
		0x05, 0x02, 0x02, 0x00,
		0x05, 0x01, 0x00, 0x03,
		0x0b,
	}
	req = append(req, []byte("example.com")...)
	req = append(req, 0x01, 0xbb)

	addr, err := New().Unwrap(newMockConn(req))
	if err != nil {
		t.Fatalf("unwrap failed: %v", err)
	}

	host, port := addr.(*AddrHead).Addr()
	if host != "example.com" || port != 443 {
		t.Fatalf("unexpected addr: %s:%d", host, port)
	}
}

func TestWrapRejectsAuthFailure(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 3)
		if _, err := io.ReadFull(server, buf); err != nil {
			return
		}
		_, _ = server.Write([]byte{0x05, 0xff})
	}()

	if err := New().Wrap(client, "example.com", 443); err == nil {
		t.Fatal("expected auth failure")
	}
	<-done
}

func TestWrapRejectsConnectFailure(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)

		auth := make([]byte, 3)
		if _, err := io.ReadFull(server, auth); err != nil {
			return
		}
		_, _ = server.Write([]byte{0x05, 0x00})

		if _, err := io.ReadFull(server, make([]byte, 4)); err != nil {
			return
		}
		var nameLen [1]byte
		if _, err := io.ReadFull(server, nameLen[:]); err != nil {
			return
		}
		if _, err := io.ReadFull(server, make([]byte, int(nameLen[0])+2)); err != nil {
			return
		}

		resp := bytes.NewBuffer(nil)
		_ = binary.Write(resp, binary.BigEndian, reqHead{VER: 5, CMD: 5, RSV: 0, ATYP: 1})
		_, _ = server.Write(resp.Bytes())
	}()

	if err := New().Wrap(client, "example.com", 443); err == nil {
		t.Fatal("expected connect failure")
	}
	<-done
}

func TestWrapRejectsTooLongHost(t *testing.T) {
	if err := New().Wrap(newMockConn(nil), string(bytes.Repeat([]byte{'a'}, 256)), 443); err == nil {
		t.Fatal("expected error for too long host")
	}
}
