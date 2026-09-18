package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sower-proxy/sower/config"
	transportSower "github.com/sower-proxy/sower/transport/sower"
	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

func TestSanitizeConfig(t *testing.T) {
	t.Parallel()

	cfg := config.SowerdConfig{
		LogLevel: slog.LevelDebug,
		ServeIP:  "0.0.0.0",
		Password: "secret",
		FakeSite: "127.0.0.1:8080",
	}
	cfg.Cert.Email = "ops@example.com"
	cfg.Cert.Cert = "/tmp/cert.pem"
	cfg.Cert.Key = "/tmp/key.pem"

	got := sanitizeConfig(cfg)
	if got["fake_site"] != cfg.FakeSite {
		t.Fatalf("unexpected fake_site: %#v", got["fake_site"])
	}
	if _, ok := got["password"]; ok {
		t.Fatal("password must not be logged")
	}
}

func TestIsLocalRemoteAddr(t *testing.T) {
	t.Parallel()

	// localAddrIsLocal reports whether an address is loopback; it can only
	// test the loopback half of isLocalRemoteAddr without knowing the test
	// host's interface addresses.
	tests := []struct {
		addr string
		want bool
	}{
		{addr: "127.0.0.1:1234", want: true},
		{addr: "[::1]:443", want: true},
		{addr: "10.0.0.1:1234", want: false},
		{addr: "not-an-addr", want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.addr, func(t *testing.T) {
			t.Parallel()
			if got := isLocalRemoteAddr(tt.addr); got != tt.want {
				t.Fatalf("isLocalRemoteAddr(%q) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}

func TestHasInstallFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "short flag", args: []string{"-i"}, want: true},
		{name: "long flag", args: []string{"--install"}, want: true},
		{name: "missing flag", args: []string{"-c", "/etc/sower/sowerd.toml"}, want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := hasInstallFlag(tt.args); got != tt.want {
				t.Fatalf("hasInstallFlag(%q) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestResolveCacheDir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		userCache   func() (string, error)
		fallbackDir string
		wantDir     string
		wantErr     bool
	}{
		{
			name: "user cache dir available",
			userCache: func() (string, error) {
				return "/tmp/cache", nil
			},
			fallbackDir: "/var/cache/sower",
			wantDir:     filepath.Join("/tmp/cache", "sower"),
		},
		{
			name: "fallback to system cache dir",
			userCache: func() (string, error) {
				return "", errors.New("missing home")
			},
			fallbackDir: "/var/cache/sower",
			wantDir:     "/var/cache/sower",
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotDir, err := resolveCacheDir(tt.userCache, tt.fallbackDir)
			if tt.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotDir != tt.wantDir {
				t.Fatalf("resolveCacheDir() = %q, want %q", gotDir, tt.wantDir)
			}
		})
	}
}

func TestSiteRouterLookup(t *testing.T) {
	t.Parallel()

	router := newSiteRouter([]config.SiteRoute{
		{Domains: []string{"a.example.com", "B.Example.COM"}, Upstream: "http://127.0.0.1:9000"},
		{Domains: []string{"c.example.com"}, Upstream: "https://backend.example.com"},
	})

	tests := []struct {
		sni  string
		want string
	}{
		{sni: "a.example.com", want: "127.0.0.1:9000"},
		{sni: "b.example.com", want: "127.0.0.1:9000"},
		{sni: "B.EXAMPLE.COM", want: "127.0.0.1:9000"},
		{sni: "c.example.com", want: "backend.example.com"},
		{sni: "unknown.example.com", want: ""},
		{sni: "", want: ""},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.sni, func(t *testing.T) {
			t.Parallel()
			u := router.lookup(tt.sni)
			if tt.want == "" {
				if u != nil {
					t.Fatalf("lookup(%q) = %v, want nil", tt.sni, u)
				}
				return
			}
			if u == nil {
				t.Fatalf("lookup(%q) = nil, want host %q", tt.sni, tt.want)
			}
			if u.upstream.Host != tt.want {
				t.Fatalf("lookup(%q).upstream.Host = %q, want %q", tt.sni, u.upstream.Host, tt.want)
			}
		})
	}
}

func TestSniFromConn(t *testing.T) {
	t.Parallel()

	conn := &net.TCPConn{}
	if got := sniFromConn(conn); got != "" {
		t.Fatalf("sniFromConn(non-TLS) = %q, want empty", got)
	}
}

func TestSingleConnListener(t *testing.T) {
	t.Parallel()

	serverConn, _ := net.Pipe()
	ln := newSingleConnListener(serverConn)

	got, err := ln.Accept()
	if err != nil {
		t.Fatalf("first Accept: %v", err)
	}
	if got != serverConn {
		t.Fatal("first Accept returned wrong conn")
	}

	errCh := make(chan error, 1)
	go func() {
		_, err := ln.Accept()
		errCh <- err
	}()

	_ = ln.Close()
	select {
	case err := <-errCh:
		if err != net.ErrClosed {
			t.Fatalf("second Accept err = %v, want net.ErrClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second Accept did not unblock after Close")
	}

	if ln.Addr() == nil {
		t.Fatal("Addr() should not be nil")
	}
	_ = serverConn.Close()
}

func TestReverseProxyConnHTTP(t *testing.T) {
	var upstreamHost string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != upstreamHost {
			t.Errorf("upstream Host = %q, want %q", r.Host, upstreamHost)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("upstream-response"))
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	upstreamHost = upstreamURL.Host
	serverConn, clientConn := net.Pipe()

	errCh := make(chan error, 1)
	go func() {
		errCh <- reverseProxyConn(serverConn, &siteEntry{upstream: upstreamURL}, &atomic.Bool{})
	}()

	// net.Pipe is synchronous; write and read must run concurrently.
	go func() {
		_, _ = clientConn.Write([]byte("GET /test HTTP/1.1\r\nHost: a.example.com\r\n\r\n"))
	}()

	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	n, _ := clientConn.Read(buf)
	_ = clientConn.Close()

	resp := string(buf[:n])
	if !strings.Contains(resp, "upstream-response") {
		t.Fatalf("response does not contain upstream body: %q", resp)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("reverseProxyConn error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reverseProxyConn timed out")
	}
}

func TestProxyErrorHandler(t *testing.T) {
	var logged strings.Builder
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	upstream, _ := url.Parse("http://127.0.0.1:9081")
	req := httptest.NewRequest(http.MethodGet, "https://gateway.example.com/browser/wss", nil)
	req.RemoteAddr = "203.0.113.5:54321"
	rec := httptest.NewRecorder()

	proxyErrorHandler(upstream)(rec, req, errors.New("dial tcp 127.0.0.1:9081: connect: connection refused"))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
	if !strings.Contains(rec.Body.String(), "bad gateway") {
		t.Fatalf("body = %q, want a bad gateway marker", rec.Body.String())
	}

	out := logged.String()
	for _, want := range []string{
		"proxy upstream error",
		"dial tcp 127.0.0.1:9081",
		"gateway.example.com",
		"/browser/wss",
		"203.0.113.5",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("log %q does not contain %q", out, want)
		}
	}
}

func TestReverseProxyConnUpstreamUnreachable(t *testing.T) {
	// Bind and release a port so the upstream dial is refused.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	upstream, _ := url.Parse("http://" + ln.Addr().String())
	_ = ln.Close()

	serverConn, clientConn := net.Pipe()

	errCh := make(chan error, 1)
	go func() {
		errCh <- reverseProxyConn(serverConn, &siteEntry{upstream: upstream}, &atomic.Bool{})
	}()

	// net.Pipe is synchronous; write and read must run concurrently.
	go func() {
		_, _ = clientConn.Write([]byte("GET /test HTTP/1.1\r\nHost: a.example.com\r\n\r\n"))
	}()

	_ = clientConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 4096)
	n, _ := clientConn.Read(buf)
	_ = clientConn.Close()

	resp := string(buf[:n])
	if !strings.Contains(resp, "502 Bad Gateway") || !strings.Contains(resp, "bad gateway") {
		t.Fatalf("response is not a proxy error: %q", resp)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("reverseProxyConn error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reverseProxyConn timed out")
	}
}

type connWithRemoteAddr struct {
	net.Conn
	addr net.Addr
}

func (c connWithRemoteAddr) RemoteAddr() net.Addr { return c.addr }

func TestReverseProxyConnForwardingHeaders(t *testing.T) {
	const clientIP = "203.0.113.10"
	gotCh := make(chan *http.Request, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCh <- r.Clone(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	rawServer, clientConn := net.Pipe()
	serverConn := connWithRemoteAddr{
		Conn: rawServer,
		addr: &net.TCPAddr{IP: net.ParseIP(clientIP), Port: 54321},
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- reverseProxyConn(serverConn, &siteEntry{upstream: upstreamURL}, &atomic.Bool{})
	}()
	go func() {
		_, _ = clientConn.Write([]byte(
			"GET /test HTTP/1.1\r\n" +
				"Host: a.example.com\r\n" +
				"X-Forwarded-For: 1.2.3.4\r\n" +
				"X-Forwarded-Proto: http\r\n" +
				"X-Forwarded-Host: spoofed.example\r\n" +
				"X-Forwarded-Port: 80\r\n" +
				"X-Real-IP: 1.2.3.4\r\n\r\n"))
	}()

	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	_, _ = clientConn.Read(buf)
	_ = clientConn.Close()

	select {
	case got := <-gotCh:
		if got.Host != upstreamURL.Host {
			t.Errorf("Host = %q, want upstream %q", got.Host, upstreamURL.Host)
		}
		if got.Header.Get("X-Forwarded-For") != clientIP {
			t.Errorf("X-Forwarded-For = %q, want %q", got.Header.Get("X-Forwarded-For"), clientIP)
		}
		if got.Header.Get("X-Real-IP") != clientIP {
			t.Errorf("X-Real-IP = %q, want %q", got.Header.Get("X-Real-IP"), clientIP)
		}
		if got.Header.Get("X-Forwarded-Proto") != "https" {
			t.Errorf("X-Forwarded-Proto = %q, want https", got.Header.Get("X-Forwarded-Proto"))
		}
		if got.Header.Get("X-Forwarded-Host") != "a.example.com" {
			t.Errorf("X-Forwarded-Host = %q, want a.example.com", got.Header.Get("X-Forwarded-Host"))
		}
		if got.Header.Get("X-Forwarded-Port") != "" {
			t.Errorf("X-Forwarded-Port = %q, want empty", got.Header.Get("X-Forwarded-Port"))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not receive request")
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("reverseProxyConn error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reverseProxyConn timed out")
	}
}

func TestReverseProxyConnPreserveHost(t *testing.T) {
	gotCh := make(chan string, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCh <- r.Host
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	serverConn, clientConn := net.Pipe()
	errCh := make(chan error, 1)
	go func() {
		errCh <- reverseProxyConn(serverConn, &siteEntry{upstream: upstreamURL, preserveHost: true}, &atomic.Bool{})
	}()
	go func() {
		_, _ = clientConn.Write([]byte("GET / HTTP/1.1\r\nHost: miss.example.com\r\n\r\n"))
	}()

	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	_, _ = clientConn.Read(buf)
	_ = clientConn.Close()

	select {
	case got := <-gotCh:
		if got != "miss.example.com" {
			t.Fatalf("Host = %q, want miss.example.com", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not receive request")
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("reverseProxyConn error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reverseProxyConn timed out")
	}
}

func TestHandleConnRoutesFallbackBySNI(t *testing.T) {
	var upstreamHost string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != upstreamHost {
			t.Errorf("upstream Host = %q, want %q", r.Host, upstreamHost)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("sni-routed"))
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	upstreamHost = upstreamURL.Host
	router := newSiteRouter([]config.SiteRoute{
		{Domains: []string{"route.example.com"}, Upstream: upstream.URL},
	})

	certServer := httptest.NewTLSServer(http.NotFoundHandler())
	cert := certServer.TLS.Certificates[0]
	certServer.Close()

	serverRaw, clientRaw := net.Pipe()
	serverTLS := tls.Server(serverRaw, &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"http/1.1"},
	})
	clientTLS := tls.Client(clientRaw, &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         "route.example.com",
		NextProtos:         []string{"http/1.1"},
	})
	defer clientTLS.Close()

	go handleConn(serverTLS, "127.0.0.1:1", router, []proxyProtocolHandler{newSowerProtocolHandler(transportSower.New("secret"))})

	if err := clientTLS.Handshake(); err != nil {
		t.Fatalf("tls handshake: %v", err)
	}
	if state := clientTLS.ConnectionState(); state.NegotiatedProtocol != "http/1.1" {
		t.Fatalf("negotiated protocol = %q, want http/1.1", state.NegotiatedProtocol)
	}

	padding := strings.Repeat("x", 512)
	_, _ = clientTLS.Write([]byte("GET /route HTTP/1.1\r\nHost: route.example.com\r\nX-Pad: " + padding + "\r\n\r\n"))

	_ = clientTLS.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	n, err := clientTLS.Read(buf)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	resp := string(buf[:n])
	if !strings.Contains(resp, "sni-routed") {
		t.Fatalf("response does not contain routed upstream body: %q", resp)
	}
}

func TestSiteRouterResolvePathRoutes(t *testing.T) {
	router := newSiteRouter([]config.SiteRoute{
		{
			Domains:  []string{"site.example.com"},
			Upstream: "http://127.0.0.1:8080",
			Routes: map[string]string{
				"/ws":   "http://127.0.0.1:8082",
				"/wss/": "http://127.0.0.1:8083",
				"/":     "http://127.0.0.1:8084",
			},
		},
		{Domains: []string{"plain.example.com"}, Upstream: "http://127.0.0.1:8080"},
	})

	entry := router.lookup("SITE.example.com")
	if entry == nil {
		t.Fatal("lookup returned nil for routed domain")
	}

	tests := []struct {
		path     string
		wantHost string
	}{
		{"/ws", "127.0.0.1:8082"},
		{"/ws/", "127.0.0.1:8082"},
		{"/ws/extra", "127.0.0.1:8082"},
		{"/wss/other", "127.0.0.1:8083"},
		{"/wsish", "127.0.0.1:8084"}, // longest prefix match, not substring
		{"/other", "127.0.0.1:8084"},
		{"/", "127.0.0.1:8084"},
	}
	for _, tt := range tests {
		if got := entry.resolve(tt.path); got.Host != tt.wantHost {
			t.Errorf("resolve(%q) = %q, want %q", tt.path, got.Host, tt.wantHost)
		}
	}

	plain := router.lookup("plain.example.com")
	if plain == nil {
		t.Fatal("lookup returned nil for plain domain")
	}
	if got := plain.resolve("/anything"); got.Host != "127.0.0.1:8080" {
		t.Errorf("plain resolve = %q, want default upstream 127.0.0.1:8080", got.Host)
	}

	if entry := router.lookup("unknown.example.com"); entry != nil {
		t.Error("lookup returned entry for unrouted domain")
	}
}

func TestReverseProxyConnPathRouting(t *testing.T) {
	defaultUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("default-upstream"))
	}))
	defer defaultUpstream.Close()

	wsUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ws-upstream"))
	}))
	defer wsUpstream.Close()

	defaultURL, _ := url.Parse(defaultUpstream.URL)
	wsURL, _ := url.Parse(wsUpstream.URL)
	entry := &siteEntry{
		upstream: defaultURL,
		paths:    []pathRoute{{path: "/ws", upstream: wsURL}},
	}

	for _, tt := range []struct {
		path string
		want string
	}{
		{"/ws", "ws-upstream"},
		{"/ws/extra", "ws-upstream"},
		{"/other", "default-upstream"},
	} {
		serverConn, clientConn := net.Pipe()
		errCh := make(chan error, 1)
		go func() {
			errCh <- reverseProxyConn(serverConn, entry, &atomic.Bool{})
		}()
		go func() {
			_, _ = clientConn.Write([]byte("GET " + tt.path + " HTTP/1.1\r\nHost: a.example.com\r\n\r\n"))
		}()

		_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 4096)
		n, _ := clientConn.Read(buf)
		_ = clientConn.Close()

		if resp := string(buf[:n]); !strings.Contains(resp, tt.want) {
			t.Errorf("GET %s response %q does not contain %q", tt.path, resp, tt.want)
		}

		select {
		case err := <-errCh:
			if err != nil && !errors.Is(err, net.ErrClosed) {
				t.Fatalf("reverseProxyConn error: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("reverseProxyConn timed out")
		}
	}
}

func TestReverseProxyConnPathRoutedWebSocket(t *testing.T) {
	upstreamHit := make(chan string, 1)
	wsUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit <- r.URL.Path
		key := r.Header.Get("Sec-WebSocket-Key")
		if key == "" {
			t.Errorf("missing Sec-WebSocket-Key")
			return
		}
		w.Header().Set("Upgrade", "websocket")
		w.Header().Set("Connection", "Upgrade")
		w.Header().Set("Sec-WebSocket-Accept", websocketAcceptKey(key))
		w.WriteHeader(http.StatusSwitchingProtocols)
	}))
	defer wsUpstream.Close()

	defaultURL, _ := url.Parse("http://127.0.0.1:1") // must never be dialed
	wsURL, _ := url.Parse(wsUpstream.URL)
	entry := &siteEntry{
		upstream: defaultURL,
		paths:    []pathRoute{{path: "/ws", upstream: wsURL}},
	}

	serverConn, clientConn := net.Pipe()
	errCh := make(chan error, 1)
	go func() {
		errCh <- reverseProxyConn(serverConn, entry, &atomic.Bool{})
	}()

	go func() {
		req := "GET /ws HTTP/1.1\r\n" +
			"Host: a.example.com\r\n" +
			"Connection: Upgrade\r\n" +
			"Upgrade: websocket\r\n" +
			"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
			"Sec-WebSocket-Version: 13\r\n\r\n"
		_, _ = clientConn.Write([]byte(req))
	}()

	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	n, _ := clientConn.Read(buf)
	_ = clientConn.Close()

	resp := string(buf[:n])
	if !strings.Contains(resp, "101 Switching Protocols") {
		t.Fatalf("upgrade response = %q, want 101 Switching Protocols", resp)
	}

	select {
	case path := <-upstreamHit:
		if path != "/ws" {
			t.Fatalf("upstream path = %q, want /ws", path)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not receive upgrade request")
	}

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			t.Fatalf("reverseProxyConn error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reverseProxyConn timed out")
	}
}

func TestReverseProxyConnWebSocket(t *testing.T) {
	upstreamHit := make(chan *http.Request, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit <- r

		if r.Header.Get("Upgrade") != "websocket" {
			t.Errorf("Upgrade header = %q, want websocket", r.Header.Get("Upgrade"))
		}
		if !strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") {
			t.Errorf("Connection header = %q, want contains upgrade", r.Header.Get("Connection"))
		}

		key := r.Header.Get("Sec-WebSocket-Key")
		if key == "" {
			// The handler runs on the httptest server goroutine, not the test
			// goroutine: t.Fatal would Goexit only this goroutine and can hang
			// the test server, so report the failure and abort the handler.
			t.Errorf("missing Sec-WebSocket-Key")
			return
		}

		w.Header().Set("Upgrade", "websocket")
		w.Header().Set("Connection", "Upgrade")
		w.Header().Set("Sec-WebSocket-Accept", websocketAcceptKey(key))
		w.WriteHeader(http.StatusSwitchingProtocols)
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	serverConn, clientConn := net.Pipe()

	errCh := make(chan error, 1)
	go func() {
		errCh <- reverseProxyConn(serverConn, &siteEntry{upstream: upstreamURL}, &atomic.Bool{})
	}()

	go func() {
		req := "GET /ws HTTP/1.1\r\n" +
			"Host: a.example.com\r\n" +
			"Connection: Upgrade\r\n" +
			"Upgrade: websocket\r\n" +
			"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
			"Sec-WebSocket-Version: 13\r\n\r\n"
		_, _ = clientConn.Write([]byte(req))
	}()

	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	n, _ := clientConn.Read(buf)
	_ = clientConn.Close()

	resp := string(buf[:n])
	if !strings.Contains(resp, "101 Switching Protocols") {
		t.Fatalf("upgrade response = %q, want 101 Switching Protocols", resp)
	}
	wantAccept := websocketAcceptKey("dGhlIHNhbXBsZSBub25jZQ==")
	if !strings.Contains(resp, "Sec-Websocket-Accept: "+wantAccept) {
		t.Fatalf("response missing accept key %q: %q", wantAccept, resp)
	}

	select {
	case <-upstreamHit:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not receive upgrade request")
	}

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			t.Fatalf("reverseProxyConn error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reverseProxyConn timed out")
	}
}

func websocketAcceptKey(key string) string {
	const magic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	h := sha1.New()
	_, _ = h.Write([]byte(key + magic))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func TestHandleConnFallsBackToFakeSiteWhenSNIMisses(t *testing.T) {
	gotCh := make(chan *http.Request, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCh <- r.Clone(r.Context())
		_, _ = w.Write([]byte("fake-site-fallback"))
	}))
	defer upstream.Close()
	upstreamURL, _ := url.Parse(upstream.URL)

	certServer := httptest.NewTLSServer(http.NotFoundHandler())
	cert := certServer.TLS.Certificates[0]
	certServer.Close()

	serverRaw, clientRaw := net.Pipe()
	serverTLS := tls.Server(serverRaw, &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"http/1.1"},
	})
	clientTLS := tls.Client(clientRaw, &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         "miss.example.com",
		NextProtos:         []string{"http/1.1"},
	})
	defer clientTLS.Close()

	router := newSiteRouter([]config.SiteRoute{
		{Domains: []string{"route.example.com"}, Upstream: "http://127.0.0.1:1"},
	})
	go handleConn(serverTLS, upstreamURL.Host, router, []proxyProtocolHandler{newSowerProtocolHandler(transportSower.New("secret"))})

	if err := clientTLS.Handshake(); err != nil {
		t.Fatalf("tls handshake: %v", err)
	}

	padding := strings.Repeat("x", 512)
	_, _ = clientTLS.Write([]byte(
		"GET /fallback HTTP/1.1\r\n" +
			"Host: miss.example.com\r\n" +
			"X-Forwarded-For: 1.2.3.4\r\n" +
			"X-Forwarded-Proto: http\r\n" +
			"X-Forwarded-Host: spoofed.example\r\n" +
			"X-Real-IP: 1.2.3.4\r\n" +
			"X-Pad: " + padding + "\r\n\r\n"))

	_ = clientTLS.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	n, err := clientTLS.Read(buf)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	resp := string(buf[:n])
	if !strings.Contains(resp, "fake-site-fallback") {
		t.Fatalf("response does not contain fake site body: %q", resp)
	}

	select {
	case got := <-gotCh:
		if got.Host != "miss.example.com" {
			t.Errorf("Host = %q, want miss.example.com", got.Host)
		}
		if got.Header.Get("X-Forwarded-Proto") != "https" {
			t.Errorf("X-Forwarded-Proto = %q, want https", got.Header.Get("X-Forwarded-Proto"))
		}
		if got.Header.Get("X-Forwarded-Host") != "miss.example.com" {
			t.Errorf("X-Forwarded-Host = %q, want miss.example.com", got.Header.Get("X-Forwarded-Host"))
		}
		if got.Header.Get("X-Forwarded-For") == "1.2.3.4" {
			t.Errorf("X-Forwarded-For still spoofed: %q", got.Header.Get("X-Forwarded-For"))
		}
		if got.Header.Get("X-Real-IP") == "1.2.3.4" {
			t.Errorf("X-Real-IP still spoofed: %q", got.Header.Get("X-Real-IP"))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not receive request")
	}
}

func TestHandleConnFakeSiteHTTPSplitRequestLineSetsProxyHeaders(t *testing.T) {
	gotCh := make(chan *http.Request, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCh <- r.Clone(r.Context())
		_, _ = w.Write([]byte("split-fallback"))
	}))
	defer upstream.Close()
	upstreamURL, _ := url.Parse(upstream.URL)

	certServer := httptest.NewTLSServer(http.NotFoundHandler())
	cert := certServer.TLS.Certificates[0]
	certServer.Close()

	serverRaw, clientRaw := net.Pipe()
	serverTLS := tls.Server(serverRaw, &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"http/1.1"},
	})
	clientTLS := tls.Client(clientRaw, &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         "miss.example.com",
		NextProtos:         []string{"http/1.1"},
	})
	defer clientTLS.Close()

	go handleConn(serverTLS, upstreamURL.Host, siteRouter{}, []proxyProtocolHandler{newSowerProtocolHandler(transportSower.New("secret"))})

	if err := clientTLS.Handshake(); err != nil {
		t.Fatalf("tls handshake: %v", err)
	}

	_, _ = clientTLS.Write([]byte("GET /fall"))
	_, _ = clientTLS.Write([]byte("back HTTP/1.1\r\nHost: miss.example.com\r\n\r\n"))

	_ = clientTLS.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	n, err := clientTLS.Read(buf)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if !strings.Contains(string(buf[:n]), "split-fallback") {
		t.Fatalf("response does not contain fake site body: %q", buf[:n])
	}

	select {
	case got := <-gotCh:
		if got.Header.Get("X-Forwarded-Proto") != "https" {
			t.Errorf("X-Forwarded-Proto = %q, want https", got.Header.Get("X-Forwarded-Proto"))
		}
		if got.Host != "miss.example.com" {
			t.Errorf("Host = %q, want miss.example.com", got.Host)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not receive request")
	}
}

func TestHandleConnRelaysNonHTTPFakeSiteWhenProbeMisses(t *testing.T) {
	fakeSite := startRawTCPServer(t, "raw-fallback")

	certServer := httptest.NewTLSServer(http.NotFoundHandler())
	cert := certServer.TLS.Certificates[0]
	certServer.Close()

	serverRaw, clientRaw := net.Pipe()
	serverTLS := tls.Server(serverRaw, &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"http/1.1"},
	})
	clientTLS := tls.Client(clientRaw, &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         "miss.example.com",
		NextProtos:         []string{"http/1.1"},
	})
	defer clientTLS.Close()

	go handleConn(serverTLS, fakeSite, siteRouter{}, []proxyProtocolHandler{newSowerProtocolHandler(transportSower.New("secret"))})

	if err := clientTLS.Handshake(); err != nil {
		t.Fatalf("tls handshake: %v", err)
	}
	_ = clientTLS.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := clientTLS.Write([]byte("not-http-payload")); err != nil {
		t.Fatalf("write non-http payload: %v", err)
	}

	buf := make([]byte, 4096)
	n, err := clientTLS.Read(buf)
	if err != nil {
		t.Fatalf("read fallback response: %v", err)
	}
	if !strings.Contains(string(buf[:n]), "raw-fallback") {
		t.Fatalf("response does not contain raw fallback body: %q", buf[:n])
	}
}

func TestHandleConnFallsBackAfterSowerAuthFailure(t *testing.T) {
	fakeSite := startRawTCPServer(t, "auth-fallback")

	certServer := httptest.NewTLSServer(http.NotFoundHandler())
	cert := certServer.TLS.Certificates[0]
	certServer.Close()

	serverRaw, clientRaw := net.Pipe()
	serverTLS := tls.Server(serverRaw, &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"http/1.1"},
	})
	clientTLS := tls.Client(clientRaw, &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         "miss.example.com",
		NextProtos:         []string{"http/1.1"},
	})
	defer clientTLS.Close()

	go handleConn(serverTLS, fakeSite, siteRouter{}, []proxyProtocolHandler{newSowerProtocolHandler(transportSower.New("secret"))})

	if err := clientTLS.Handshake(); err != nil {
		t.Fatalf("tls handshake: %v", err)
	}
	_ = clientTLS.SetDeadline(time.Now().Add(2 * time.Second))
	if err := transportSower.New("wrong-password").Wrap(clientTLS, "example.com", 443); err != nil {
		t.Fatalf("write invalid sower request: %v", err)
	}

	buf := make([]byte, 4096)
	n, err := clientTLS.Read(buf)
	if err != nil {
		t.Fatalf("read fallback response: %v", err)
	}
	resp := string(buf[:n])
	if !strings.Contains(resp, "auth-fallback") {
		t.Fatalf("response does not contain fallback body: %q", resp)
	}
}

func TestHTTPRequestLineProbe(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"GET / HTTP/1.1\r\nHost: x\r\n\r\n", true},
		{"POST /submit HTTP/1.0\r\n\r\n", true},
		{"CONNECT example.com:443 HTTP/1.1\r\n", true},
		{"GET / HTTP/1.1\nHost: x\n\n", true},
		{"GET / HTTP/1.1", true},
		{"GET / HTTP/1.1\r", true},
		{"GET / HTTP/1.10\r\n", false},
		{"GET / HTTP/1.1 extra\r\n", false},
		{"GET /fall", false},
		{"hello", false},
		{"\x80xxxx", false},
		{"GETX / HTTP/1.1\r\n", false},
	}
	for _, tt := range tests {
		if got := httpRequestLineProbe([]byte(tt.in)); got != tt.want {
			t.Errorf("httpRequestLineProbe(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestCouldBeHTTPRequestLine(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"G", true},
		{"GET", true},
		{"GET /fall", true},
		{"GET / HTTP/1.", true},
		{"GET / HTTP/1.1\r", true},
		{"GET / HTTP/1.1\r\n", false},
		{"hello", false},
		{"\x80", false},
		{"GET / HTTP/1.10", false},
	}
	for _, tt := range tests {
		if got := couldBeHTTPRequestLine([]byte(tt.in)); got != tt.want {
			t.Errorf("couldBeHTTPRequestLine(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestNewFakeSiteEntry(t *testing.T) {
	e := newFakeSiteEntry("127.0.0.1:9080")
	if e.upstream.String() != "http://127.0.0.1:9080" {
		t.Fatalf("upstream = %q, want http://127.0.0.1:9080", e.upstream.String())
	}
	if !e.preserveHost {
		t.Fatal("preserveHost = false, want true")
	}
	v6 := newFakeSiteEntry("[::1]:8080")
	if v6.upstream.Host != "[::1]:8080" {
		t.Fatalf("ipv6 host = %q, want [::1]:8080", v6.upstream.Host)
	}
}

func TestExtendHTTPRequestProbeReadsSplitRequestLine(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go func() {
		_, _ = client.Write([]byte("GET /fall"))
		_, _ = client.Write([]byte("back HTTP/1.1\r\nHost: x\r\n\r\n"))
	}()

	_ = server.SetReadDeadline(time.Now().Add(2 * time.Second))
	first := make([]byte, protocolProbeMaxBytes)
	n, err := server.Read(first)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	got := extendHTTPRequestProbe(server, first[:n])
	if !httpRequestLineProbe(got) {
		t.Fatalf("extended probe not HTTP request line: %q", got)
	}
}

func TestExtendHTTPRequestProbeSkipsNonHTTP(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	got := extendHTTPRequestProbe(server, []byte("not-http"))
	if string(got) != "not-http" {
		t.Fatalf("probe = %q, want not-http", got)
	}
}

func startRawTCPServer(t *testing.T, body string) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen raw server: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, 1024)
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, _ = conn.Read(buf)
		_, _ = conn.Write([]byte(body))
	}()

	return ln.Addr().String()
}

// testCertBlob builds an autocert-format cache entry (private key PEM first,
// certificate PEM second) for a self-signed certificate of the given name.
// The key type is caller-chosen: autocert keys its cache by client
// capabilities, so a zero-value ClientHelloInfo (non-ECDSA) resolves to the
// "domain+rsa" key while an ECDSA-capable hello resolves to the bare domain.
func testCertBlob(t *testing.T, name string, key crypto.Signer) ([]byte, tls.Certificate) {
	t.Helper()

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		DNSNames:              []string{name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}

	var keyBlock *pem.Block
	switch k := key.(type) {
	case *rsa.PrivateKey:
		keyBlock = &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}
	case *ecdsa.PrivateKey:
		keyDER, err := x509.MarshalECPrivateKey(k)
		if err != nil {
			t.Fatalf("marshal ECDSA key: %v", err)
		}
		keyBlock = &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}
	default:
		t.Fatalf("unsupported key type %T", key)
	}
	blob := append(pem.EncodeToMemory(keyBlock),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	return blob, tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

// stubAutocertCache serves ECDSA and RSA certificate blobs under autocert's
// capability-derived keys and reports a cache miss otherwise, so autocert
// falls through to issuance.
type stubAutocertCache struct {
	ecdsaBlob []byte // bare-domain key
	rsaBlob   []byte // "domain+rsa" key
}

func (c stubAutocertCache) Get(_ context.Context, name string) ([]byte, error) {
	switch name {
	case "primary.example.com":
		if c.ecdsaBlob != nil {
			return c.ecdsaBlob, nil
		}
	case "primary.example.com+rsa":
		if c.rsaBlob != nil {
			return c.rsaBlob, nil
		}
	}
	return nil, autocert.ErrCacheMiss
}

func (stubAutocertCache) Put(context.Context, string, []byte) error { return nil }

func (stubAutocertCache) Delete(context.Context, string) error { return nil }

func TestGetCertificateFallbackForUnknownSNI(t *testing.T) {
	t.Parallel()

	rsaBlob, rsaCert := testCertBlob(t, "primary.example.com", mustRSAKey(t))
	ecdsaBlob, ecdsaCert := testCertBlob(t, "primary.example.com", mustECDSAKey(t))
	manager := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Cache:      stubAutocertCache{ecdsaBlob: ecdsaBlob, rsaBlob: rsaBlob},
		HostPolicy: autocert.HostWhitelist("primary.example.com"),
		// Keep the test hermetic even if the cache lookup misses.
		Client: &acme.Client{DirectoryURL: "https://127.0.0.1:1/directory"},
	}

	var cfg config.SowerdConfig
	cfg.LogLevel = slog.LevelDebug
	cfg.ServeIP = "0.0.0.0"
	cfg.Password = "secret"
	cfg.FakeSite = "127.0.0.1:8080"
	cfg.Cert.Domains = []string{"primary.example.com"}

	getCert := getCertificateWithFallback(manager, cfg)

	// An ECDSA-capable hello must be answered with the ECDSA certificate: the
	// fallback carries the client's capabilities over to autocert, and a
	// regression to a bare ServerName hello would silently serve the RSA one.
	ecdsaHello := &tls.ClientHelloInfo{
		ServerName:       "prober.example.org",
		SignatureSchemes: []tls.SignatureScheme{tls.ECDSAWithP256AndSHA256},
		SupportedCurves:  []tls.CurveID{tls.CurveP256},
		CipherSuites:     []uint16{tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256},
	}

	tests := []struct {
		name  string
		hello *tls.ClientHelloInfo
		want  tls.Certificate
	}{
		// Active probers use arbitrary or empty SNI; the handshake must be
		// answered with the primary domain's certificate, like a real site's
		// default vhost, instead of failing at the TLS layer. Zero-value
		// hellos resolve to autocert's "domain+rsa" cache key.
		{name: "unknown SNI gets fallback cert", hello: &tls.ClientHelloInfo{ServerName: "prober.example.org"}, want: rsaCert},
		{name: "empty SNI gets fallback cert", hello: &tls.ClientHelloInfo{}, want: rsaCert},
		{name: "whitelisted SNI gets its cert", hello: &tls.ClientHelloInfo{ServerName: "primary.example.com"}, want: rsaCert},
		{name: "unknown SNI with ECDSA-capable hello gets ECDSA cert", hello: ecdsaHello, want: ecdsaCert},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := getCert(tt.hello)
			if err != nil {
				t.Fatalf("GetCertificate(%q): %v", tt.hello.ServerName, err)
			}
			if len(got.Certificate) != 1 || !bytes.Equal(got.Certificate[0], tt.want.Certificate[0]) {
				t.Fatalf("GetCertificate(%q) returned an unexpected certificate", tt.hello.ServerName)
			}
		})
	}
}

func mustRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return key
}

func mustECDSAKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ECDSA key: %v", err)
	}
	return key
}

func TestGetCertificateFallbackNeverMasksIssuanceErrors(t *testing.T) {
	t.Parallel()

	// Empty cache + a dead ACME directory: any whitelisted name falls through
	// to issuance and fails. The error must surface as-is, not be masked by a
	// wrong-name fallback certificate.
	manager := &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Cache:      stubAutocertCache{},
		HostPolicy: autocert.HostWhitelist("primary.example.com"),
		Client:     &acme.Client{DirectoryURL: "https://127.0.0.1:1/directory"},
	}

	var cfg config.SowerdConfig
	cfg.LogLevel = slog.LevelDebug
	cfg.ServeIP = "0.0.0.0"
	cfg.Password = "secret"
	cfg.FakeSite = "127.0.0.1:8080"
	cfg.Cert.Domains = []string{"primary.example.com"}

	getCert := getCertificateWithFallback(manager, cfg)

	got, err := getCert(&tls.ClientHelloInfo{ServerName: "primary.example.com"})
	if err == nil {
		t.Fatal("issuance error was masked by a fallback certificate")
	}
	if got != nil {
		t.Fatalf("expected no certificate on issuance failure, got %v", got)
	}
}
