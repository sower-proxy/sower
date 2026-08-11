package admin

import (
	"context"
	"sync/atomic"
	"testing"
)

type fakeHostnameResolver struct {
	lookups atomic.Int32
	byIP    map[string]string
}

func (f *fakeHostnameResolver) Hostname(_ context.Context, ip string) string {
	f.lookups.Add(1)
	return f.byIP[ip]
}

func TestAttachHostnamesResolvesAndCaches(t *testing.T) {
	t.Parallel()

	resolver := &fakeHostnameResolver{byIP: map[string]string{
		"192.168.1.2":    "android-phone.lan",
		"100.108.222.67": "workstation.tailnet.ts.net",
	}}
	cache := newHostnameCache(resolver)

	clients := []ClientStat{
		{IP: "192.168.1.2"},
		{IP: "100.108.222.67"},
		{IP: "1.2.3.4"}, // no PTR data
	}
	cache.attach(clients)
	if clients[0].Hostname != "android-phone.lan" {
		t.Fatalf("client 0 hostname: %q", clients[0].Hostname)
	}
	if clients[1].Hostname != "workstation.tailnet.ts.net" {
		t.Fatalf("client 1 hostname: %q", clients[1].Hostname)
	}
	if clients[2].Hostname != "" {
		t.Fatalf("client 2 should stay empty, got %q", clients[2].Hostname)
	}
	if resolver.lookups.Load() != 3 {
		t.Fatalf("expected 3 lookups on first pass, got %d", resolver.lookups.Load())
	}

	// Second pass must hit the cache: no new lookups, same hostnames.
	again := []ClientStat{{IP: "192.168.1.2"}, {IP: "100.108.222.67"}}
	cache.attach(again)
	if resolver.lookups.Load() != 3 {
		t.Fatalf("cache miss: lookups=%d", resolver.lookups.Load())
	}
	if again[0].Hostname != "android-phone.lan" {
		t.Fatalf("cached hostname: %q", again[0].Hostname)
	}
}

func TestAttachHostnamesNoopWithoutResolver(t *testing.T) {
	t.Parallel()

	s := &Server{}
	clients := []ClientStat{{IP: "192.168.1.2"}}
	s.attachHostnames(clients)
	if clients[0].Hostname != "" {
		t.Fatalf("noop attach wrote hostname %q", clients[0].Hostname)
	}
}

func TestAttachHostnamesConcurrent(t *testing.T) {
	t.Parallel()

	resolver := &fakeHostnameResolver{byIP: map[string]string{"192.168.1.2": "phone.lan"}}
	cache := newHostnameCache(resolver)

	done := make(chan struct{})
	for range 8 {
		go func() {
			defer func() { done <- struct{}{} }()
			for range 20 {
				clients := []ClientStat{{IP: "192.168.1.2"}, {IP: "10.0.0.8"}}
				cache.attach(clients)
				if clients[0].Hostname != "phone.lan" {
					t.Errorf("unexpected hostname %q", clients[0].Hostname)
				}
			}
		}()
	}
	for range 8 {
		<-done
	}
}
