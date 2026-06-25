package main

import (
	"bufio"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sower-proxy/sower/config"
	transportSower "github.com/sower-proxy/sower/transport/sower"
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

func TestIsLoopbackRemoteAddr(t *testing.T) {
	t.Parallel()

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
			if got := isLoopbackRemoteAddr(tt.addr); got != tt.want {
				t.Fatalf("isLoopbackRemoteAddr(%q) = %v, want %v", tt.addr, got, tt.want)
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
			if u.Host != tt.want {
				t.Fatalf("lookup(%q).Host = %q, want %q", tt.sni, u.Host, tt.want)
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
		errCh <- reverseProxyConn(serverConn, upstreamURL)
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

func TestReverseProxyConnRejectsUpgrade(t *testing.T) {
	upstreamHit := make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHit <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	upstreamURL, _ := url.Parse(upstream.URL)
	serverConn, clientConn := net.Pipe()

	errCh := make(chan error, 1)
	go func() {
		errCh <- reverseProxyConn(serverConn, upstreamURL)
	}()

	go func() {
		_, _ = clientConn.Write([]byte("GET /ws HTTP/1.1\r\nHost: a.example.com\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n"))
	}()

	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 4096)
	n, _ := clientConn.Read(buf)
	_ = clientConn.Close()

	resp := string(buf[:n])
	if !strings.Contains(resp, "400 Bad Request") {
		t.Fatalf("upgrade response = %q, want 400", resp)
	}

	select {
	case <-upstreamHit:
		t.Fatal("upgrade request should not reach upstream")
	default:
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

func TestHandleConnFallsBackToFakeSiteWhenSNIMisses(t *testing.T) {
	fakeSite := startHTTP1TCPServer(t, "fake-site-fallback")

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
	go handleConn(serverTLS, fakeSite, router, []proxyProtocolHandler{newSowerProtocolHandler(transportSower.New("secret"))})

	if err := clientTLS.Handshake(); err != nil {
		t.Fatalf("tls handshake: %v", err)
	}

	padding := strings.Repeat("x", 512)
	_, _ = clientTLS.Write([]byte("GET /fallback HTTP/1.1\r\nHost: miss.example.com\r\nX-Pad: " + padding + "\r\n\r\n"))

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

func startHTTP1TCPServer(t *testing.T, body string) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen fake site: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		r := bufio.NewReader(conn)
		for {
			line, err := r.ReadString('\n')
			if err != nil || line == "\r\n" || line == "\n" {
				break
			}
		}
		_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: " + fmt.Sprint(len(body)) + "\r\n\r\n" + body))
	}()

	return ln.Addr().String()
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
