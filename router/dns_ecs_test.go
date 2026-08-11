package router

import (
	"net"
	"sync/atomic"
	"testing"

	"github.com/miekg/dns"
)

func ecsMsg(addr string, family uint16, netmask uint8) *dns.Msg {
	msg := new(dns.Msg)
	msg.SetQuestion("example.com.", dns.TypeA)
	opt := new(dns.OPT)
	opt.Hdr.Name = "."
	opt.Hdr.Rrtype = dns.TypeOPT
	opt.Option = append(opt.Option, &dns.EDNS0_SUBNET{
		Code:          dns.EDNS0SUBNET,
		Family:        family,
		SourceNetmask: netmask,
		SourceScope:   0,
		Address:       net.ParseIP(addr),
	})
	msg.Extra = append(msg.Extra, opt)
	return msg
}

func TestClientIPOfPrefersECS(t *testing.T) {
	t.Parallel()

	remote := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5353}

	// Queries forwarded by dnsmasq --add-subnet carry the real client in
	// ECS; it must win over the transport source (the resolver itself).
	if got := ClientIPOf(ecsMsg("192.168.1.42", 1, 32), remote); got != "192.168.1.42" {
		t.Fatalf("ECS v4: got %q", got)
	}
	if got := ClientIPOf(ecsMsg("fd00::1", 2, 128), remote); got != "fd00::1" {
		t.Fatalf("ECS v6: got %q", got)
	}

	// No ECS: fall back to the transport remote address.
	plain := new(dns.Msg)
	plain.SetQuestion("example.com.", dns.TypeA)
	if got := ClientIPOf(plain, remote); got != "127.0.0.1" {
		t.Fatalf("fallback: got %q", got)
	}
	if got := ClientIPOf(plain, nil); got != "" {
		t.Fatalf("nil remote: got %q", got)
	}
}

func TestStripECS(t *testing.T) {
	t.Parallel()

	// ECS-only query: OPT section left empty.
	req := ecsMsg("192.168.1.42", 1, 32)
	stripECS(req)
	if opt := req.IsEdns0(); opt == nil || len(opt.Option) != 0 {
		t.Fatalf("strip all: opt=%v options=%d", opt, len(opt.Option))
	}

	// ECS alongside another EDNS option: only ECS is removed.
	req = ecsMsg("192.168.1.42", 1, 32)
	req.IsEdns0().Option = append(req.IsEdns0().Option, &dns.EDNS0_NSID{Code: dns.EDNS0NSID, Nsid: "test"})
	stripECS(req)
	opt := req.IsEdns0()
	if opt == nil || len(opt.Option) != 1 {
		t.Fatalf("strip one: opt=%v options=%d", opt, len(opt.Option))
	}
	if _, ok := opt.Option[0].(*dns.EDNS0_NSID); !ok {
		t.Fatalf("unexpected remaining option: %#v", opt.Option[0])
	}

	// No EDNS at all: no-op.
	plain := new(dns.Msg)
	plain.SetQuestion("example.com.", dns.TypeA)
	stripECS(plain)
	if plain.IsEdns0() != nil {
		t.Fatal("no-op query gained an OPT record")
	}
}

func TestExchangeStripsECSUpstream(t *testing.T) {
	t.Parallel()

	var seen atomic.Bool
	upstreamAddr := startUDPTestDNSServer(t, dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		if opt := req.IsEdns0(); opt != nil {
			for _, o := range opt.Option {
				if _, isECS := o.(*dns.EDNS0_SUBNET); isECS {
					t.Errorf("upstream received ECS option")
				}
			}
		}
		seen.Store(true)
		m := new(dns.Msg)
		m.SetReply(req)
		m.Answer = append(m.Answer, &dns.A{
			Hdr: dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
			A:   net.ParseIP("1.2.3.4"),
		})
		_ = w.WriteMsg(m)
	}))

	r := newTestRouter(t, nil, "", "223.5.5.5", "", nil)
	r.dns.upstreamAddrs = []string{upstreamAddr}
	r.dns.upstreamIndex = 0
	t.Cleanup(func() { _ = r.Close() })

	req := ecsMsg("192.168.1.42", 1, 32)
	resp, err := r.Exchange(req)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if !seen.Load() {
		t.Fatal("upstream handler never invoked")
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("unexpected answer count: %d", len(resp.Answer))
	}
}
