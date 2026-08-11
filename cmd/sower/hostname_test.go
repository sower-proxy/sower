package main

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// startPTRTestDNSServer serves PTR answers on 127.0.0.1:0 with the given
// mapping from reverse name to hostname.
func startPTRTestDNSServer(t *testing.T, ptr map[string]string) string {
	t.Helper()

	udpConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	handler := dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(req)
		name := req.Question[0].Name
		if host, ok := ptr[name]; ok {
			m.Answer = append(m.Answer, &dns.PTR{
				Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypePTR, Class: dns.ClassINET, Ttl: 60},
				Ptr: host,
			})
		}
		_ = w.WriteMsg(m)
	})
	server := &dns.Server{PacketConn: udpConn, Handler: handler}
	go func() { _ = server.ActivateAndServe() }()
	t.Cleanup(func() {
		_ = server.Shutdown()
		_ = udpConn.Close()
	})
	return udpConn.LocalAddr().String()
}

func TestDNSHostnameResolver(t *testing.T) {
	t.Parallel()

	// Point the resolver at the test server by extracting its port; the
	// resolver joins "53" itself, so build an address on a fixed base.
	addr := startPTRTestDNSServer(t, map[string]string{
		"2.1.168.192.in-addr.arpa.": "phone.lan.",
		"1.1.168.192.in-addr.arpa.": "router.lan.",
	})

	// Build the resolver directly on the test server address so the PTR
	// query lands on the fake upstream regardless of its port.
	r := &dnsHostnameResolver{addr: addr, client: &dns.Client{Net: "udp", Timeout: 2 * time.Second}}

	ctx := context.Background()
	if got := r.Hostname(ctx, "192.168.1.2"); got != "phone.lan" {
		t.Fatalf("reverse 192.168.1.2: got %q", got)
	}
	if got := r.Hostname(ctx, "192.168.1.1"); got != "router.lan" {
		t.Fatalf("reverse 192.168.1.1: got %q", got)
	}

	// No PTR data: empty hostname.
	if got := r.Hostname(ctx, "8.8.8.8"); got != "" {
		t.Fatalf("reverse 8.8.8.8: got %q", got)
	}

	// Invalid IP: empty without querying.
	if got := r.Hostname(ctx, "not-an-ip"); got != "" {
		t.Fatalf("reverse invalid ip: got %q", got)
	}

	// Canceled context: empty.
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if got := r.Hostname(canceled, "192.168.1.2"); got != "" {
		t.Fatalf("reverse with canceled ctx: got %q", got)
	}
}
