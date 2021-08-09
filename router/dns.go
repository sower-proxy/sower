package router

import (
	"net"
	"time"

	"github.com/miekg/dns"
	"github.com/wweir/deferlog"
)

func (r *Router) ServeDNS(w dns.ResponseWriter, req *dns.Msg) {
	// https://stackoverflow.com/questions/4082081/requesting-a-and-aaaa-records-in-single-dns-query/4083071#4083071
	if len(req.Question) == 0 {
		_ = w.WriteMsg(r.dnsFail(req, dns.RcodeFormatError))
		return
	}

	domain := req.Question[0].Name
	switch {
	case r.blockRule.Match(domain):
		_ = w.WriteMsg(r.dnsFail(req, dns.RcodeNameError))
		return

	case r.directRule.Match(domain):

	case r.proxyRule.Match(domain):
		_ = w.WriteMsg(r.dnsProxyA(domain, r.dns.serveIP, req))
		return
	}

	c := &dnsCache{Router: r, Req: req}
	if err := r.dns.cache.Remember(c, req.Question[0].String()); err != nil {
		_ = w.WriteMsg(r.dnsFail(req, dns.RcodeServerFailure))
		return
	}

	c.Resp.SetReply(req)
	c.Resp.Compress = true
	_ = w.WriteMsg(c.Resp)
}

func (r *Router) dnsFail(req *dns.Msg, rcode int) *dns.Msg {
	m := new(dns.Msg)
	m.SetRcode(req, dns.RcodeServerFailure)
	return m
}

func (r *Router) dnsProxyA(domain string, localIP net.IP, req *dns.Msg) *dns.Msg {
	m := new(dns.Msg)
	m.SetReply(req)

	if localIP.To4() != nil {
		m.Answer = []dns.RR{&dns.A{
			Hdr: dns.RR_Header{Name: domain, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 20},
			A:   localIP,
		}}

	} else {
		m.Answer = []dns.RR{&dns.AAAA{
			Hdr:  dns.RR_Header{Name: domain, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 20},
			AAAA: localIP,
		}}
	}
	return m
}

type dnsCache struct {
	*Router
	Req, Resp *dns.Msg
}

func (r *dnsCache) Fulfill(question string) (err error) {
	conn := <-r.dns.connCh

	var rtt time.Duration
	r.Resp, rtt, err = r.dns.ExchangeWithConn(r.Req, conn)
	if err != nil {
		r.Resp, rtt, err = r.dns.ExchangeWithConn(r.Req, conn)
	}
	deferlog.Std.DebugWarn(err).
		Dur("rtt", rtt).
		Str("question", question).
		Msg("exchange dns record")

	select {
	case r.dns.connCh <- conn:
	default:
		conn.Close()
	}
	return err
}
