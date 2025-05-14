package router

import (
	"log/slog"
	"net"
	"sync/atomic"

	"github.com/miekg/dns"
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
		host, _, err := net.SplitHostPort(w.LocalAddr().String())
		if err != nil {
			slog.Warn("proxy dns", "error", err, "host", host)
		} else {
			slog.Debug("proxy dns", "host", host)
		}
		_ = w.WriteMsg(r.dnsProxyA(domain, net.ParseIP(host), req))
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
	m.SetRcode(req, rcode)
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

var (
	upstreamAddrs []string
	upstreamIndex int32 = -1
	queryCount    int32 = 0
)

func (r *Router) Exchange(req *dns.Msg) (_ *dns.Msg, err error) {
	atomic.AddInt32(&queryCount, 1)
	if atomic.LoadInt32(&upstreamIndex) < 0 {
		dnsIPs := []string{r.dns.upstreamDNS}
		if r.dns.upstreamDNS == "" {
			dnsIPs, err = dhcp.GetDNSServer()
			slog.Error("get dns server", "error", err, "queryCount", atomic.LoadInt32(&queryCount), "dns", dnsIPs)
		}

		addrs := make([]string, 0, len(dnsIPs)+1)
		addrs = append(addrs, net.JoinHostPort(r.dns.fallbackDNS, "53"))
		for _, ip := range dnsIPs {
			if ip != string(r.dns.serveIP) {
				addrs = append(addrs, net.JoinHostPort(ip, "53"))
			}
		}

		atomic.StoreInt32(&queryCount, 0)
		upstreamAddrs = addrs
		atomic.StoreInt32(&upstreamIndex, int32(len(addrs)-1))
		slog.Info("use upstream dns", "ips", upstreamAddrs)
	}

	index := atomic.LoadInt32(&upstreamIndex)
	resp, err := dns.Exchange(req, upstreamAddrs[index])
	if err != nil {
		if atomic.CompareAndSwapInt32(&upstreamIndex, index, index-1) && index > 0 {
			slog.Info("use upstream dns", "ip", upstreamAddrs[index-1])
			resp, err = dns.Exchange(req, upstreamAddrs[index-1])
		}
	}

	return resp, err
}
