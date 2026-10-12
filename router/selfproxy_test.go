package router

import (
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// newSelfTargetRouter builds a router whose proxy dialer hands back an
// in-memory pair and whose self-target lookup is stubbed, so the guard runs
// without DNS or a network. A nil resolve keeps the production lookup, which
// goes through the router's upstream resolvers.
func newSelfTargetRouter(t *testing.T, selfAddrs []string, resolve func(string) ([]net.IP, error)) *Router {
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
	r := newSelfTargetRouter(t, []string{"100.64.1.1:80"}, func(string) ([]net.IP, error) {
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
	r := newSelfTargetRouter(t, []string{"100.64.1.1:80", "100.64.1.1:443"}, func(string) ([]net.IP, error) {
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

	r := newSelfTargetRouter(t, []string{"100.64.1.1:80"}, func(string) ([]net.IP, error) {
		return nil, errors.New("no such host")
	})
	if _, err := r.DialProxyOnly("tcp", "missing.example.com", 80); err != nil {
		t.Fatalf("dial after a resolution failure: %v", err)
	}
}

func TestDialProxyOnlyAllowsTargetWithNoAnswer(t *testing.T) {
	t.Parallel()

	r := newSelfTargetRouter(t, []string{"100.64.1.1:80"}, func(string) ([]net.IP, error) {
		return nil, nil
	})
	if _, err := r.DialProxyOnly("tcp", "nodata.example.com", 80); err != nil {
		t.Fatalf("dial after an empty answer: %v", err)
	}
}

// DialSmart reaches directDial without passing DialProxyOnly, so the guard has
// to sit on that path too: a name a direct rule matches, or one the access
// probe marks reachable, would otherwise dial this client's own listener in a
// loop that never leaves the host.
func TestDialSmartRefusesDirectSelfTarget(t *testing.T) {
	t.Parallel()

	r := newSelfTargetRouter(t, []string{"100.64.1.1:80"}, func(string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("100.64.1.1")}, nil
	})
	r.DirectRule.Add("self.example.com")

	if _, err := r.DialSmart("tcp", "self.example.com", 80); !errors.Is(err, ErrSelfProxyTarget) {
		t.Fatalf("direct-routed self target err = %v, want ErrSelfProxyTarget", err)
	}
}

func TestDialSmartRefusesSelfTargetLiteral(t *testing.T) {
	t.Parallel()

	r := newSelfTargetRouter(t, []string{"100.64.1.1:443"}, nil)
	if _, err := r.DialSmart("tcp", "100.64.1.1", 443); !errors.Is(err, ErrSelfProxyTarget) {
		t.Fatalf("literal self target on the smart path err = %v, want ErrSelfProxyTarget", err)
	}
}

// startUpstreamDNS serves a fake upstream resolver answering the given names,
// so the guard's production lookup can be exercised without the network.
func startUpstreamDNS(t *testing.T, answers map[string]net.IP) string {
	t.Helper()

	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen fake upstream dns: %v", err)
	}
	server := &dns.Server{PacketConn: conn, Handler: dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		resp := new(dns.Msg)
		resp.SetReply(req)
		if len(req.Question) > 0 {
			question := req.Question[0]
			name := strings.ToLower(strings.TrimSuffix(question.Name, "."))
			if ip, ok := answers[name]; ok {
				switch {
				case question.Qtype == dns.TypeA && ip.To4() != nil:
					resp.Answer = []dns.RR{&dns.A{
						Hdr: dns.RR_Header{Name: question.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
						A:   ip,
					}}
				case question.Qtype == dns.TypeAAAA && ip.To4() == nil:
					resp.Answer = []dns.RR{&dns.AAAA{
						Hdr:  dns.RR_Header{Name: question.Name, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 60},
						AAAA: ip,
					}}
				}
			}
		}
		_ = w.WriteMsg(resp)
	})}
	go func() { _ = server.ActivateAndServe() }()
	t.Cleanup(func() { _ = server.Shutdown() })

	return conn.LocalAddr().String()
}

func newSelfTargetRouterWithUpstream(t *testing.T, selfAddrs []string, answers map[string]net.IP) *Router {
	t.Helper()

	upstream := startUpstreamDNS(t, answers)
	r := newSelfTargetRouter(t, selfAddrs, nil)
	r.dns.upstreamAddrs = []string{upstream}
	r.dns.upstreamIndex = 0
	r.dns.refreshAt = time.Now().Add(time.Hour)
	return r
}

// The guard must ask the upstream resolvers, not the system resolver: this
// client answers its own DNS for every domain it proxies, so its own answer
// would make every proxied dial look like a self target. A name the upstream
// resolves remotely stays proxied.
func TestDialProxyOnlyAllowsDomainTheUpstreamResolvesRemotely(t *testing.T) {
	t.Parallel()

	r := newSelfTargetRouterWithUpstream(t, []string{"100.64.1.1:80", "100.64.1.1:443"},
		map[string]net.IP{"proxied.example.com": net.ParseIP("93.184.216.34")})

	if _, err := r.DialProxyOnly("tcp", "proxied.example.com", 443); err != nil {
		t.Fatalf("proxied domain refused: %v", err)
	}
}

// A name whose own record points at this client is what the recursion needs,
// and the upstream answer is where that shows up.
func TestDialProxyOnlyRefusesNameTheUpstreamPointsAtSelf(t *testing.T) {
	t.Parallel()

	r := newSelfTargetRouterWithUpstream(t, []string{"100.64.1.1:80"},
		map[string]net.IP{"sower.example.com": net.ParseIP("100.64.1.1")})

	if _, err := r.DialProxyOnly("tcp", "sower.example.com", 80); !errors.Is(err, ErrSelfProxyTarget) {
		t.Fatalf("self-published name err = %v, want ErrSelfProxyTarget", err)
	}
}
