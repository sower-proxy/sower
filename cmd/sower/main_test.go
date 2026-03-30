package main

import (
	"compress/gzip"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"testing"
)

func TestFetchRuleFileEmptyPathReturnsClosedChannel(t *testing.T) {
	t.Parallel()

	ch := fetchRuleFile(context.Background(), nil, "")
	if _, ok := <-ch; ok {
		t.Fatal("expected closed channel for empty rule file path")
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

	got := make([]string, 0, 2)
	for line := range fetchRuleFile(context.Background(), dialFn, srv.URL) {
		got = append(got, line)
	}

	want := []string{"alpha", "beta"}
	if !slices.Equal(got, want) {
		t.Fatalf("unexpected rules: got %v want %v", got, want)
	}
}
