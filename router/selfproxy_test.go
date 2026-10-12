package router

import (
	"context"
	"errors"
	"net"
	"testing"
)

// newSelfTargetRouter builds a router whose proxy dialer hands back an
// in-memory pair and whose self-target lookup is stubbed, so the guard runs
// without DNS or a network.
func newSelfTargetRouter(t *testing.T, selfAddrs []string, resolve func(context.Context, string) ([]net.IP, error)) *Router {
	t.Helper()

	r, err := NewRouter(nil, "", "", "", func(_, _ string, _ uint16) (net.Conn, error) {
		client, server := net.Pipe()
		t.Cleanup(func() { _ = server.Close() })
		return client, nil
	})
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })

	r.SetSelfProxyAddrs(selfAddrs...)
	if resolve != nil {
		r.selfProxyLookup = resolve
	}
	return r
}

func TestDialProxyOnlyRefusesSelfTargetLiteral(t *testing.T) {
	t.Parallel()

	r := newSelfTargetRouter(t, []string{"100.64.1.1:80", "100.64.1.1:443", "100.64.1.1:1080"}, nil)

	for _, port := range []uint16{80, 443, 1080} {
		if _, err := r.DialProxyOnly("tcp", "100.64.1.1", port); !errors.Is(err, ErrSelfProxyTarget) {
			t.Errorf("dial 100.64.1.1:%d err = %v, want ErrSelfProxyTarget", port, err)
		}
	}
}

func TestDialProxyOnlyRefusesNameResolvingToSelf(t *testing.T) {
	t.Parallel()

	lookups := 0
	r := newSelfTargetRouter(t, []string{"100.64.1.1:80"}, func(context.Context, string) ([]net.IP, error) {
		lookups++
		return []net.IP{net.ParseIP("100.64.1.1")}, nil
	})

	// The recursion this guard exists for repeats the same name, so the
	// decision must be cached rather than paid per dial.
	for i := range 2 {
		if _, err := r.DialProxyOnly("tcp", "sower.example.com", 80); !errors.Is(err, ErrSelfProxyTarget) {
			t.Fatalf("dial %d err = %v, want ErrSelfProxyTarget", i, err)
		}
	}
	if lookups != 1 {
		t.Fatalf("lookups = %d, want 1: the self-target decision is not cached", lookups)
	}
}

func TestDialProxyOnlyAllowsNonSelfTargets(t *testing.T) {
	t.Parallel()

	lookups := 0
	r := newSelfTargetRouter(t, []string{"100.64.1.1:80", "100.64.1.1:443"}, func(context.Context, string) ([]net.IP, error) {
		lookups++
		return []net.IP{net.ParseIP("93.184.216.34")}, nil
	})

	if _, err := r.DialProxyOnly("tcp", "example.com", 443); err != nil {
		t.Fatalf("dial to a remote address on a served port: %v", err)
	}
	// A port this client does not serve proxy traffic on cannot loop, so it
	// must not even cost a lookup.
	if _, err := r.DialProxyOnly("tcp", "100.64.1.1", 8443); err != nil {
		t.Fatalf("dial to an unserved port: %v", err)
	}
	if lookups != 1 {
		t.Fatalf("lookups = %d, want 1: unserved ports must skip resolution", lookups)
	}
}

// A listener bound to an unspecified address answers at every local address,
// so any name resolving to one of them is a self target.
func TestDialProxyOnlyRefusesUnspecifiedBind(t *testing.T) {
	t.Parallel()

	r := newSelfTargetRouter(t, []string{"0.0.0.0:443"}, nil)
	if _, err := r.DialProxyOnly("tcp", "192.0.2.10", 443); !errors.Is(err, ErrSelfProxyTarget) {
		t.Fatalf("dial to an address served by an unspecified bind err = %v, want ErrSelfProxyTarget", err)
	}
}

// A resolution failure says nothing about self-targeting: the dial proceeds so
// the relay reports its own resolution result.
func TestDialProxyOnlyAllowsTargetThatFailsToResolve(t *testing.T) {
	t.Parallel()

	r := newSelfTargetRouter(t, []string{"100.64.1.1:80"}, func(context.Context, string) ([]net.IP, error) {
		return nil, errors.New("no such host")
	})
	if _, err := r.DialProxyOnly("tcp", "missing.example.com", 80); err != nil {
		t.Fatalf("dial after a resolution failure: %v", err)
	}
}
