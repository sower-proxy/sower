package sower

import (
	"bytes"
	"encoding/binary"
	"net"
	"strings"
	"testing"

	"github.com/sower-proxy/sower/transport/internal/conntest"
)

func TestWrapRejectsTooLongHost(t *testing.T) {
	err := New("123").Wrap(conntest.NewChunkConn(nil, 1), strings.Repeat("a", maxDomainLength+1), 443)
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

	addr, err := New("123").Unwrap(conntest.NewChunkConn(buf.Bytes(), 7))
	if err != nil {
		t.Fatalf("unwrap failed: %v", err)
	}
	if got := addr.String(); got != "example.com:443" {
		t.Fatalf("unexpected addr: %s", got)
	}
}
