package router

import (
	"net"

	"github.com/miekg/dns"
	"github.com/sower-proxy/deferlog/log"
)

func (r *Router) ServeDNS(w dns.ResponseWriter, req *dns.Msg) {
	// https://stackoverflow.com/questions/4082081/requesting-a-and-aaaa-records-in-single-dns-query/4083071#4083071
	if len(req.Question) == 0 {
		_ = w.WriteMsg(r.dnsFail(req, dns.RcodeFormatError))
		return
	}

	domain := req.Question[0].Name
	// 1. rule_based( block > direct > proxy )
	switch {
	case r.blockRule.Match(domain):
		_ = w.WriteMsg(r.dnsFail(req, dns.RcodeNameError))
		return

	case r.directRule.Match(domain):

	case r.proxyRule.Match(domain):
		_ = w.WriteMsg(r.dnsProxyA(domain, r.dns.serveIP, req))
		return
	}

	// 2. direct with cache, do not fallback to proxy to avoid side-effect
	conn := <-r.dns.connCh
	resp, rtt, err := r.dns.ExchangeWithConn(req, conn)
	if err != nil {
		resp, rtt, err = r.dns.ExchangeWithConn(req, conn)
	}
	select {
	case r.dns.connCh <- conn:
	default:
		conn.Close()
	}

	log.DebugWarn(err).
		Dur("rtt", rtt).
		Str("question", req.Question[0].String()).
		Msg("exchange dns record")
	if err != nil {
		_ = w.WriteMsg(r.dnsFail(req, dns.RcodeServerFailure))
	} else {
		_ = w.WriteMsg(resp)
	}
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
