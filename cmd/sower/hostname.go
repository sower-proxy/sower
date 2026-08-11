package main

import (
	"context"
	"net"
	"strings"
	"time"

	"github.com/miekg/dns"
)

// dnsHostnameResolver resolves client IPs to hostnames via PTR queries to a
// configured reverse DNS server. On OpenWrt deployments this is the local
// dnsmasq: it answers LAN DHCP lease names directly, routes tailnet names to
// MagicDNS, and forwards public reverse names upstream. A failure or empty
// answer yields an empty hostname and the console falls back to the raw IP.
type dnsHostnameResolver struct {
	addr   string
	client *dns.Client
}

func newDNSHostnameResolver(reverseDNS string) *dnsHostnameResolver {
	return &dnsHostnameResolver{
		addr:   net.JoinHostPort(reverseDNS, "53"),
		client: &dns.Client{Net: "udp", Timeout: 2 * time.Second},
	}
}

func (r *dnsHostnameResolver) Hostname(ctx context.Context, ip string) string {
	// Transport addresses may carry an IPv6 zone (fe80::1%eth0); strip it
	// before constructing the reverse name.
	if i := strings.IndexByte(ip, '%'); i >= 0 {
		ip = ip[:i]
	}
	arpa, err := dns.ReverseAddr(ip)
	if err != nil {
		return ""
	}
	msg := new(dns.Msg)
	msg.SetQuestion(arpa, dns.TypePTR)

	resp, _, err := r.client.ExchangeContext(ctx, msg, r.addr)
	if err != nil || resp == nil {
		return ""
	}
	for _, rr := range resp.Answer {
		if ptr, ok := rr.(*dns.PTR); ok {
			return strings.TrimSuffix(ptr.Ptr, ".")
		}
	}
	return ""
}
