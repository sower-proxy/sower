package main

import (
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/sower-proxy/sower/pkg/suffixtree"
	"github.com/sower-proxy/sower/pkg/upstreamtls"
	"github.com/sower-proxy/sower/router"
)

func TestGenProxyDialRejectsUnknownProxyType(t *testing.T) {
	t.Parallel()

	if _, err := GenProxyDial("unknown", "example.com", "", "8.8.8.8", upstreamtls.Options{}); err == nil {
		t.Fatal("expected error for unknown proxy type")
	}
}

func TestGenProxyDialRejectsInvalidTLSClientHello(t *testing.T) {
	t.Parallel()

	_, err := GenProxyDial("sower", "example.com", "", "8.8.8.8", upstreamtls.Options{
		ClientHello: "invalid",
	})
	if err == nil {
		t.Fatal("expected error for invalid TLS client hello")
	}
}

func TestUpstreamDialAddrAddsDefaultPort(t *testing.T) {
	t.Parallel()

	addr, err := upstreamDialAddr("example.com", "443")
	if err != nil {
		t.Fatalf("upstream dial addr: %v", err)
	}
	if addr != "example.com:443" {
		t.Fatalf("unexpected dial addr: %q", addr)
	}
}

func TestUpstreamDialAddrPreservesExplicitPort(t *testing.T) {
	t.Parallel()

	addr, err := upstreamDialAddr("example.com:8443", "443")
	if err != nil {
		t.Fatalf("upstream dial addr: %v", err)
	}
	if addr != "example.com:8443" {
		t.Fatalf("unexpected dial addr: %q", addr)
	}
}

func TestHandleSocks5ConnReturnsForbiddenForBlockedConnect(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()

	r := newTestRouter()
	go handleSocks5Conn(server, r)

	client.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.WriteString(client, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n"); err != nil {
		t.Fatalf("write request: %v", err)
	}

	resp, err := io.ReadAll(client)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if !strings.HasPrefix(string(resp), "HTTP/1.1 403 Forbidden") {
		t.Fatalf("unexpected response: %q", string(resp))
	}
}

func TestHandleSocks5ConnReturnsSocks5FailureForBlockedRequest(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()

	r := newTestRouter()
	go handleSocks5Conn(server, r)

	client.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := client.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatalf("write auth request: %v", err)
	}

	authResp := make([]byte, 2)
	if _, err := io.ReadFull(client, authResp); err != nil {
		t.Fatalf("read auth response: %v", err)
	}
	if authResp[0] != 0x05 || authResp[1] != 0x00 {
		t.Fatalf("unexpected auth response: %v", authResp)
	}

	req := []byte{0x05, 0x01, 0x00, 0x03, 0x0b}
	req = append(req, []byte("example.com")...)
	req = append(req, 0x01, 0xbb)
	if _, err := client.Write(req); err != nil {
		t.Fatalf("write connect request: %v", err)
	}

	reply := make([]byte, 10)
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read connect reply: %v", err)
	}
	if reply[1] != 0x02 {
		t.Fatalf("expected connection-not-allowed reply, got %d", reply[1])
	}
}

func newTestRouter() *router.Router {
	return &router.Router{
		BlockRule:  suffixtree.NewNodeFromRules("example.com"),
		DirectRule: suffixtree.NewNodeFromRules(),
		ProxyRule:  suffixtree.NewNodeFromRules(),
		ProxyDial: func(network, host string, port uint16) (net.Conn, error) {
			return nil, router.ErrBlocked
		},
	}
}
