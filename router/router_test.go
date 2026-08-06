package router

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func newTestRouter(t *testing.T, serveIPs []string, upstreamDNS, fallbackDNS, mmdbFile string, proxyDial ProxyDialFn) *Router {
	t.Helper()
	r, err := NewRouter(serveIPs, upstreamDNS, fallbackDNS, mmdbFile, proxyDial)
	if err != nil {
		t.Fatalf("new router: %v", err)
	}
	return r
}

func TestNewRouterInitializesRules(t *testing.T) {
	t.Parallel()

	r := newTestRouter(t, nil, "", "223.5.5.5", "", nil)
	if r.BlockRule == nil || r.DirectRule == nil || r.ProxyRule == nil {
		t.Fatal("expected router rules to be initialized")
	}
}

func TestDNSProxyReplyMatchesQuestionType(t *testing.T) {
	t.Parallel()

	r := newTestRouter(t, []string{"127.0.0.1", "::1"}, "", "223.5.5.5", "", nil)

	reqA := new(dns.Msg)
	reqA.SetQuestion("example.com.", dns.TypeA)
	respA, err := r.dnsProxyReply("example.com.", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 53}, reqA)
	if err != nil {
		t.Fatalf("dnsProxyReply A failed: %v", err)
	}
	if len(respA.Answer) != 1 {
		t.Fatalf("unexpected A answer count: %d", len(respA.Answer))
	}
	if _, ok := respA.Answer[0].(*dns.A); !ok {
		t.Fatalf("expected A record, got %T", respA.Answer[0])
	}

	reqAAAA := new(dns.Msg)
	reqAAAA.SetQuestion("example.com.", dns.TypeAAAA)
	respAAAA, err := r.dnsProxyReply("example.com.", &net.UDPAddr{IP: net.ParseIP("::1"), Port: 53}, reqAAAA)
	if err != nil {
		t.Fatalf("dnsProxyReply AAAA failed: %v", err)
	}
	if len(respAAAA.Answer) != 1 {
		t.Fatalf("unexpected AAAA answer count: %d", len(respAAAA.Answer))
	}
	if _, ok := respAAAA.Answer[0].(*dns.AAAA); !ok {
		t.Fatalf("expected AAAA record, got %T", respAAAA.Answer[0])
	}
}

func TestDNSProxyReplyPrefersRequestLocalAddr(t *testing.T) {
	t.Parallel()

	r := newTestRouter(t, []string{"127.0.0.1", "127.0.0.2"}, "", "223.5.5.5", "", nil)
	req := new(dns.Msg)
	req.SetQuestion("example.com.", dns.TypeA)

	resp, err := r.dnsProxyReply("example.com.", &net.UDPAddr{IP: net.ParseIP("127.0.0.2"), Port: 53}, req)
	if err != nil {
		t.Fatalf("dnsProxyReply failed: %v", err)
	}

	answer, ok := resp.Answer[0].(*dns.A)
	if !ok {
		t.Fatalf("expected A record, got %T", resp.Answer[0])
	}
	if !answer.A.Equal(net.ParseIP("127.0.0.2")) {
		t.Fatalf("expected request local IP 127.0.0.2, got %s", answer.A)
	}
}

func TestDNSProxyReplyIgnoresUnspecifiedLocalAddr(t *testing.T) {
	t.Parallel()

	r := newTestRouter(t, []string{"127.0.0.2", "0.0.0.0"}, "", "223.5.5.5", "", nil)
	req := new(dns.Msg)
	req.SetQuestion("example.com.", dns.TypeA)

	resp, err := r.dnsProxyReply("example.com.", &net.UDPAddr{IP: net.ParseIP("0.0.0.0"), Port: 53}, req)
	if err != nil {
		t.Fatalf("dnsProxyReply failed: %v", err)
	}

	answer, ok := resp.Answer[0].(*dns.A)
	if !ok {
		t.Fatalf("expected A record, got %T", resp.Answer[0])
	}
	if !answer.A.Equal(net.ParseIP("127.0.0.2")) {
		t.Fatalf("expected configured serve IP 127.0.0.2, got %s", answer.A)
	}
}

func TestDNSProxyReplyFallsBackWhenLocalAddrMissing(t *testing.T) {
	t.Parallel()

	r := newTestRouter(t, []string{"127.0.0.2"}, "", "223.5.5.5", "", nil)
	req := new(dns.Msg)
	req.SetQuestion("example.com.", dns.TypeA)

	resp, err := r.dnsProxyReply("example.com.", nil, req)
	if err != nil {
		t.Fatalf("dnsProxyReply failed: %v", err)
	}

	answer, ok := resp.Answer[0].(*dns.A)
	if !ok {
		t.Fatalf("expected A record, got %T", resp.Answer[0])
	}
	if !answer.A.Equal(net.ParseIP("127.0.0.2")) {
		t.Fatalf("expected configured serve IP 127.0.0.2, got %s", answer.A)
	}
}

func TestServeDNSForUnknownDomainForwardsUpstream(t *testing.T) {
	t.Parallel()

	var upstreamQueries atomic.Int32
	upstreamAddr := startUDPTestDNSServer(t, dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		upstreamQueries.Add(1)
		resp := new(dns.Msg)
		resp.SetReply(req)
		resp.Answer = []dns.RR{&dns.A{
			Hdr: dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 20},
			A:   net.ParseIP("198.51.100.10"),
		}}
		_ = w.WriteMsg(resp)
	}))

	r := newTestRouter(t, []string{"127.0.0.2"}, "", "", "", nil)
	r.dns.upstreamAddrs = []string{upstreamAddr}
	r.dns.upstreamIndex = 0

	req := new(dns.Msg)
	req.SetQuestion("unknown.example.", dns.TypeA)
	writer := &mockDNSWriter{localAddr: &net.UDPAddr{IP: net.ParseIP("127.0.0.2"), Port: 53}}

	r.ServeDNS(writer, req)

	if writer.msg == nil {
		t.Fatal("expected response message")
	}
	if upstreamQueries.Load() != 1 {
		t.Fatalf("expected unknown DNS name to be forwarded once, got %d", upstreamQueries.Load())
	}
	if len(writer.msg.Answer) != 1 {
		t.Fatalf("expected one upstream answer, got rcode=%d answers=%d", writer.msg.Rcode, len(writer.msg.Answer))
	}
	answer, ok := writer.msg.Answer[0].(*dns.A)
	if !ok {
		t.Fatalf("expected upstream A answer, got %T", writer.msg.Answer[0])
	}
	if !answer.A.Equal(net.ParseIP("198.51.100.10")) {
		t.Fatalf("unexpected upstream answer: %s", answer.A)
	}
}

func TestDialSmartFallsBackToProxyForUnknownTCP(t *testing.T) {
	t.Parallel()

	var gotNetwork, gotHost string
	var gotPort uint16
	proxyErr := errors.New("proxy called")
	r := newTestRouter(t, nil, "", "223.5.5.5", "", func(network, host string, port uint16) (net.Conn, error) {
		gotNetwork = network
		gotHost = host
		gotPort = port
		return nil, proxyErr
	})

	_, err := r.DialSmart("tcp", "203.0.113.10", 12345)
	if !errors.Is(err, proxyErr) {
		t.Fatalf("expected proxy fallback error, got %v", err)
	}
	if gotNetwork != "tcp" || gotHost != "203.0.113.10" || gotPort != 12345 {
		t.Fatalf("unexpected proxy target: %s %s:%d", gotNetwork, gotHost, gotPort)
	}
}

func TestExchangeSkipsServeIPInUpstreamList(t *testing.T) {
	t.Parallel()

	r := newTestRouter(t, []string{"127.0.0.1"}, "127.0.0.1", "127.0.0.2", "", nil)
	addrs, err := r.buildUpstreamAddrs()
	if err != nil {
		t.Fatalf("buildUpstreamAddrs failed: %v", err)
	}

	if len(addrs) != 1 {
		t.Fatalf("unexpected upstream list: %v", addrs)
	}
	if addrs[0] != "127.0.0.2:53" {
		t.Fatalf("expected fallback only, got %v", addrs)
	}
}

func TestBuildUpstreamAddrsPrefersConfiguredDNSBeforeFallback(t *testing.T) {
	t.Parallel()

	r := newTestRouter(t, []string{"127.0.0.1"}, "1.1.1.1", "9.9.9.9", "", nil)
	addrs, err := r.buildUpstreamAddrs()
	if err != nil {
		t.Fatalf("buildUpstreamAddrs failed: %v", err)
	}

	want := []string{"1.1.1.1:53", "9.9.9.9:53"}
	if strings.Join(addrs, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected upstream order: got %v want %v", addrs, want)
	}
}

func TestExchangeFallsBackOnRetryableDNSResponse(t *testing.T) {
	t.Parallel()

	primaryAddr := startUDPTestDNSServer(t, dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		resp := new(dns.Msg)
		resp.SetRcode(req, dns.RcodeServerFailure)
		_ = w.WriteMsg(resp)
	}))
	fallbackAddr := startUDPTestDNSServer(t, dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		resp := new(dns.Msg)
		resp.SetReply(req)
		resp.Answer = []dns.RR{&dns.A{
			Hdr: dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 20},
			A:   net.ParseIP("203.0.113.10"),
		}}
		_ = w.WriteMsg(resp)
	}))

	r := newTestRouter(t, nil, "", "", "", nil)
	r.dns.upstreamAddrs = []string{primaryAddr, fallbackAddr}
	r.dns.upstreamIndex = 0

	req := new(dns.Msg)
	req.SetQuestion("example.com.", dns.TypeA)
	resp, err := r.Exchange(req)
	if err != nil {
		t.Fatalf("exchange failed: %v", err)
	}
	if r.dns.upstreamIndex != 1 {
		t.Fatalf("expected degraded upstream index 1, got %d", r.dns.upstreamIndex)
	}
	answer, ok := resp.Answer[0].(*dns.A)
	if !ok {
		t.Fatalf("expected fallback A answer, got %T", resp.Answer[0])
	}
	if !answer.A.Equal(net.ParseIP("203.0.113.10")) {
		t.Fatalf("unexpected fallback answer: %s", answer.A)
	}
}

func TestExchangeDoesNotFallbackOnTerminalDNSResponse(t *testing.T) {
	t.Parallel()

	var fallbackQueries atomic.Int32
	primaryAddr := startUDPTestDNSServer(t, dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		resp := new(dns.Msg)
		resp.SetRcode(req, dns.RcodeNameError)
		_ = w.WriteMsg(resp)
	}))
	fallbackAddr := startUDPTestDNSServer(t, dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		fallbackQueries.Add(1)
		resp := new(dns.Msg)
		resp.SetReply(req)
		_ = w.WriteMsg(resp)
	}))

	r := newTestRouter(t, nil, "", "", "", nil)
	r.dns.upstreamAddrs = []string{primaryAddr, fallbackAddr}
	r.dns.upstreamIndex = 0

	req := new(dns.Msg)
	req.SetQuestion("example.com.", dns.TypeA)
	resp, err := r.Exchange(req)
	if err != nil {
		t.Fatalf("exchange failed: %v", err)
	}
	if resp.Rcode != dns.RcodeNameError {
		t.Fatalf("expected NXDOMAIN response, got rcode %d", resp.Rcode)
	}
	if r.dns.upstreamIndex != 0 {
		t.Fatalf("expected preferred upstream to remain selected, got %d", r.dns.upstreamIndex)
	}
	if fallbackQueries.Load() != 0 {
		t.Fatalf("expected fallback to remain unused, got %d queries", fallbackQueries.Load())
	}
}

func TestServeDNSReturnsRetryableResponseWhenUpstreamsExhausted(t *testing.T) {
	t.Parallel()

	upstreamAddr := startUDPTestDNSServer(t, dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		resp := new(dns.Msg)
		resp.SetRcode(req, dns.RcodeRefused)
		_ = w.WriteMsg(resp)
	}))

	r := newTestRouter(t, nil, "", "", "", nil)
	r.dns.upstreamAddrs = []string{upstreamAddr}
	r.dns.upstreamIndex = 0

	req := new(dns.Msg)
	req.SetQuestion("example.com.", dns.TypeA)
	writer := &mockDNSWriter{localAddr: &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 53}}

	r.ServeDNS(writer, req)

	if writer.msg == nil {
		t.Fatal("expected response message")
	}
	if writer.msg.Rcode != dns.RcodeRefused {
		t.Fatalf("expected upstream REFUSED response, got rcode %d", writer.msg.Rcode)
	}
}

func TestExchangeDoesNotPromoteRetryableProbeResponse(t *testing.T) {
	t.Parallel()

	var primaryQueries atomic.Int32
	var fallbackQueries atomic.Int32
	primaryAddr := startUDPTestDNSServer(t, dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		primaryQueries.Add(1)
		resp := new(dns.Msg)
		resp.SetRcode(req, dns.RcodeRefused)
		_ = w.WriteMsg(resp)
	}))
	fallbackAddr := startUDPTestDNSServer(t, dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		fallbackQueries.Add(1)
		resp := new(dns.Msg)
		resp.SetReply(req)
		resp.Answer = []dns.RR{&dns.A{
			Hdr: dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 20},
			A:   net.ParseIP("203.0.113.20"),
		}}
		_ = w.WriteMsg(resp)
	}))

	r := newTestRouter(t, nil, "", "", "", nil)
	r.dns.upstreamAddrs = []string{primaryAddr, fallbackAddr}
	r.dns.upstreamIndex = 1
	r.dns.retryAt = time.Now().Add(-time.Second)

	req := new(dns.Msg)
	req.SetQuestion("example.com.", dns.TypeA)
	if _, err := r.Exchange(req); err != nil {
		t.Fatalf("exchange failed: %v", err)
	}
	if r.dns.upstreamIndex != 1 {
		t.Fatalf("expected retryable probe response to keep fallback index 1, got %d", r.dns.upstreamIndex)
	}
	if primaryQueries.Load() != 1 {
		t.Fatalf("expected one preferred probe, got %d", primaryQueries.Load())
	}
	if fallbackQueries.Load() != 1 {
		t.Fatalf("expected one fallback query, got %d", fallbackQueries.Load())
	}
}

func TestAddCountryCIDRsRejectsInvalidEntries(t *testing.T) {
	t.Parallel()

	r := newTestRouter(t, nil, "", "223.5.5.5", "", nil)
	if err := r.AddCountryCIDRs("invalid-cidr"); err == nil {
		t.Fatal("expected invalid CIDR to fail")
	}

	if len(r.country.cidrs) != 0 {
		t.Fatalf("expected invalid CIDR to be rejected, got %d entries", len(r.country.cidrs))
	}
	if r.localSite(context.Background(), "127.0.0.1") {
		t.Fatal("unexpected localSite match without valid CIDRs or MMDB")
	}
}

func TestNewRouterSkipsEmptyCountryMMDB(t *testing.T) {
	t.Parallel()

	for _, mmdbFile := range []string{"", "   "} {
		r := newTestRouter(t, nil, "", "223.5.5.5", mmdbFile, nil)
		if r.country.Reader != nil {
			t.Fatalf("expected empty MMDB %q to disable GeoIP lookup", mmdbFile)
		}
	}
}

func TestNewRouterRejectsInvalidCountryMMDB(t *testing.T) {
	t.Parallel()

	if _, err := NewRouter(nil, "", "223.5.5.5", "/path/to/missing.mmdb", nil); err == nil {
		t.Fatal("expected missing MMDB to fail")
	}
}

func TestIsAccessChecksRequestedPortOnly(t *testing.T) {
	t.Parallel()

	r := newTestRouter(t, nil, "", "223.5.5.5", "", nil)
	r.accessCache = newAccessCache(accessCacheTTL, func(key string) (bool, error) {
		scheme, _, err := accessProbeTarget(key)
		if err != nil {
			return false, err
		}
		return scheme == "https", nil
	})
	if !r.isAccess("example.com", 443) {
		t.Fatal("expected HTTPS probe to succeed")
	}
	if r.isAccess("example.com", 80) {
		t.Fatal("expected HTTP probe to fail")
	}
}

func TestServeDNSForProxyRuleSuppressesNonAddressQuestion(t *testing.T) {
	t.Parallel()

	udpConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer udpConn.Close()

	var upstreamQueries atomic.Int32
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		upstreamQueries.Add(1)
		resp := new(dns.Msg)
		resp.SetReply(req)
		resp.Answer = []dns.RR{&dns.TXT{
			Hdr: dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 20},
			Txt: []string{"forwarded"},
		}}
		_ = w.WriteMsg(resp)
	})

	server := &dns.Server{PacketConn: udpConn, Handler: handler}
	go func() { _ = server.ActivateAndServe() }()
	t.Cleanup(func() {
		_ = server.Shutdown()
	})

	r := newTestRouter(t, []string{"127.0.0.2"}, "", "223.5.5.5", "", nil)
	r.BlockRule = NewRuleSet()
	r.DirectRule = NewRuleSet()
	r.ProxyRule = NewRuleSet("example.com.")
	r.dns.upstreamAddrs = []string{udpConn.LocalAddr().String()}
	r.dns.upstreamIndex = 0

	req := new(dns.Msg)
	req.SetQuestion("example.com.", dns.TypeTXT)
	writer := &mockDNSWriter{localAddr: &net.UDPAddr{IP: net.ParseIP("127.0.0.2"), Port: 53}}

	r.ServeDNS(writer, req)

	if writer.msg == nil {
		t.Fatal("expected response message")
	}
	if writer.msg.Rcode != dns.RcodeSuccess {
		t.Fatalf("expected successful response, got rcode %d", writer.msg.Rcode)
	}
	if len(writer.msg.Answer) != 0 {
		t.Fatalf("expected NODATA response, got %d answers", len(writer.msg.Answer))
	}
	if upstreamQueries.Load() != 0 {
		t.Fatalf("expected TXT query to be suppressed locally, got %d upstream queries", upstreamQueries.Load())
	}
}

func TestServeDNSForProxyRuleSuppressesHTTPSQuestion(t *testing.T) {
	t.Parallel()

	udpConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer udpConn.Close()

	var upstreamQueries atomic.Int32
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		upstreamQueries.Add(1)
		resp := new(dns.Msg)
		resp.SetReply(req)
		_ = w.WriteMsg(resp)
	})

	server := &dns.Server{PacketConn: udpConn, Handler: handler}
	go func() { _ = server.ActivateAndServe() }()
	t.Cleanup(func() {
		_ = server.Shutdown()
	})

	r := newTestRouter(t, []string{"127.0.0.2"}, "", "223.5.5.5", "", nil)
	r.BlockRule = NewRuleSet()
	r.DirectRule = NewRuleSet()
	r.ProxyRule = NewRuleSet("example.com.")
	r.dns.upstreamAddrs = []string{udpConn.LocalAddr().String()}
	r.dns.upstreamIndex = 0

	req := new(dns.Msg)
	req.SetQuestion("example.com.", dns.TypeHTTPS)
	writer := &mockDNSWriter{localAddr: &net.UDPAddr{IP: net.ParseIP("127.0.0.2"), Port: 53}}

	r.ServeDNS(writer, req)

	if writer.msg == nil {
		t.Fatal("expected response message")
	}
	if writer.msg.Rcode != dns.RcodeSuccess {
		t.Fatalf("expected successful response, got rcode %d", writer.msg.Rcode)
	}
	if len(writer.msg.Answer) != 0 {
		t.Fatalf("expected NODATA response, got %d answers", len(writer.msg.Answer))
	}
	if upstreamQueries.Load() != 0 {
		t.Fatalf("expected HTTPS query to be suppressed locally, got %d upstream queries", upstreamQueries.Load())
	}
}

func TestServeDNSForProxyRuleSuppressesSRVQuestion(t *testing.T) {
	t.Parallel()

	udpConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer udpConn.Close()

	var upstreamQueries atomic.Int32
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		upstreamQueries.Add(1)
		resp := new(dns.Msg)
		resp.SetReply(req)
		resp.Answer = []dns.RR{&dns.SRV{
			Hdr:      dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeSRV, Class: dns.ClassINET, Ttl: 20},
			Priority: 10,
			Weight:   5,
			Port:     5222,
			Target:   "srv.example.com.",
		}}
		_ = w.WriteMsg(resp)
	})

	server := &dns.Server{PacketConn: udpConn, Handler: handler}
	go func() { _ = server.ActivateAndServe() }()
	t.Cleanup(func() {
		_ = server.Shutdown()
	})

	r := newTestRouter(t, []string{"127.0.0.2"}, "", "223.5.5.5", "", nil)
	r.BlockRule = NewRuleSet()
	r.DirectRule = NewRuleSet()
	r.ProxyRule = NewRuleSet("example.com.")
	r.dns.upstreamAddrs = []string{udpConn.LocalAddr().String()}
	r.dns.upstreamIndex = 0

	req := new(dns.Msg)
	req.SetQuestion("_xmpp-client._tcp.example.com.", dns.TypeSRV)
	writer := &mockDNSWriter{localAddr: &net.UDPAddr{IP: net.ParseIP("127.0.0.2"), Port: 53}}

	r.ServeDNS(writer, req)

	if writer.msg == nil {
		t.Fatal("expected response message")
	}
	if writer.msg.Rcode != dns.RcodeSuccess {
		t.Fatalf("expected successful response, got rcode %d", writer.msg.Rcode)
	}
	if len(writer.msg.Answer) != 0 {
		t.Fatalf("expected NODATA response, got %d answers", len(writer.msg.Answer))
	}
	if upstreamQueries.Load() != 0 {
		t.Fatalf("expected SRV query to be suppressed locally, got %d upstream queries", upstreamQueries.Load())
	}
}

func TestServeDNSDoesNotStripServicePrefixForNonServiceQuestion(t *testing.T) {
	t.Parallel()

	udpConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer udpConn.Close()

	var upstreamQueries atomic.Int32
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		upstreamQueries.Add(1)
		resp := new(dns.Msg)
		resp.SetReply(req)
		resp.Answer = []dns.RR{&dns.TXT{
			Hdr: dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 20},
			Txt: []string{"forwarded"},
		}}
		_ = w.WriteMsg(resp)
	})

	server := &dns.Server{PacketConn: udpConn, Handler: handler}
	go func() { _ = server.ActivateAndServe() }()
	t.Cleanup(func() {
		_ = server.Shutdown()
	})

	r := newTestRouter(t, []string{"127.0.0.2"}, "", "223.5.5.5", "", nil)
	r.BlockRule = NewRuleSet()
	r.DirectRule = NewRuleSet()
	r.ProxyRule = NewRuleSet("example.com.")
	r.dns.upstreamAddrs = []string{udpConn.LocalAddr().String()}
	r.dns.upstreamIndex = 0

	req := new(dns.Msg)
	req.SetQuestion("_xmpp-client._tcp.example.com.", dns.TypeTXT)
	writer := &mockDNSWriter{localAddr: &net.UDPAddr{IP: net.ParseIP("127.0.0.2"), Port: 53}}

	r.ServeDNS(writer, req)

	if writer.msg == nil {
		t.Fatal("expected response message")
	}
	if writer.msg.Rcode != dns.RcodeSuccess {
		t.Fatalf("expected successful response, got rcode %d", writer.msg.Rcode)
	}
	if len(writer.msg.Answer) != 1 {
		t.Fatalf("expected forwarded TXT answer, got %d answers", len(writer.msg.Answer))
	}
	if upstreamQueries.Load() != 1 {
		t.Fatalf("expected TXT query to be forwarded once, got %d upstream queries", upstreamQueries.Load())
	}
}

func TestServeDNSForProxyRuleReturnsNoDataWhenAddressFamilyUnavailable(t *testing.T) {
	t.Parallel()

	r := newTestRouter(t, []string{"127.0.0.1"}, "", "223.5.5.5", "", nil)
	r.BlockRule = NewRuleSet()
	r.DirectRule = NewRuleSet()
	r.ProxyRule = NewRuleSet("example.com.")

	req := new(dns.Msg)
	req.SetQuestion("example.com.", dns.TypeAAAA)
	writer := &mockDNSWriter{localAddr: &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 53}}

	r.ServeDNS(writer, req)

	if writer.msg == nil {
		t.Fatal("expected response message")
	}
	if writer.msg.Rcode != dns.RcodeSuccess {
		t.Fatalf("expected successful response, got rcode %d", writer.msg.Rcode)
	}
	if len(writer.msg.Answer) != 0 {
		t.Fatalf("expected NODATA response, got %d answers", len(writer.msg.Answer))
	}
}

func TestBuildUpstreamAddrsIsPerRouter(t *testing.T) {
	t.Parallel()

	r1 := newTestRouter(t, []string{"127.0.0.1"}, "1.1.1.1", "9.9.9.9", "", nil)
	r2 := newTestRouter(t, []string{"127.0.0.2"}, "8.8.8.8", "4.4.4.4", "", nil)

	addrs1, err := r1.buildUpstreamAddrs()
	if err != nil {
		t.Fatalf("buildUpstreamAddrs r1 failed: %v", err)
	}
	addrs2, err := r2.buildUpstreamAddrs()
	if err != nil {
		t.Fatalf("buildUpstreamAddrs r2 failed: %v", err)
	}

	if strings.Join(addrs1, ",") == strings.Join(addrs2, ",") {
		t.Fatalf("expected router-specific upstreams, got %v and %v", addrs1, addrs2)
	}
}

func TestCurrentUpstreamStateSchedulesPreferredProbeAfterRetryInterval(t *testing.T) {
	t.Parallel()

	r := newTestRouter(t, []string{"127.0.0.1"}, "1.1.1.1", "9.9.9.9", "", nil)
	r.dns.upstreamAddrs = []string{"1.1.1.1:53", "9.9.9.9:53"}
	r.dns.upstreamIndex = 1
	r.dns.retryAt = time.Now().Add(-time.Second)

	_, index, shouldProbe, err := r.currentUpstreamState(time.Now())
	if err != nil {
		t.Fatalf("currentUpstreamState failed: %v", err)
	}
	if index != 1 {
		t.Fatalf("expected current degraded upstream index 1, got %d", index)
	}
	if !shouldProbe {
		t.Fatal("expected preferred upstream probe to be scheduled")
	}
}

func TestCurrentUpstreamStateAllowsOnlyOnePreferredProbe(t *testing.T) {
	t.Parallel()

	now := time.Now()
	r := newTestRouter(t, []string{"127.0.0.1"}, "1.1.1.1", "9.9.9.9", "", nil)
	r.dns.upstreamAddrs = []string{"1.1.1.1:53", "9.9.9.9:53"}
	r.dns.upstreamIndex = 1
	r.dns.retryAt = now.Add(-time.Second)

	_, _, shouldProbe, err := r.currentUpstreamState(now)
	if err != nil {
		t.Fatalf("currentUpstreamState failed: %v", err)
	}
	if !shouldProbe {
		t.Fatal("expected first caller to probe preferred upstream")
	}

	_, _, shouldProbe, err = r.currentUpstreamState(now)
	if err != nil {
		t.Fatalf("currentUpstreamState second call failed: %v", err)
	}
	if shouldProbe {
		t.Fatal("expected second caller to reuse in-flight probe state")
	}
}

func TestDegradeUpstreamMovesTowardFallback(t *testing.T) {
	t.Parallel()

	r := newTestRouter(t, []string{"127.0.0.1"}, "1.1.1.1", "9.9.9.9", "", nil)
	r.dns.upstreamAddrs = []string{"1.1.1.1:53", "9.9.9.9:53"}
	r.dns.upstreamIndex = 0

	index, switched := r.degradeUpstream(0)
	if !switched {
		t.Fatal("expected upstream switch")
	}
	if index != 1 {
		t.Fatalf("expected fallback index 1, got %d", index)
	}
	if r.dns.retryAt.IsZero() {
		t.Fatal("expected retry deadline to be set")
	}
}

func TestPromoteUpstreamRestoresPreferredAndClearsRetry(t *testing.T) {
	t.Parallel()

	r := newTestRouter(t, []string{"127.0.0.1"}, "1.1.1.1", "9.9.9.9", "", nil)
	r.dns.upstreamAddrs = []string{"1.1.1.1:53", "9.9.9.9:53"}
	r.dns.upstreamIndex = 1
	r.dns.retryAt = time.Now().Add(time.Minute)

	r.promoteUpstream()

	if r.dns.upstreamIndex != 0 {
		t.Fatalf("expected preferred index 0, got %d", r.dns.upstreamIndex)
	}
	if !r.dns.retryAt.IsZero() {
		t.Fatal("expected retry deadline to be cleared")
	}
}

func TestCurrentUpstreamStateRefreshesDHCPUpstreams(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	r := newTestRouter(t, []string{"127.0.0.1"}, "", "9.9.9.9", "", nil)
	r.dns.getDNSServer = func() ([]string, error) {
		if calls.Add(1) == 1 {
			return []string{"1.1.1.1"}, nil
		}
		return []string{"8.8.8.8"}, nil
	}

	now := time.Now()
	addrs, index, shouldProbe, err := r.currentUpstreamState(now)
	if err != nil {
		t.Fatalf("initial currentUpstreamState failed: %v", err)
	}
	if index != 0 || shouldProbe {
		t.Fatalf("unexpected initial state: index=%d shouldProbe=%v", index, shouldProbe)
	}
	if strings.Join(addrs, ",") != "1.1.1.1:53,9.9.9.9:53" {
		t.Fatalf("unexpected initial upstreams: %v", addrs)
	}

	addrs, index, shouldProbe, err = r.currentUpstreamState(now.Add(dnsRefreshTTL + time.Second))
	if err != nil {
		t.Fatalf("refreshed currentUpstreamState failed: %v", err)
	}
	if index != 0 || shouldProbe {
		t.Fatalf("unexpected refreshed state: index=%d shouldProbe=%v", index, shouldProbe)
	}
	if strings.Join(addrs, ",") != "8.8.8.8:53,9.9.9.9:53" {
		t.Fatalf("unexpected refreshed upstreams: %v", addrs)
	}
}

func TestCurrentUpstreamStateBacksOffInitialDHCPFailure(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	r := newTestRouter(t, []string{"127.0.0.1"}, "", "", "", nil)
	r.dns.getDNSServer = func() ([]string, error) {
		calls.Add(1)
		return nil, errors.New("dhcp unavailable")
	}

	now := time.Now()
	if _, _, _, err := r.currentUpstreamState(now); err == nil {
		t.Fatal("expected initial DHCP failure")
	}
	if calls.Load() != 1 {
		t.Fatalf("expected one DHCP call, got %d", calls.Load())
	}

	if _, _, _, err := r.currentUpstreamState(now.Add(time.Second)); err == nil {
		t.Fatal("expected cached DHCP failure during backoff")
	}
	if calls.Load() != 1 {
		t.Fatalf("expected no extra DHCP call during backoff, got %d", calls.Load())
	}
}

func TestCurrentUpstreamStateRefreshDoesNotBlockConcurrentReaders(t *testing.T) {
	t.Parallel()

	startRefresh := make(chan struct{})
	releaseRefresh := make(chan struct{})
	var calls atomic.Int32

	r := newTestRouter(t, []string{"127.0.0.1"}, "", "9.9.9.9", "", nil)
	r.dns.upstreamAddrs = []string{"1.1.1.1:53", "9.9.9.9:53"}
	r.dns.upstreamIndex = 0
	r.dns.refreshAt = time.Now().Add(-time.Second)
	r.dns.getDNSServer = func() ([]string, error) {
		if calls.Add(1) == 1 {
			close(startRefresh)
			<-releaseRefresh
		}
		return []string{"8.8.8.8"}, nil
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _, _, _ = r.currentUpstreamState(time.Now())
	}()

	<-startRefresh

	done := make(chan struct{})
	go func() {
		defer close(done)
		addrs, index, shouldProbe, err := r.currentUpstreamState(time.Now())
		if err != nil {
			t.Errorf("concurrent currentUpstreamState failed: %v", err)
			return
		}
		if index != 0 || shouldProbe {
			t.Errorf("unexpected concurrent state: index=%d shouldProbe=%v", index, shouldProbe)
			return
		}
		if strings.Join(addrs, ",") != "1.1.1.1:53,9.9.9.9:53" {
			t.Errorf("unexpected concurrent upstreams: %v", addrs)
		}
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("concurrent reader blocked on refresh lock")
	}

	close(releaseRefresh)
	wg.Wait()
}

func TestCurrentUpstreamStateUsesFallbackWhileInitialRefreshInFlight(t *testing.T) {
	t.Parallel()

	startRefresh := make(chan struct{})
	releaseRefresh := make(chan struct{})

	r := newTestRouter(t, []string{"127.0.0.1"}, "", "9.9.9.9", "", nil)
	r.dns.getDNSServer = func() ([]string, error) {
		close(startRefresh)
		<-releaseRefresh
		return []string{"1.1.1.1"}, nil
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _, _, _ = r.currentUpstreamState(time.Now())
	}()

	<-startRefresh

	done := make(chan struct{})
	go func() {
		defer close(done)
		addrs, index, shouldProbe, err := r.currentUpstreamState(time.Now())
		if err != nil {
			t.Errorf("currentUpstreamState during initial refresh failed: %v", err)
			return
		}
		if index != 0 || shouldProbe {
			t.Errorf("unexpected fallback state: index=%d shouldProbe=%v", index, shouldProbe)
			return
		}
		if strings.Join(addrs, ",") != "9.9.9.9:53" {
			t.Errorf("expected fallback-only upstreams, got %v", addrs)
		}
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("concurrent caller blocked instead of using fallback")
	}

	close(releaseRefresh)
	wg.Wait()
}

func TestCurrentUpstreamStatePreservesProbeWhenRefreshIsDue(t *testing.T) {
	t.Parallel()

	now := time.Now()
	r := newTestRouter(t, []string{"127.0.0.1"}, "", "9.9.9.9", "", nil)
	r.dns.upstreamAddrs = []string{"1.1.1.1:53", "9.9.9.9:53"}
	r.dns.upstreamIndex = 1
	r.dns.retryAt = now.Add(-time.Second)
	r.dns.refreshAt = now.Add(-time.Second)
	r.dns.getDNSServer = func() ([]string, error) {
		return []string{"8.8.8.8"}, nil
	}

	addrs, index, shouldProbe, err := r.currentUpstreamState(now)
	if err != nil {
		t.Fatalf("currentUpstreamState with refresh failed: %v", err)
	}
	if !shouldProbe {
		t.Fatal("expected refresh to preserve due preferred probe")
	}
	if index != 1 {
		t.Fatalf("expected degraded upstream to be preserved across refresh, got %d", index)
	}
	if strings.Join(addrs, ",") != "8.8.8.8:53,9.9.9.9:53" {
		t.Fatalf("unexpected refreshed upstreams: %v", addrs)
	}
	if !r.dns.probeInFlight {
		t.Fatal("expected probe to be marked in-flight after refresh")
	}
	if !r.dns.retryAt.After(now) {
		t.Fatalf("expected retryAt to be pushed forward after scheduling probe, got %v", r.dns.retryAt)
	}
}

func TestServeDNSReturnsNXDomainForBlockedDomain(t *testing.T) {
	t.Parallel()

	r := newTestRouter(t, []string{"127.0.0.1"}, "", "223.5.5.5", "", nil)
	r.BlockRule = NewRuleSet("example.com.")
	r.DirectRule = NewRuleSet()
	r.ProxyRule = NewRuleSet()

	req := new(dns.Msg)
	req.SetQuestion("example.com.", dns.TypeA)

	writer := &mockDNSWriter{localAddr: &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 53}}
	r.ServeDNS(writer, req)

	if writer.msg == nil {
		t.Fatal("expected response message")
	}
	if writer.msg.Rcode != dns.RcodeNameError {
		t.Fatalf("expected NXDOMAIN, got %d", writer.msg.Rcode)
	}
}

func TestServeDNSRejectsUnsupportedQuestionClass(t *testing.T) {
	t.Parallel()

	r := newTestRouter(t, []string{"127.0.0.1"}, "", "223.5.5.5", "", nil)
	req := new(dns.Msg)
	req.SetQuestion("example.com.", dns.TypeA)
	req.Question[0].Qclass = dns.ClassCHAOS

	writer := &mockDNSWriter{localAddr: &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 53}}
	r.ServeDNS(writer, req)

	if writer.msg == nil {
		t.Fatal("expected response message")
	}
	if writer.msg.Rcode != dns.RcodeNotImplemented {
		t.Fatalf("expected NOTIMP, got %d", writer.msg.Rcode)
	}
}

func TestServeDNSRejectsMultipleQuestions(t *testing.T) {
	t.Parallel()

	r := newTestRouter(t, []string{"127.0.0.1"}, "", "223.5.5.5", "", nil)
	req := new(dns.Msg)
	req.SetQuestion("example.com.", dns.TypeA)
	req.Question = append(req.Question, dns.Question{Name: "example.org.", Qtype: dns.TypeA, Qclass: dns.ClassINET})

	writer := &mockDNSWriter{localAddr: &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 53}}
	r.ServeDNS(writer, req)

	if writer.msg == nil {
		t.Fatal("expected response message")
	}
	if writer.msg.Rcode != dns.RcodeFormatError {
		t.Fatalf("expected format error, got %d", writer.msg.Rcode)
	}
}

func TestExchangeWithRetryFallsBackToTCPOnTruncatedUDP(t *testing.T) {
	t.Parallel()

	udpConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer udpConn.Close()

	tcpLn, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strings.Split(udpConn.LocalAddr().String(), ":")[1]))
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	defer tcpLn.Close()

	var tcpQueries atomic.Int32
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		resp := new(dns.Msg)
		resp.SetReply(req)
		if _, ok := w.RemoteAddr().(*net.TCPAddr); ok {
			tcpQueries.Add(1)
			resp.Answer = []dns.RR{&dns.A{
				Hdr: dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 20},
				A:   net.ParseIP("127.0.0.1"),
			}}
		} else {
			resp.Truncated = true
		}
		_ = w.WriteMsg(resp)
	})

	udpServer := &dns.Server{PacketConn: udpConn, Handler: handler}
	tcpServer := &dns.Server{Listener: tcpLn, Handler: handler}
	go func() { _ = udpServer.ActivateAndServe() }()
	go func() { _ = tcpServer.ActivateAndServe() }()
	t.Cleanup(func() {
		_ = udpServer.Shutdown()
		_ = tcpServer.Shutdown()
	})

	r := newTestRouter(t, nil, "", "223.5.5.5", "", nil)
	req := new(dns.Msg)
	req.SetQuestion("example.com.", dns.TypeA)

	resp, err := r.exchangeWithRetry(req, udpConn.LocalAddr().String())
	if err != nil {
		t.Fatalf("exchangeWithRetry failed: %v", err)
	}
	if len(resp.Answer) != 1 {
		t.Fatalf("expected tcp answer, got %d answers", len(resp.Answer))
	}
	if tcpQueries.Load() == 0 {
		t.Fatal("expected TCP fallback query")
	}
}

type mockDNSWriter struct {
	msg       *dns.Msg
	localAddr net.Addr
}

func (w *mockDNSWriter) LocalAddr() net.Addr         { return w.localAddr }
func (w *mockDNSWriter) RemoteAddr() net.Addr        { return &net.UDPAddr{} }
func (w *mockDNSWriter) WriteMsg(msg *dns.Msg) error { w.msg = msg; return nil }
func (w *mockDNSWriter) Write([]byte) (int, error)   { return 0, nil }
func (w *mockDNSWriter) Close() error                { return nil }
func (w *mockDNSWriter) TsigStatus() error           { return nil }
func (w *mockDNSWriter) TsigTimersOnly(bool)         {}
func (w *mockDNSWriter) Hijack()                     {}

func startUDPTestDNSServer(t *testing.T, handler dns.Handler) string {
	t.Helper()

	udpConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}

	server := &dns.Server{PacketConn: udpConn, Handler: handler}
	go func() { _ = server.ActivateAndServe() }()
	t.Cleanup(func() {
		_ = server.Shutdown()
		_ = udpConn.Close()
	})
	return udpConn.LocalAddr().String()
}
