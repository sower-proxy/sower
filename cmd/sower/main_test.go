package main

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sower-proxy/sower/config"
	"github.com/sower-proxy/sower/pkg/suffixtree"
)

func TestFetchRuleFileEmptyPathReturnsClosedChannel(t *testing.T) {
	t.Parallel()

	lines, err := fetchRuleFile(context.Background(), nil, "")
	if err != nil {
		t.Fatalf("fetch empty rule file: %v", err)
	}
	if len(lines) != 0 {
		t.Fatalf("unexpected lines for empty rule file path: %v", lines)
	}
}

func TestNewRouterPreservesEmptyUpstreamForDiscovery(t *testing.T) {
	t.Parallel()

	cfg := config.SowerConfig{}
	cfg.DNS.Serve = "127.0.0.1"
	cfg.DNS.Upstream = ""
	cfg.DNS.Fallback = "223.5.5.5"

	r := newRouter(cfg, nil)
	dnsState := reflect.ValueOf(r).Elem().FieldByName("dns")
	upstreamDNS := dnsState.FieldByName("upstreamDNS").String()
	fallbackDNS := dnsState.FieldByName("fallbackDNS").String()

	if upstreamDNS != "" {
		t.Fatalf("expected empty router upstream DNS to enable discovery, got %q", upstreamDNS)
	}
	if fallbackDNS != "223.5.5.5" {
		t.Fatalf("unexpected fallback DNS: %q", fallbackDNS)
	}
}

func TestFetchRuleFileRemoteGzip(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		defer gz.Close()
		_, _ = gz.Write([]byte("alpha\n\nbeta\n"))
	}))
	defer srv.Close()

	dialFn := func(network, host string, port uint16) (net.Conn, error) {
		return net.Dial(network, net.JoinHostPort(host, strconv.Itoa(int(port))))
	}

	got, err := fetchRuleFile(context.Background(), dialFn, srv.URL)
	if err != nil {
		t.Fatalf("fetch remote gzip rule file: %v", err)
	}

	want := []string{"alpha", "beta"}
	if !slices.Equal(got, want) {
		t.Fatalf("unexpected rules: got %v want %v", got, want)
	}
}

func TestFetchRuleFileRemoteRequiresProxyDialer(t *testing.T) {
	t.Parallel()

	_, err := fetchRuleFile(context.Background(), nil, "https://example.com/rules.txt")
	if err == nil {
		t.Fatal("expected remote rule file without proxy dialer to fail")
	}
	if !strings.Contains(err.Error(), "requires upstream proxy dialer") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFetchRuleFileCanceledIncludesPreviousError(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dialErr := errors.New("proxy dial failed")
	dialFn := func(network, host string, port uint16) (net.Conn, error) {
		cancel()
		return nil, dialErr
	}

	_, err := fetchRuleFile(ctx, dialFn, "https://example.com/rules.txt")
	if err == nil {
		t.Fatal("expected canceled fetch to fail")
	}
	if !strings.Contains(err.Error(), dialErr.Error()) {
		t.Fatalf("expected previous error in cancellation message, got: %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled wrapping, got: %v", err)
	}
}

func TestFetchRuleFileUsesProxyDialerForRemoteRules(t *testing.T) {
	t.Parallel()

	proxyHost := ""
	dialFn := func(network, host string, port uint16) (net.Conn, error) {
		proxyHost = host
		return nil, fmt.Errorf("stop after proxy dial")
	}

	_, err := fetchRuleFile(context.Background(), dialFn, "https://example.com/rules.txt")
	if err == nil {
		t.Fatal("expected proxy dial failure")
	}
	if proxyHost != "example.com" {
		t.Fatalf("expected remote host to be dialed through proxy, got %q", proxyHost)
	}
}

func TestServeAndReportRunsServeFunction(t *testing.T) {
	t.Parallel()

	errCh := make(chan error, 1)
	serveErr := errors.New("serve failed")
	done := make(chan struct{})

	go func() {
		serveAndReport(errCh, "test service", func() error {
			close(done)
			return serveErr
		})
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("serve function was not called")
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, serveErr) {
			t.Fatalf("unexpected reported error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("serve error was not reported")
	}
}

func TestLoadRuleSkipsOnlyFileRules(t *testing.T) {
	t.Parallel()

	path := writeGzipRuleFile(t, "t.co\nexample.com\n")
	rule := suffixtree.NewNodeFromRules("manual.example")

	if err := loadRule(context.Background(), rule, nil, path, "**.", []string{"t.co"}); err != nil {
		t.Fatalf("load rule: %v", err)
	}

	if !rule.Match("manual.example") {
		t.Fatal("expected existing manual rule to remain")
	}
	if rule.Match("t.co") {
		t.Fatal("expected skipped file rule to be absent")
	}
	if !rule.Match("example.com") {
		t.Fatal("expected non-skipped file rule to be loaded")
	}
}

func TestLoadRuleSkipsPrefixedFileRule(t *testing.T) {
	t.Parallel()

	path := writeGzipRuleFile(t, "t.co\nexample.com\n")
	rule := suffixtree.NewNodeFromRules()

	if err := loadRule(context.Background(), rule, nil, path, "blocked.", []string{"blocked.t.co"}); err != nil {
		t.Fatalf("load rule: %v", err)
	}

	if rule.Match("blocked.t.co") {
		t.Fatal("expected prefixed file rule to be skipped")
	}
	if !rule.Match("blocked.example.com") {
		t.Fatal("expected non-skipped prefixed file rule to be loaded")
	}
}

func writeGzipRuleFile(t *testing.T, content string) string {
	t.Helper()

	path := t.TempDir() + "/rules.txt"
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create rule file: %v", err)
	}
	gz := gzip.NewWriter(file)
	if _, err := gz.Write([]byte(content)); err != nil {
		t.Fatalf("write gzip rule file: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close rule file: %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("write rule file: %v", err)
	}
	return path
}
