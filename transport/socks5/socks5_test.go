package socks5

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"

	"github.com/sower-proxy/sower/transport/internal/conntest"
)

func TestUnwrapRejectsZeroMethodsWithoutPanic(t *testing.T) {
	conn := conntest.NewMockConn([]byte{0x05, 0x00})
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

	addr, err := New().Unwrap(conntest.NewMockConn(req))
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
		_ = binary.Write(resp, binary.BigEndian, replyHead{VER: 5, REP: 5, RSV: 0, ATYP: 1})
		_, _ = server.Write(resp.Bytes())
	}()

	if err := New().Wrap(client, "example.com", 443); err == nil {
		t.Fatal("expected connect failure")
	}
	<-done
}

func TestWrapRejectsTooLongHost(t *testing.T) {
	if err := New().Wrap(conntest.NewMockConn(nil), string(bytes.Repeat([]byte{'a'}, 256)), 443); err == nil {
		t.Fatal("expected error for too long host")
	}
}
