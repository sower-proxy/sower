package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sower-proxy/sower/internal/admin"
	"github.com/sower-proxy/sower/pkg/upstreamtls"
	"github.com/sower-proxy/sower/router"
)

func TestGenProxyDialRejectsUnknownProxyType(t *testing.T) {
	t.Parallel()

	if _, err := GenProxyDial("unknown", "example.com", "", "8.8.8.8", upstreamtls.Options{}, nil); err == nil {
		t.Fatal("expected error for unknown proxy type")
	}
}

func TestGenProxyDialRejectsInvalidTLSClientHello(t *testing.T) {
	t.Parallel()

	_, err := GenProxyDial("sower", "example.com", "", "8.8.8.8", upstreamtls.Options{
		ClientHello: "invalid",
	}, nil)
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

func TestHostOnly(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		addr string
		want string
	}{
		{addr: "example.com:8080", want: "example.com"},
		{addr: "example.com", want: "example.com"},
		{addr: "[2001:db8::1]:443", want: "2001:db8::1"},
	} {
		t.Run(test.addr, func(t *testing.T) {
			if got := hostOnly(test.addr); got != test.want {
				t.Fatalf("hostOnly(%q) = %q, want %q", test.addr, got, test.want)
			}
		})
	}
}

func TestHandleSocks5ConnReturnsForbiddenForBlockedConnect(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()

	r := newTestRouter()
	go handleSocks5Conn(server, r, newTestStats(t))

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
	stats := newTestStats(t)
	go handleSocks5Conn(server, r, stats)

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

	// A blocked target is a policy refusal like a self target: neither feeds
	// the service alert, and only the self target has a refusal metric.
	assertNoProxyServiceErrors(t, stats)
	rec := httptest.NewRecorder()
	stats.Metrics().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if strings.Contains(rec.Body.String(), "sower_proxy_self_refusals_total") {
		t.Fatalf("blocked target counted as a self-target refusal:\n%s", rec.Body.String())
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

func TestHandleHTTPConnUsesProxyOnlyEvenWhenBlocked(t *testing.T) {
	t.Parallel()

	downstreamServer, downstreamClient := net.Pipe()
	defer downstreamClient.Close()

	upstreamClient, upstreamServer := net.Pipe()
	defer upstreamServer.Close()

	request := []byte("GET / HTTP/1.1\r\nHost: example.com\r\n\r\n")
	var (
		gotHost string
		gotPort uint16
	)
	r := &router.Router{
		BlockRule:  router.NewRuleSet("example.com"),
		DirectRule: router.NewRuleSet(),
		ProxyRule:  router.NewRuleSet(),
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
		handleHTTPConn(downstreamServer, r, newTestStats(t))
	}()

	downstreamClient.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := downstreamClient.Write(request); err != nil {
		t.Fatalf("write downstream request: %v", err)
	}

	gotRequest := make([]byte, len(request))
	upstreamServer.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(upstreamServer, gotRequest); err != nil {
		t.Fatalf("read upstream request: %v", err)
	}
	if string(gotRequest) != string(request) {
		t.Fatalf("request was not relayed intact: %q", gotRequest)
	}
	if gotHost != "example.com" || gotPort != 80 {
		t.Fatalf("unexpected proxy target: %s:%d", gotHost, gotPort)
	}

	_ = downstreamClient.Close()
	_ = upstreamServer.Close()
	waitForHandler(t, &wg)
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
		BlockRule:  router.NewRuleSet("github.com"),
		DirectRule: router.NewRuleSet(),
		ProxyRule:  router.NewRuleSet(),
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
		handleHTTPSConn(downstreamServer, r, newTestStats(t))
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
	select {
	case err := <-writeErrCh:
		if err != nil {
			t.Fatalf("write downstream client hello: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("write downstream client hello timed out")
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
	select {
	case err := <-readErrCh:
		if err != nil {
			t.Fatalf("read downstream response: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("read downstream response timed out")
	}
	if string(gotResp) != string(response) {
		t.Fatalf("unexpected downstream response: %q", gotResp)
	}
	if gotHost != "github.com" || gotPort != 443 {
		t.Fatalf("unexpected proxy target: %s:%d", gotHost, gotPort)
	}

	_ = downstreamClient.Close()
	_ = upstreamServer.Close()
	waitForHandler(t, &wg)
}

func TestHandleHTTPSConnRelaysClientHelloAfterReadingSNI(t *testing.T) {
	server, client := net.Pipe()
	upstreamClient, upstreamServer := net.Pipe()
	defer server.Close()
	defer client.Close()
	defer upstreamClient.Close()
	defer upstreamServer.Close()

	hostCh := make(chan string, 1)
	r := &router.Router{
		ProxyDial: func(network, host string, port uint16) (net.Conn, error) {
			if network != "tcp" {
				t.Errorf("unexpected network: %s", network)
			}
			if port != 443 {
				t.Errorf("unexpected port: %d", port)
			}
			hostCh <- host
			return upstreamClient, nil
		},
	}

	go handleHTTPSConn(server, r, newTestStats(t))

	tlsClient := tls.Client(client, &tls.Config{
		ServerName:         "example.com",
		InsecureSkipVerify: true,
	})
	_ = client.SetDeadline(time.Now().Add(2 * time.Second))
	_ = upstreamServer.SetDeadline(time.Now().Add(2 * time.Second))

	handshakeDone := make(chan error, 1)
	go func() {
		handshakeDone <- tlsClient.Handshake()
	}()

	select {
	case host := <-hostCh:
		if host != "example.com" {
			t.Fatalf("unexpected SNI host: %q", host)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("proxy dial was not called")
	}

	recordHeader := make([]byte, 5)
	if _, err := io.ReadFull(upstreamServer, recordHeader); err != nil {
		t.Fatalf("read relayed TLS record header: %v", err)
	}
	if recordHeader[0] != 0x16 {
		t.Fatalf("expected relayed TLS handshake record, got %#x", recordHeader[0])
	}

	_ = client.Close()
	_ = upstreamServer.Close()
	select {
	case <-handshakeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("tls client handshake did not return after connections closed")
	}
}

func waitForHandler(t *testing.T, wg *sync.WaitGroup) {
	t.Helper()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not exit")
	}
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
		BlockRule:  router.NewRuleSet("example.com"),
		DirectRule: router.NewRuleSet(),
		ProxyRule:  router.NewRuleSet(),
		ProxyDial: func(network, host string, port uint16) (net.Conn, error) {
			return nil, router.ErrBlocked
		},
	}
}

func newTestStats(t *testing.T) *admin.Stats {
	t.Helper()
	s, err := admin.NewStats()
	if err != nil {
		t.Fatalf("new stats: %v", err)
	}
	return s
}

func TestHandleHTTPConnConnectTunnelsToTargetPort(t *testing.T) {
	t.Parallel()

	downstreamServer, downstreamClient := net.Pipe()
	defer downstreamClient.Close()

	upstreamClient, upstreamServer := net.Pipe()
	defer upstreamServer.Close()

	request := []byte("CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n")
	tunnelData := []byte("\x16\x03\x01\x00\x05hello")
	var (
		gotHost string
		gotPort uint16
	)
	r := &router.Router{
		BlockRule:  router.NewRuleSet("example.com"),
		DirectRule: router.NewRuleSet(),
		ProxyRule:  router.NewRuleSet(),
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
		handleHTTPConn(downstreamServer, r, newTestStats(t))
	}()

	downstreamClient.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := downstreamClient.Write(request); err != nil {
		t.Fatalf("write connect request: %v", err)
	}

	// The proxy must acknowledge CONNECT before tunneling.
	reply := make([]byte, len("HTTP/1.1 200 Connection established\r\n\r\n"))
	downstreamClient.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(downstreamClient, reply); err != nil {
		t.Fatalf("read connect reply: %v", err)
	}
	if !strings.HasPrefix(string(reply), "HTTP/1.1 200") {
		t.Fatalf("unexpected connect reply: %q", string(reply))
	}

	if _, err := downstreamClient.Write(tunnelData); err != nil {
		t.Fatalf("write tunnel data: %v", err)
	}

	// The request head must be consumed; only raw tunnel data reaches the target.
	gotData := make([]byte, len(tunnelData))
	upstreamServer.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(upstreamServer, gotData); err != nil {
		t.Fatalf("read upstream data: %v", err)
	}
	if string(gotData) != string(tunnelData) {
		t.Fatalf("unexpected upstream data: %q", gotData)
	}
	if gotHost != "example.com" || gotPort != 443 {
		t.Fatalf("unexpected proxy target: %s:%d", gotHost, gotPort)
	}

	_ = downstreamClient.Close()
	_ = upstreamServer.Close()
	waitForHandler(t, &wg)
}

// A refused proxy request must be answered rather than silently closed: only a
// status tells the client that the refusal is policy (this proxy's own
// listener, or a blocked target) and not a broken proxy.
func TestHandleHTTPConnAnswersRefusedSelfTarget(t *testing.T) {
	t.Parallel()

	downstreamServer, downstreamClient := net.Pipe()
	defer downstreamClient.Close()
	t.Cleanup(func() { _ = downstreamServer.Close() })

	r, err := router.NewRouter(nil, "", "", "", func(_, _ string, _ uint16) (net.Conn, error) {
		t.Error("proxy dial reached for a self target")
		return nil, nil
	})
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	r.SetSelfProxyAddrs("100.64.1.1:80")

	stats := newTestStats(t)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		handleHTTPConn(downstreamServer, r, stats)
	}()

	if _, err := downstreamClient.Write([]byte("GET / HTTP/1.1\r\nHost: 100.64.1.1\r\n\r\n")); err != nil {
		t.Fatalf("write downstream request: %v", err)
	}
	_ = downstreamClient.SetReadDeadline(time.Now().Add(2 * time.Second))
	response, err := io.ReadAll(downstreamClient)
	if err != nil {
		t.Fatalf("read refusal response: %v", err)
	}
	if !strings.HasPrefix(string(response), "HTTP/1.1 403 Forbidden") {
		t.Fatalf("refusal response = %q, want a 403 status", response)
	}

	waitForHandler(t, &wg)
	assertNoProxyServiceErrors(t, stats)
	assertSelfRefusalCounted(t, stats, "http")
}

// A self-target refusal is a policy answer, so the SOCKS5 listener must reply
// with connection-not-allowed and must not record a service error.
func TestHandleSocks5ConnSelfTargetRefusalIsNotAServiceError(t *testing.T) {
	t.Parallel()

	server, client := net.Pipe()
	defer client.Close()

	r := newTestRouter()
	r.SetSelfProxyAddrs("100.64.1.1:80")
	stats := newTestStats(t)
	go handleSocks5Conn(server, r, stats)

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

	// CONNECT 100.64.1.1:80, the address this client serves proxy traffic on.
	req := []byte{0x05, 0x01, 0x00, 0x01, 100, 64, 1, 1, 0x00, 0x50}
	if _, err := client.Write(req); err != nil {
		t.Fatalf("write connect request: %v", err)
	}
	reply := make([]byte, 10)
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatalf("read connect reply: %v", err)
	}
	if reply[1] != 0x02 {
		t.Fatalf("connect reply = %#x, want connection-not-allowed (0x02)", reply[1])
	}

	assertNoProxyServiceErrors(t, stats)
	assertSelfRefusalCounted(t, stats, "socks5")
}

// The SOCKS5 listener also accepts HTTP proxy requests (non-0x05 first byte);
// a refused self target there is answered 403 and is not a service error.
func TestHandleSocks5ConnHTTPSelfTargetRefusalIsNotAServiceError(t *testing.T) {
	t.Parallel()

	server, client := net.Pipe()
	defer client.Close()

	r := newTestRouter()
	r.SetSelfProxyAddrs("100.64.1.1:80")
	stats := newTestStats(t)
	go handleSocks5Conn(server, r, stats)

	_ = client.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.WriteString(client, "GET http://100.64.1.1/ HTTP/1.1\r\nHost: 100.64.1.1\r\n\r\n"); err != nil {
		t.Fatalf("write proxy request: %v", err)
	}
	response, err := io.ReadAll(client)
	if err != nil {
		t.Fatalf("read refusal response: %v", err)
	}
	if !strings.HasPrefix(string(response), "HTTP/1.1 403 Forbidden") {
		t.Fatalf("refusal response = %q, want a 403 status", response)
	}

	assertNoProxyServiceErrors(t, stats)
	assertSelfRefusalCounted(t, stats, "socks5")
}

// A self-target refusal on the HTTPS/SNI path must not count as a service
// failure either. The router's guard is covered in the router package; here a
// stubbed dialer returns the same sentinel to pin the handler's classification.
func TestHandleHTTPSConnDoesNotCountSelfTargetRefusal(t *testing.T) {
	t.Parallel()

	server, client := net.Pipe()
	defer client.Close()

	dialed := make(chan struct{}, 1)
	r := &router.Router{
		BlockRule:  router.NewRuleSet(),
		DirectRule: router.NewRuleSet(),
		ProxyRule:  router.NewRuleSet(),
		ProxyDial: func(_, _ string, _ uint16) (net.Conn, error) {
			dialed <- struct{}{}
			return nil, fmt.Errorf("proxy dial 100.64.1.1:443: %w", router.ErrSelfProxyTarget)
		},
	}
	stats := newTestStats(t)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		handleHTTPSConn(server, r, stats)
	}()

	hello := generateTLSClientHelloRecord(t, "example.com")
	if _, err := client.Write(hello); err != nil {
		t.Fatalf("write client hello: %v", err)
	}
	select {
	case <-dialed:
	case <-time.After(2 * time.Second):
		t.Fatal("proxy dial was not reached")
	}

	waitForHandler(t, &wg)
	assertNoProxyServiceErrors(t, stats)
	assertSelfRefusalCounted(t, stats, "https")
}

// The suppression must not swallow real dial failures: they still feed the
// service alert.
func TestHandleHTTPConnCountsRealDialFailure(t *testing.T) {
	t.Parallel()

	downstreamServer, downstreamClient := net.Pipe()
	defer downstreamClient.Close()
	t.Cleanup(func() { _ = downstreamServer.Close() })

	r := &router.Router{
		BlockRule:  router.NewRuleSet(),
		DirectRule: router.NewRuleSet(),
		ProxyRule:  router.NewRuleSet(),
		ProxyDial: func(_, _ string, _ uint16) (net.Conn, error) {
			return nil, errors.New("dial tcp: i/o timeout")
		},
	}
	stats := newTestStats(t)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		handleHTTPConn(downstreamServer, r, stats)
	}()

	if _, err := downstreamClient.Write([]byte("GET http://example.com/ HTTP/1.1\r\nHost: example.com\r\n\r\n")); err != nil {
		t.Fatalf("write downstream request: %v", err)
	}
	_ = downstreamClient.SetReadDeadline(time.Now().Add(2 * time.Second))
	response, err := io.ReadAll(downstreamClient)
	if err != nil {
		t.Fatalf("read failure response: %v", err)
	}
	if !strings.HasPrefix(string(response), "HTTP/1.1 502 Bad Gateway") {
		t.Fatalf("failure response = %q, want a 502 status", response)
	}

	waitForHandler(t, &wg)
	snap := stats.Snapshot(admin.DomainSortBytes, admin.SourceAll, "")
	if snap.Errors.Dial != 1 {
		t.Fatalf("dial errors = %d, want 1 for a real dial failure", snap.Errors.Dial)
	}
	if len(snap.Events) != 1 {
		t.Fatalf("error events = %d, want 1 for a real dial failure", len(snap.Events))
	}
}

func TestIsPolicyRefusal(t *testing.T) {
	t.Parallel()

	for _, err := range []error{
		router.ErrBlocked,
		router.ErrSelfProxyTarget,
		fmt.Errorf("proxy dial 100.64.1.1:80: %w", router.ErrSelfProxyTarget),
	} {
		if !isPolicyRefusal(err) {
			t.Fatalf("isPolicyRefusal(%v) = false, want true", err)
		}
	}
	if isPolicyRefusal(errors.New("dial tcp: i/o timeout")) {
		t.Fatal("isPolicyRefusal(network error) = true, want false")
	}
}

// assertNoProxyServiceErrors reports whether a policy refusal leaked into the
// console's service-failure counters or event ring.
func assertNoProxyServiceErrors(t *testing.T, stats *admin.Stats) {
	t.Helper()

	snap := stats.Snapshot(admin.DomainSortBytes, admin.SourceAll, "")
	if snap.Errors.Dial != 0 || snap.Errors.DNS != 0 || snap.Errors.Accept != 0 {
		t.Fatalf("policy refusal counted as a proxy failure: %+v", snap.Errors)
	}
	if len(snap.Events) != 0 {
		t.Fatalf("policy refusal entered the error ring: %+v", snap.Events)
	}
}

// assertSelfRefusalCounted checks that a refusal stays observable through the
// dedicated metric instead of the alert counters.
func assertSelfRefusalCounted(t *testing.T, stats *admin.Stats, protocol string) {
	t.Helper()

	rec := httptest.NewRecorder()
	stats.Metrics().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	want := fmt.Sprintf(`sower_proxy_self_refusals_total{protocol=%q} 1`, protocol)
	if !strings.Contains(body, want) {
		t.Fatalf("expected %q in the metrics export, got:\n%s", want, body)
	}
	if strings.Contains(body, "sower_proxy_errors_total") {
		t.Fatalf("refusal was also counted as a proxy error:\n%s", body)
	}
}
