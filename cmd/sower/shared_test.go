package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sower-proxy/deferlog/v2"
	"github.com/sower-proxy/sower/config"
	"github.com/sower-proxy/sower/internal/admin"
)

func TestSharedAdminHTTPAddr(t *testing.T) {
	t.Parallel()

	base := config.SowerConfig{}
	base.DNS.Serve = "127.0.0.1"
	base.Admin.Addr = "127.0.0.1:80"
	base.Admin.Password = deferlog.NewPassword("secret")

	cfg := base
	if addr, ok := sharedAdminHTTPAddr(cfg); !ok || addr != "127.0.0.1:80" {
		t.Fatalf("expected shared mode, got %q %v", addr, ok)
	}

	cfg = base
	cfg.Admin.Disable = true
	if _, ok := sharedAdminHTTPAddr(cfg); ok {
		t.Fatal("expected no shared mode when admin disabled")
	}

	cfg = base
	cfg.DNS.Disable = true
	if _, ok := sharedAdminHTTPAddr(cfg); ok {
		t.Fatal("expected no shared mode when dns disabled")
	}

	cfg = base
	cfg.Admin.Addr = "127.0.0.1:19090"
	if _, ok := sharedAdminHTTPAddr(cfg); ok {
		t.Fatal("expected no shared mode for a different port")
	}

	cfg = base
	cfg.Admin.Addr = "127.0.0.2:80"
	if _, ok := sharedAdminHTTPAddr(cfg); ok {
		t.Fatal("expected no shared mode for a different host")
	}
}

func TestIsAdminRequest(t *testing.T) {
	t.Parallel()

	adminHost := "127.0.0.1"
	cases := []struct {
		name string
		head string
		want bool
	}{
		{"origin form local host", "GET / HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n", true},
		{"origin form local host with port", "GET / HTTP/1.1\r\nHost: 127.0.0.1:80\r\n\r\n", true},
		{"post api local host", "POST /api/session HTTP/1.1\r\nHost: 127.0.0.1\r\nContent-Length: 0\r\n\r\n", true},
		{"origin form other host", "GET / HTTP/1.1\r\nHost: example.com\r\n\r\n", false},
		{"absolute form", "GET http://example.com/ HTTP/1.1\r\nHost: example.com\r\n\r\n", false},
		{"connect", "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n", false},
		{"no host header", "GET / HTTP/1.1\r\n\r\n", false},
		{"malformed request line", "GET\r\nHost: 127.0.0.1\r\n\r\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAdminRequest([]byte(tc.head), adminHost); got != tc.want {
				t.Fatalf("isAdminRequest = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReadRequestHead(t *testing.T) {
	t.Parallel()

	br := bufio.NewReaderSize(bytes.NewReader([]byte("GET / HTTP/1.1\r\nHost: x\r\n\r\nbody")), 1024)
	head, err := readRequestHead(br)
	if err != nil {
		t.Fatalf("read head: %v", err)
	}
	if string(head) != "GET / HTTP/1.1\r\nHost: x\r\n\r\n" {
		t.Fatalf("unexpected head: %q", head)
	}

	br = bufio.NewReaderSize(bytes.NewReader([]byte(strings.Repeat("a", sharedHeaderMaxBytes+1))), sharedHeaderMaxBytes)
	if _, err := readRequestHead(br); !errors.Is(err, errHeaderTooLarge) {
		t.Fatalf("expected header too large, got %v", err)
	}

	br = bufio.NewReaderSize(bytes.NewReader([]byte("GET / HTTP/1.1\r\n")), 1024)
	if _, err := readRequestHead(br); err == nil {
		t.Fatal("expected error for truncated head")
	}
}

func TestPrefixConnReplays(t *testing.T) {
	t.Parallel()

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	prefix := []byte("GET / HTTP/1.1\r\n\r\n")
	pc := &prefixConn{Conn: server, prefix: bytes.NewReader(prefix)}

	buf := make([]byte, len(prefix))
	if _, err := io.ReadFull(pc, buf); err != nil {
		t.Fatalf("read prefix: %v", err)
	}
	if !bytes.Equal(buf, prefix) {
		t.Fatalf("unexpected prefix: %q", buf)
	}

	go func() {
		_, _ = client.Write([]byte("body"))
	}()
	rest := make([]byte, 4)
	if _, err := io.ReadFull(pc, rest); err != nil {
		t.Fatalf("read rest: %v", err)
	}
	if string(rest) != "body" {
		t.Fatalf("unexpected rest: %q", rest)
	}
}

func TestChanListenerDispatchAndClose(t *testing.T) {
	t.Parallel()

	addr := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 80}
	l := newChanListener(addr)
	if l.Addr() != addr {
		t.Fatalf("unexpected addr: %v", l.Addr())
	}

	server, client := net.Pipe()
	defer client.Close()
	acceptedCh := make(chan net.Conn, 1)
	go func() {
		c, _ := l.Accept()
		acceptedCh <- c
	}()
	l.dispatch(server)
	accepted := <-acceptedCh
	if accepted != server {
		t.Fatal("unexpected accepted conn")
	}

	done := make(chan struct{})
	go func() {
		_, _ = l.Accept()
		close(done)
	}()
	_ = l.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("accept did not unblock after close")
	}

	server2, client2 := net.Pipe()
	defer client2.Close()
	l.dispatch(server2)
	_ = server2.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := server2.Read(make([]byte, 1)); err == nil {
		t.Fatal("expected conn closed after listener close")
	}
}

func TestServeSharedHTTPRoutesAdminAndProxy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	addr := ln.Addr().String()
	host, _, _ := net.SplitHostPort(addr)

	stats := newTestStats(t)
	r := newTestRouter()
	state := admin.LoadStateStore("")
	baseline := snapshotBaseline(r)
	state.SetBaseline(baseline)
	srv := admin.NewServer(admin.Options{
		Password: "secret",
		Version:  "v1.2.3",
		Date:     "2026-01-01",
		Rules:    newAdminRules(r, state, baseline, newRuleHitTracker(r.BlockRule, maxRuleHits), newRuleHitTracker(r.DirectRule, maxRuleHitsWide), newRuleHitTracker(r.ProxyRule, maxRuleHitsWide), newRuleMissTracker()),
		Stats:    stats,
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		if err := ServeSharedHTTP(ctx, ln, newTestRouter(), stats, srv, host, errCh); err != nil {
			t.Errorf("serve shared http: %v", err)
		}
	}()

	client := &http.Client{Timeout: 5 * time.Second}

	// admin: GET / with Host = listener IP
	resp, err := client.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("admin get: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for admin, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// admin: POST /api/session with Host = listener IP
	loginResp, err := client.Post("http://"+addr+"/api/session", "application/json", strings.NewReader(`{"password":"secret"}`))
	if err != nil {
		t.Fatalf("admin login: %v", err)
	}
	if loginResp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 for admin login, got %d", loginResp.StatusCode)
	}
	loginResp.Body.Close()

	// proxy: origin-form with another Host -> connection closed (blocked)
	conn0, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn0.Close()
	_ = conn0.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.WriteString(conn0, "GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"); err != nil {
		t.Fatalf("write origin form: %v", err)
	}
	if _, err := conn0.Read(make([]byte, 1)); err == nil {
		t.Fatal("expected proxy path to close the connection")
	}

	// proxy: CONNECT -> connection closed
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.WriteString(conn, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n"); err != nil {
		t.Fatalf("write connect: %v", err)
	}
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("expected proxy path to close the connection")
	}

	// proxy: absolute-form -> connection closed
	conn2, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn2.Close()
	_ = conn2.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.WriteString(conn2, "GET http://example.com/ HTTP/1.1\r\nHost: example.com\r\n\r\n"); err != nil {
		t.Fatalf("write absolute: %v", err)
	}
	if _, err := conn2.Read(make([]byte, 1)); err == nil {
		t.Fatal("expected proxy path to close the connection")
	}
}
