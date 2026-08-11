package admin

import (
	"context"
	"testing"
)

func TestTotalsWithHostnames(t *testing.T) {
	t.Parallel()

	resolver := &fakeHostnameResolver{byIP: map[string]string{
		"192.168.1.2": "n100.lan",
		"100.64.1.1":  "router.tailnet.ts.net",
	}}
	stats := newTestStats(t)
	stats.RecordDNS("example.com.", "192.168.1.2")
	stats.RecordDNS("example.com.", "100.64.1.1")

	s := NewServer(Options{Password: "secret", Version: "v1", Date: "2026-01-01", Stats: stats, Hostnames: resolver})

	totals := s.totalsWithHostnames()
	got := map[string]string{}
	for _, c := range totals.Clients {
		got[c.IP] = c.Hostname
	}
	if got["192.168.1.2"] != "n100.lan" {
		t.Fatalf("hostname for 192.168.1.2: %q", got["192.168.1.2"])
	}
	if got["100.64.1.1"] != "router.tailnet.ts.net" {
		t.Fatalf("hostname for 100.64.1.1: %q", got["100.64.1.1"])
	}

	// Second call hits the cache: the resolver must not be consulted again.
	before := resolver.lookups.Load()
	totals = s.totalsWithHostnames()
	if resolver.lookups.Load() != before {
		t.Fatalf("cache miss on second call: lookups %d -> %d", before, resolver.lookups.Load())
	}
	if totals.Clients[0].Hostname != "n100.lan" && totals.Clients[1].Hostname != "n100.lan" {
		t.Fatalf("cached hostname lost: %+v", totals.Clients)
	}
}

var _ HostnameResolver = (*fakeHostnameResolver)(nil)

func TestHostnameResolverInterface(t *testing.T) {
	t.Parallel()

	var r HostnameResolver = &fakeHostnameResolver{byIP: map[string]string{}}
	if got := r.Hostname(context.Background(), "192.168.1.9"); got != "" {
		t.Fatalf("unexpected hostname %q", got)
	}
}
