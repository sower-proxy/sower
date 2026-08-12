package router

import (
	"net"
	"sync/atomic"
	"testing"

	"github.com/miekg/dns"
)

func TestParseReverseName(t *testing.T) {
	t.Parallel()

	ip, ok := parseReverseName("1.2.3.4.in-addr.arpa.")
	if !ok || !ip.Equal(net.ParseIP("4.3.2.1")) {
		t.Fatalf("parse 1.2.3.4.in-addr.arpa: ip=%v ok=%v", ip, ok)
	}
	if ip, ok := parseReverseName("4.3.2.1.IN-ADDR.ARPA."); !ok || !ip.Equal(net.ParseIP("1.2.3.4")) {
		t.Fatalf("case-insensitive parse: ip=%v ok=%v", ip, ok)
	}
	if ip, ok := parseReverseName("8.8.8.8.in-addr.arpa."); !ok || !ip.Equal(net.ParseIP("8.8.8.8")) {
		t.Fatalf("parse public reverse name: ip=%v ok=%v", ip, ok)
	}

	v6 := net.ParseIP("fd00::1")
	arpa, err := dns.ReverseAddr(v6.String())
	if err != nil {
		t.Fatalf("reverse addr: %v", err)
	}
	ip, ok = parseReverseName(arpa)
	if !ok || !ip.Equal(v6) {
		t.Fatalf("parse %s: ip=%v ok=%v", arpa, ip, ok)
	}

	for _, name := range []string{
		"168.192.in-addr.arpa.",   // incomplete zone name
		"example.com.",            // not a reverse name
		"256.1.2.3.in-addr.arpa.", // octet out of range
		"1.2.3.in-addr.arpa.",     // too few octets
		"1.2.3.4.5.in-addr.arpa.", // too many octets
		"g.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.ip6.arpa.",  // invalid hex nibble
		"ff.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.ip6.arpa.", // multi-char label
		"1.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.0.ip6.arpa.",      // 31 nibbles
	} {
		if ip, ok := parseReverseName(name); ok {
			t.Fatalf("expected %q to be rejected, got %v", name, ip)
		}
	}
}

func TestIsInternalIP(t *testing.T) {
	t.Parallel()

	internal := []string{
		"10.0.0.1", "172.16.0.1", "172.31.255.255", "192.168.1.1",
		"100.64.0.1", "100.127.255.254", // CGNAT
		"169.254.1.1",        // link-local unicast
		"127.0.0.1",          // loopback
		"0.0.0.0",            // unspecified
		"fc00::1", "fd00::1", // IPv6 ULA
		"fe80::1", // IPv6 link-local
		"::1",     // IPv6 loopback
	}
	for _, s := range internal {
		if !isInternalIP(net.ParseIP(s)) {
			t.Errorf("expected %s to be internal", s)
		}
	}

	public := []string{
		"8.8.8.8", "1.1.1.1", "223.5.5.5",
		"100.63.255.255", "100.128.0.1", // CGNAT boundaries
		"192.0.2.1", // TEST-NET-1
		"224.0.0.1", // multicast
		"2400:3200::1", "2001:4860:4860::8888",
	}
	for _, s := range public {
		if isInternalIP(net.ParseIP(s)) {
			t.Errorf("expected %s to be public", s)
		}
	}
}

func TestDNSSelectedUpstreamIsInternal(t *testing.T) {
	t.Parallel()

	r := newTestRouter(t, nil, "", "", "", nil)

	// The gate judges the currently selected upstream, not the pool: a
	// degraded mixed pool must not forward internal PTR queries to the
	// public fallback it degraded onto.
	r.dns.upstreamAddrs = []string{"8.8.8.8:53", "223.5.5.5:53"}
	r.dns.upstreamIndex = 0
	if r.dnsSelectedUpstreamIsInternal() {
		t.Fatal("public selected upstream reported as internal")
	}

	r.dns.upstreamAddrs = []string{"192.168.1.1:53"}
	r.dns.upstreamIndex = 0
	if !r.dnsSelectedUpstreamIsInternal() {
		t.Fatal("internal selected upstream not reported")
	}

	// Same pool, different selection: internal at index 0, public after
	// degradation at index 1 must not count as internal.
	r.dns.upstreamAddrs = []string{"192.168.1.1:53", "8.8.8.8:53"}
	r.dns.upstreamIndex = 1
	if r.dnsSelectedUpstreamIsInternal() {
		t.Fatal("degraded public upstream reported as internal")
	}

	// Out-of-range index degrades to the configured/fallback judgment.
	r.dns.upstreamAddrs = []string{"192.168.1.1:53"}
	r.dns.upstreamIndex = 5
	if r.dnsSelectedUpstreamIsInternal() {
		t.Fatal("out-of-range index must fall back to configured judgment")
	}

	r.dns.upstreamAddrs = []string{"[::1]:53"}
	r.dns.upstreamIndex = 0
	if !r.dnsSelectedUpstreamIsInternal() {
		t.Fatal("ipv6 loopback upstream not reported")
	}

	r.dns.upstreamAddrs = nil
	r.dns.upstreamDNS = "192.168.1.1"
	r.dns.fallbackDNS = "223.5.5.5"
	if !r.dnsSelectedUpstreamIsInternal() {
		t.Fatal("internal configured upstream not reported")
	}

	r.dns.upstreamDNS = ""
	r.dns.fallbackDNS = "223.5.5.5"
	if r.dnsSelectedUpstreamIsInternal() {
		t.Fatal("public fallback reported as internal")
	}
}

func TestLocalNXDOMAINCarriesOPTForEDNSQueries(t *testing.T) {
	t.Parallel()

	r := newTestRouter(t, []string{"127.0.0.2"}, "", "223.5.5.5", "", nil)
	r.dns.upstreamAddrs = []string{"8.8.8.8:53"}
	r.dns.upstreamIndex = 0

	// ecsMsg builds a query with an EDNS0 OPT record; RFC 6891 requires the
	// response to an EDNS query to carry an OPT record as well.
	req := ecsMsg("192.168.1.42", 1, 32)
	req.SetQuestion("1.1.168.192.in-addr.arpa.", dns.TypePTR)
	writer := &mockDNSWriter{localAddr: &net.UDPAddr{IP: net.ParseIP("127.0.0.2"), Port: 53}}

	r.ServeDNS(writer, req)

	if writer.msg == nil {
		t.Fatal("expected response")
	}
	if writer.msg.Rcode != dns.RcodeNameError {
		t.Fatalf("expected NXDOMAIN, got %s", dns.RcodeToString[writer.msg.Rcode])
	}
	if writer.msg.IsEdns0() == nil {
		t.Fatal("NXDOMAIN reply to an EDNS query must carry an OPT record")
	}
}

func TestIsInternalHost(t *testing.T) {
	t.Parallel()

	internal := []string{
		"8.8.8.8:53",
		"192.168.1.1", "192.168.1.1:53",
		"[fd00::1]:53", "[::1]", "[fe80::1]:53",
	}
	for _, hostport := range internal[1:] {
		if !isInternalHost(hostport) {
			t.Errorf("expected %q to be internal", hostport)
		}
	}
	if isInternalHost(internal[0]) {
		t.Error("expected 8.8.8.8:53 to be public")
	}
	if isInternalHost("") {
		t.Error("expected empty host to be public")
	}
}

func TestServeDNSInternalReverseLookup(t *testing.T) {
	t.Parallel()

	t.Run("public upstream answers NXDOMAIN locally", func(t *testing.T) {
		r := newTestRouter(t, []string{"127.0.0.2"}, "", "223.5.5.5", "", nil)
		r.dns.upstreamAddrs = []string{"8.8.8.8:53"}
		r.dns.upstreamIndex = 0

		req := new(dns.Msg)
		req.SetQuestion("1.1.168.192.in-addr.arpa.", dns.TypePTR)
		writer := &mockDNSWriter{localAddr: &net.UDPAddr{IP: net.ParseIP("127.0.0.2"), Port: 53}}

		r.ServeDNS(writer, req)

		if writer.msg == nil {
			t.Fatal("expected response")
		}
		if writer.msg.Rcode != dns.RcodeNameError {
			t.Fatalf("expected NXDOMAIN, got %s", dns.RcodeToString[writer.msg.Rcode])
		}
		if len(writer.msg.Answer) != 0 {
			t.Fatalf("expected no answer, got %v", writer.msg.Answer)
		}
	})

	t.Run("internal upstream forwards to it", func(t *testing.T) {
		var upstreamQueries atomic.Int32
		upstreamAddr := startUDPTestDNSServer(t, dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
			upstreamQueries.Add(1)
			resp := new(dns.Msg)
			resp.SetReply(req)
			resp.Answer = []dns.RR{&dns.PTR{
				Hdr: dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypePTR, Class: dns.ClassINET, Ttl: 60},
				Ptr: "host.lan.",
			}}
			_ = w.WriteMsg(resp)
		}))

		r := newTestRouter(t, []string{"127.0.0.2"}, "", "223.5.5.5", "", nil)
		r.dns.upstreamAddrs = []string{upstreamAddr} // 127.0.0.1:port counts as internal
		r.dns.upstreamIndex = 0

		req := new(dns.Msg)
		req.SetQuestion("1.1.168.192.in-addr.arpa.", dns.TypePTR)
		writer := &mockDNSWriter{localAddr: &net.UDPAddr{IP: net.ParseIP("127.0.0.2"), Port: 53}}

		r.ServeDNS(writer, req)

		if upstreamQueries.Load() != 1 {
			t.Fatalf("expected forward to internal upstream, got %d queries", upstreamQueries.Load())
		}
		if writer.msg == nil || writer.msg.Rcode != dns.RcodeSuccess {
			t.Fatalf("expected NOERROR response, got %+v", writer.msg)
		}
		if len(writer.msg.Answer) != 1 {
			t.Fatalf("expected PTR answer, got %v", writer.msg.Answer)
		}
		if ptr, ok := writer.msg.Answer[0].(*dns.PTR); !ok || ptr.Ptr != "host.lan." {
			t.Fatalf("unexpected answer: %v", writer.msg.Answer[0])
		}
	})

	t.Run("public reverse name still forwards", func(t *testing.T) {
		var upstreamQueries atomic.Int32
		upstreamAddr := startUDPTestDNSServer(t, dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
			upstreamQueries.Add(1)
			resp := new(dns.Msg)
			resp.SetReply(req)
			_ = w.WriteMsg(resp)
		}))

		r := newTestRouter(t, []string{"127.0.0.2"}, "", "223.5.5.5", "", nil)
		r.dns.upstreamAddrs = []string{upstreamAddr}
		r.dns.upstreamIndex = 0

		req := new(dns.Msg)
		req.SetQuestion("4.2.2.4.in-addr.arpa.", dns.TypePTR)
		writer := &mockDNSWriter{localAddr: &net.UDPAddr{IP: net.ParseIP("127.0.0.2"), Port: 53}}

		r.ServeDNS(writer, req)

		if upstreamQueries.Load() != 1 {
			t.Fatalf("expected public reverse lookup forwarded, got %d queries", upstreamQueries.Load())
		}
	})

	t.Run("degraded mixed pool does not forward internal reverse", func(t *testing.T) {
		r := newTestRouter(t, []string{"127.0.0.2"}, "", "223.5.5.5", "", nil)
		// The pool contains an internal server, but the current selection
		// degraded onto the public fallback: internal PTR must not reach it.
		r.dns.upstreamAddrs = []string{"192.168.1.1:53", "8.8.8.8:53"}
		r.dns.upstreamIndex = 1

		req := new(dns.Msg)
		req.SetQuestion("1.1.168.192.in-addr.arpa.", dns.TypePTR)
		writer := &mockDNSWriter{localAddr: &net.UDPAddr{IP: net.ParseIP("127.0.0.2"), Port: 53}}

		r.ServeDNS(writer, req)

		if writer.msg == nil {
			t.Fatal("expected response")
		}
		if writer.msg.Rcode != dns.RcodeNameError {
			t.Fatalf("expected NXDOMAIN, got %s", dns.RcodeToString[writer.msg.Rcode])
		}
	})

	t.Run("selected internal upstream in mixed pool forwards", func(t *testing.T) {
		var upstreamQueries atomic.Int32
		upstreamAddr := startUDPTestDNSServer(t, dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
			upstreamQueries.Add(1)
			resp := new(dns.Msg)
			resp.SetReply(req)
			_ = w.WriteMsg(resp)
		}))

		r := newTestRouter(t, []string{"127.0.0.2"}, "", "223.5.5.5", "", nil)
		// Internal server selected first: forward even though a public
		// fallback sits behind it.
		r.dns.upstreamAddrs = []string{upstreamAddr, "8.8.8.8:53"}
		r.dns.upstreamIndex = 0

		req := new(dns.Msg)
		req.SetQuestion("1.1.168.192.in-addr.arpa.", dns.TypePTR)
		writer := &mockDNSWriter{localAddr: &net.UDPAddr{IP: net.ParseIP("127.0.0.2"), Port: 53}}

		r.ServeDNS(writer, req)

		if upstreamQueries.Load() != 1 {
			t.Fatalf("expected forward to selected internal upstream, got %d queries", upstreamQueries.Load())
		}
	})

	t.Run("internal upstream failure never degrades onto public fallback", func(t *testing.T) {
		var fallbackQueries atomic.Int32
		fallbackAddr := startUDPTestDNSServer(t, dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
			fallbackQueries.Add(1)
			resp := new(dns.Msg)
			resp.SetReply(req)
			_ = w.WriteMsg(resp)
		}))
		// The selected internal upstream answers SERVFAIL (retryable); the
		// dedicated internal-PTR path must surface the failure instead of
		// retrying the query on the public fallback behind it.
		servfailAddr := startUDPTestDNSServer(t, dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
			m := new(dns.Msg)
			m.SetRcode(req, dns.RcodeServerFailure)
			_ = w.WriteMsg(m)
		}))

		r := newTestRouter(t, []string{"127.0.0.2"}, "", "223.5.5.5", "", nil)
		r.dns.upstreamAddrs = []string{servfailAddr, fallbackAddr}
		r.dns.upstreamIndex = 0

		req := new(dns.Msg)
		req.SetQuestion("1.1.168.192.in-addr.arpa.", dns.TypePTR)
		writer := &mockDNSWriter{localAddr: &net.UDPAddr{IP: net.ParseIP("127.0.0.2"), Port: 53}}

		r.ServeDNS(writer, req)

		if writer.msg == nil {
			t.Fatal("expected response")
		}
		if writer.msg.Rcode != dns.RcodeServerFailure {
			t.Fatalf("expected SERVFAIL, got %s", dns.RcodeToString[writer.msg.Rcode])
		}
		if fallbackQueries.Load() != 0 {
			t.Fatalf("internal PTR must not retry on public fallback, got %d queries", fallbackQueries.Load())
		}
	})

	t.Run("ipv6 internal reverse with public upstream answers NXDOMAIN locally", func(t *testing.T) {
		r := newTestRouter(t, []string{"127.0.0.2"}, "", "223.5.5.5", "", nil)
		r.dns.upstreamAddrs = []string{"8.8.8.8:53"}
		r.dns.upstreamIndex = 0

		arpa, err := dns.ReverseAddr("fd00::1")
		if err != nil {
			t.Fatalf("reverse addr: %v", err)
		}
		req := new(dns.Msg)
		req.SetQuestion(arpa, dns.TypePTR)
		writer := &mockDNSWriter{localAddr: &net.UDPAddr{IP: net.ParseIP("127.0.0.2"), Port: 53}}

		r.ServeDNS(writer, req)

		if writer.msg == nil {
			t.Fatal("expected response")
		}
		if writer.msg.Rcode != dns.RcodeNameError {
			t.Fatalf("expected NXDOMAIN, got %s", dns.RcodeToString[writer.msg.Rcode])
		}
	})

	t.Run("ipv6 internal reverse with internal upstream forwards", func(t *testing.T) {
		var upstreamQueries atomic.Int32
		upstreamAddr := startUDPTestDNSServer(t, dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
			upstreamQueries.Add(1)
			resp := new(dns.Msg)
			resp.SetReply(req)
			_ = w.WriteMsg(resp)
		}))

		r := newTestRouter(t, []string{"127.0.0.2"}, "", "223.5.5.5", "", nil)
		r.dns.upstreamAddrs = []string{upstreamAddr}
		r.dns.upstreamIndex = 0

		arpa, err := dns.ReverseAddr("fe80::1")
		if err != nil {
			t.Fatalf("reverse addr: %v", err)
		}
		req := new(dns.Msg)
		req.SetQuestion(arpa, dns.TypePTR)
		writer := &mockDNSWriter{localAddr: &net.UDPAddr{IP: net.ParseIP("127.0.0.2"), Port: 53}}

		r.ServeDNS(writer, req)

		if upstreamQueries.Load() != 1 {
			t.Fatalf("expected forward to internal upstream, got %d queries", upstreamQueries.Load())
		}
	})
}
