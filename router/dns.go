package router

import (
	"net"
	"time"

	"github.com/miekg/dns"
	"github.com/rs/zerolog/log"
)

func (r *Router) ServeDNS(w dns.ResponseWriter, req *dns.Msg) {
	// *Msg r has an TSIG record and it was validated
	if req.IsTsig() != nil && w.TsigStatus() == nil {
		lastTsig := req.Extra[len(req.Extra)-1].(*dns.TSIG)
		req.SetTsig(lastTsig.Hdr.Name, dns.HmacMD5, 300, time.Now().Unix())
	}

	// https://stackoverflow.com/questions/4082081/requesting-a-and-aaaa-records-in-single-dns-query/4083071#4083071
	if len(req.Question) == 0 {
		w.WriteMsg(r.dnsFail(req, dns.RcodeFormatError))
		return
	}

	domain := req.Question[0].Name
	switch {
	case r.blockRule.Match(domain):
		w.WriteMsg(r.dnsFail(req, dns.RcodeNameError))
		return

	case r.directRule.Match(domain):

	case r.proxyRule.Match(domain):
		host, _, _ := net.SplitHostPort(w.LocalAddr().String())
		w.WriteMsg(r.dnsProxyA(domain, net.ParseIP(host), req))
		return
	}

	conn := <-r.dns.connCh
	resp, rtt, err := r.dns.ExchangeWithConn(req, conn)
	if err != nil {
		log.Error().Err(err).
			Dur("rtt", rtt).
			Str("domain", domain).
			Msg("exchange dns record")

		conn.Close()
		w.WriteMsg(r.dnsFail(req, dns.RcodeServerFailure))
		return
	}

	select {
	case r.dns.connCh <- conn:
	default:
		conn.Close()
	}
	w.WriteMsg(resp)
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
