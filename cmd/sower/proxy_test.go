package main

import (
	"crypto/tls"
	"io"
	"net"
	"strings"
	"sync"
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

func TestPeekTLSClientHelloServerName(t *testing.T) {
	t.Parallel()

	server, client := net.Pipe()
	defer client.Close()

	hello := generateTLSClientHelloRecord(t, "github.com")
	go func() {
		_, _ = client.Write(hello)
		_ = client.Close()
	}()

	got, err := peekTLSClientHelloServerName(server)
	_ = server.Close()
	if err != nil {
		t.Fatalf("peek tls client hello: %v", err)
	}
	if got != "github.com" {
		t.Fatalf("unexpected server name: %q", got)
	}
}

func TestHandleHTTPSConnRelaysClientHelloToUpstream(t *testing.T) {
	t.Parallel()

	downstreamServer, downstreamClient := net.Pipe()
	defer downstreamClient.Close()

	upstreamClient, upstreamServer := net.Pipe()
	defer upstreamServer.Close()

	hello := generateTLSClientHelloRecord(t, "github.com")
	response := []byte("upstream-response")

	var (
		gotHost string
		gotPort uint16
	)
	r := &router.Router{
		BlockRule:  suffixtree.NewNodeFromRules(),
		DirectRule: suffixtree.NewNodeFromRules(),
		ProxyRule:  suffixtree.NewNodeFromRules(),
		ProxyDial: func(network, host string, port uint16) (net.Conn, error) {
			gotHost = host
			gotPort = port
			return upstreamClient, nil
		},
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		handleHTTPSConn(downstreamServer, r)
	}()

	writeErrCh := make(chan error, 1)
	go func() {
		_, err := downstreamClient.Write(hello)
		writeErrCh <- err
	}()

	gotHello := make([]byte, len(hello))
	upstreamServer.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(upstreamServer, gotHello); err != nil {
		t.Fatalf("read upstream client hello: %v", err)
	}
	if string(gotHello) != string(hello) {
		t.Fatal("client hello was not relayed intact")
	}
	if err := <-writeErrCh; err != nil {
		t.Fatalf("write downstream client hello: %v", err)
	}

	readErrCh := make(chan error, 1)
	gotResp := make([]byte, len(response))
	go func() {
		_, err := io.ReadFull(downstreamClient, gotResp)
		readErrCh <- err
	}()

	upstreamServer.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := upstreamServer.Write(response); err != nil {
		t.Fatalf("write upstream response: %v", err)
	}
	if err := <-readErrCh; err != nil {
		t.Fatalf("read downstream response: %v", err)
	}
	if string(gotResp) != string(response) {
		t.Fatalf("unexpected downstream response: %q", gotResp)
	}
	if gotHost != "github.com" || gotPort != 443 {
		t.Fatalf("unexpected proxy target: %s:%d", gotHost, gotPort)
	}

	_ = downstreamClient.Close()
	_ = upstreamServer.Close()
	wg.Wait()
}

func generateTLSClientHelloRecord(t *testing.T, serverName string) []byte {
	t.Helper()

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	errCh := make(chan error, 1)
	go func() {
		tlsConn := tls.Client(clientConn, &tls.Config{
			ServerName:         serverName,
			InsecureSkipVerify: true,
		})
		errCh <- tlsConn.Handshake()
		_ = tlsConn.Close()
	}()

	serverConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	header := make([]byte, tlsRecordHeaderLen)
	if _, err := io.ReadFull(serverConn, header); err != nil {
		t.Fatalf("read tls record header: %v", err)
	}

	recordLen := int(header[3])<<8 | int(header[4])
	record := make([]byte, len(header)+recordLen)
	copy(record, header)
	if _, err := io.ReadFull(serverConn, record[len(header):]); err != nil {
		t.Fatalf("read tls record body: %v", err)
	}

	_ = serverConn.Close()
	<-errCh
	return record
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
