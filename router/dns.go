package router

import (
	"net"

	"github.com/miekg/dns"
	"github.com/sower-proxy/deferlog/log"
	"github.com/wweir/sower/pkg/dhcp"
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
	case r.BlockRule.Match(domain):
		_ = w.WriteMsg(r.dnsFail(req, dns.RcodeNameError))
		return

	case r.DirectRule.Match(domain):

	case r.ProxyRule.Match(domain):
		_ = w.WriteMsg(r.dnsProxyA(domain, r.dns.serveIP, req))
		return
	}

	// 2. direct with cache, do not fallback to proxy to avoid side-effect
	resp, err := r.Exchange(req)
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

var upstreamAddrs []string
var upstreamIndex = -1
var queryCount = 0

func (r *Router) Exchange(req *dns.Msg) (_ *dns.Msg, err error) {
	queryCount++
	if upstreamIndex < 0 {
		dnsIPs, err := dhcp.GetDNSServer()
		log.Err(err).
			Int("queryCount", queryCount).
			Strs("dns", dnsIPs).
			Msg("get dns server")

		addrs := make([]string, 0, len(dnsIPs)+1)
		for _, ip := range dnsIPs {
			if ip != string(r.dns.serveIP) {
				addrs = append(addrs, net.JoinHostPort(ip, "53"))
			}
		}
		if len(addrs) == 0 {
			addrs = append(addrs, net.JoinHostPort(r.dns.fallbackDNS, "53"))
		}

		queryCount = 0
		upstreamAddrs = addrs
		upstreamIndex = len(addrs) - 1
	}

	resp, err := dns.Exchange(req, upstreamAddrs[upstreamIndex])
	if err != nil {
		upstreamIndex--
		if upstreamIndex >= 0 {
			resp, err = dns.Exchange(req, upstreamAddrs[upstreamIndex])
		}
	}

	return resp, err
}
