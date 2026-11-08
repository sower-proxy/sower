package main

import (
	"net"
	"testing"

	"github.com/miekg/dns"
)

// recorder stands in for the listener's writer and keeps the message the
// handler produced.
type recorder struct {
	msg *dns.Msg
}

func (r *recorder) LocalAddr() net.Addr         { return &net.UDPAddr{IP: net.IPv4zero, Port: 53} }
func (r *recorder) RemoteAddr() net.Addr        { return &net.UDPAddr{IP: net.IPv4zero, Port: 5353} }
func (r *recorder) WriteMsg(m *dns.Msg) error   { r.msg = m; return nil }
func (r *recorder) Write(b []byte) (int, error) { return len(b), nil }
func (r *recorder) Close() error                { return nil }
func (r *recorder) TsigStatus() error           { return nil }
func (r *recorder) TsigTimersOnly(bool)         {}
func (r *recorder) Hijack()                     {}

// wideAnswerHandler answers with enough A records that name compression cannot
// bring the response under 512 bytes, which is what a client without EDNS
// advertises.
func wideAnswerHandler(t *testing.T, records int) dns.Handler {
	t.Helper()
	return dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(req)
		m.RecursionAvailable = true
		for i := 0; i < records; i++ {
			m.Answer = append(m.Answer, &dns.A{
				Hdr: dns.RR_Header{Name: req.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
				A:   net.IPv4(10, 0, byte(i/256), byte(i%256)),
			})
		}
		_ = w.WriteMsg(m)
	})
}

func queryWithEDNS(t *testing.T, size uint16) *dns.Msg {
	t.Helper()
	req := new(dns.Msg)
	req.SetQuestion("api.alipan.com.", dns.TypeA)
	if size > 0 {
		req.SetEdns0(size, false)
	}
	return req
}

func TestUDPTruncatingHandlerFitsPlainClient(t *testing.T) {
	const records = 40
	handler := udpTruncatingHandler{next: wideAnswerHandler(t, records)}

	req := queryWithEDNS(t, 0)
	probe := &recorder{}
	handler.next.ServeDNS(probe, req)
	if probe.msg.Len() <= dns.MinMsgSize {
		t.Fatalf("test answer is only %d bytes; it would never need truncation", probe.msg.Len())
	}

	w := &recorder{}
	handler.ServeDNS(w, req)
	if w.msg == nil {
		t.Fatal("handler wrote no message")
	}
	if !w.msg.Truncated {
		t.Fatalf("response of %d bytes to a 512-byte client is not truncated", w.msg.Len())
	}
	if got := w.msg.Len(); got > dns.MinMsgSize {
		t.Fatalf("truncated response is still %d bytes, want <= %d", got, dns.MinMsgSize)
	}
	if len(w.msg.Answer) >= records {
		t.Fatalf("truncated response kept all %d answers", len(w.msg.Answer))
	}
}

func TestUDPTruncatingHandlerKeepsLargeEDNSAnswer(t *testing.T) {
	const records = 40
	handler := udpTruncatingHandler{next: wideAnswerHandler(t, records)}

	w := &recorder{}
	handler.ServeDNS(w, queryWithEDNS(t, 4096))
	if w.msg == nil {
		t.Fatal("handler wrote no message")
	}
	if w.msg.Truncated {
		t.Fatalf("full %d-byte answer truncated for a 4096-byte client", w.msg.Len())
	}
	if len(w.msg.Answer) != records {
		t.Fatalf("answer records = %d, want %d", len(w.msg.Answer), records)
	}
}

func TestRequestUDPSize(t *testing.T) {
	if got := requestUDPSize(queryWithEDNS(t, 0)); got != dns.MinMsgSize {
		t.Fatalf("plain query size = %d, want %d", got, dns.MinMsgSize)
	}
	if got := requestUDPSize(queryWithEDNS(t, 1232)); got != 1232 {
		t.Fatalf("EDNS query size = %d, want 1232", got)
	}
	// RFC 6891: an advertised size below 512 is treated as 512.
	if got := requestUDPSize(queryWithEDNS(t, 256)); got != dns.MinMsgSize {
		t.Fatalf("small EDNS size = %d, want %d", got, dns.MinMsgSize)
	}
}
