package trojan

import (
	"strings"
	"testing"

	"github.com/sower-proxy/sower/transport/internal/conntest"
)

func TestWrapRejectsTooLongHost(t *testing.T) {
	err := New("123").Wrap(conntest.NewChunkConn(nil, 1), strings.Repeat("a", 256), 443)
	if err == nil {
		t.Fatal("expected error for too long host")
	}
}

func TestUnwrapReadsFullHeader(t *testing.T) {
	conn := conntest.NewChunkConn(nil, 1)
	if err := New("123").Wrap(conn, "example.com", 443); err != nil {
		t.Fatalf("wrap failed: %v", err)
	}

	addr, err := New("123").Unwrap(conntest.NewChunkConn(conn.Writes.Bytes(), 5))
	if err != nil {
		t.Fatalf("unwrap failed: %v", err)
	}
	if got := addr.String(); got != "example.com:443" {
		t.Fatalf("unexpected addr: %s", got)
	}
}

func TestUnwrapRejectsInvalidCommand(t *testing.T) {
	conn := conntest.NewChunkConn(nil, 1)
	if err := New("123").Wrap(conn, "example.com", 443); err != nil {
		t.Fatalf("wrap failed: %v", err)
	}

	data := append([]byte(nil), conn.Writes.Bytes()...)
	data[58] = 0x03

	if _, err := New("123").Unwrap(conntest.NewChunkConn(data, len(data))); err == nil {
		t.Fatal("expected invalid command error")
	}
}

func TestUnwrapRejectsInvalidCRLF(t *testing.T) {
	conn := conntest.NewChunkConn(nil, 1)
	if err := New("123").Wrap(conn, "example.com", 443); err != nil {
		t.Fatalf("wrap failed: %v", err)
	}

	data := append([]byte(nil), conn.Writes.Bytes()...)
	data[len(data)-1] = 0x00

	if _, err := New("123").Unwrap(conntest.NewChunkConn(data, len(data))); err == nil {
		t.Fatal("expected invalid CRLF error")
	}
}
