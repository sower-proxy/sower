package router

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/miekg/dns"
)

var errNoLocalIPForQuestionType = errors.New("no local IP for question type")

type retryableDNSError struct {
	rcode int
}

func (e retryableDNSError) Error() string {
	return fmt.Sprintf("retryable dns response code %s", dns.RcodeToString[e.rcode])
}

func (r *Router) ServeDNS(w dns.ResponseWriter, req *dns.Msg) {
	// https://stackoverflow.com/questions/4082081/requesting-a-and-aaaa-records-in-single-dns-query/4083071#4083071
	if len(req.Question) != 1 {
		_ = w.WriteMsg(r.dnsFail(req, dns.RcodeFormatError))
		return
	}

	domain := req.Question[0].Name
	qtype := req.Question[0].Qtype
	if req.Question[0].Qclass != dns.ClassINET {
		_ = w.WriteMsg(r.dnsFail(req, dns.RcodeNotImplemented))
		return
	}

	// 1. rule_based( block > direct > proxy )
	routeDomains := dnsRouteDomains(domain, qtype)
	if dnsRuleMatch(r.BlockRule, routeDomains) {
		_ = w.WriteMsg(r.dnsFail(req, dns.RcodeNameError))
		return
	}

	if !dnsRuleMatch(r.DirectRule, routeDomains) && dnsRuleMatch(r.ProxyRule, routeDomains) {
		switch {
		case isAddressQuestion(qtype):
			resp, err := r.dnsProxyReply(domain, w.LocalAddr(), req)
			if err != nil {
				slog.Warn("proxy dns", "error", err, "domain", domain)
				_ = w.WriteMsg(r.dnsFail(req, dns.RcodeServerFailure))
				return
			}
			_ = w.WriteMsg(resp)
			return
		default:
			_ = w.WriteMsg(r.dnsNoData(req))
			return
		}
	}

	// 2. direct query, do not fallback to proxy to avoid side-effect
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

func (r *Router) dnsNoData(req *dns.Msg) *dns.Msg {
	return dnsReply(req)
}

func (r *Router) dnsProxyReply(domain string, localAddr net.Addr, req *dns.Msg) (*dns.Msg, error) {
	qtype := req.Question[0].Qtype
	reply := dnsReply(req)

	localIP, err := r.proxyReplyIP(localAddr, qtype)
	if err != nil {
		if errors.Is(err, errNoLocalIPForQuestionType) {
			return reply, nil
		}
		return nil, err
	}

	record, err := proxyReplyRecord(domain, qtype, localIP)
	if err != nil {
		return nil, err
	}
	reply.Answer = []dns.RR{record}
	return reply, nil
}

func (r *Router) proxyReplyIP(localAddr net.Addr, qtype uint16) (net.IP, error) {
	if !isAddressQuestion(qtype) {
		return nil, fmt.Errorf("unsupported question type %d", qtype)
	}

	var localAddrErr error
	if localAddr != nil {
		host, _, err := net.SplitHostPort(localAddr.String())
		if err != nil {
			localAddrErr = fmt.Errorf("split local dns address %q: %w", localAddr.String(), err)
		} else {
			ip := net.ParseIP(strings.Trim(host, "[]"))
			switch {
			case ip == nil:
				localAddrErr = fmt.Errorf("parse local dns IP %q", host)
			case !ip.IsUnspecified() && matchesQuestionTypeIP(ip, qtype):
				return ip, nil
			}
		}
	}

	for _, serveIP := range r.dns.serveIPs {
		if !serveIP.IsUnspecified() && matchesQuestionTypeIP(serveIP, qtype) {
			return serveIP, nil
		}
	}

	if localAddrErr != nil {
		return nil, localAddrErr
	}
	return nil, fmt.Errorf("%w %d", errNoLocalIPForQuestionType, qtype)
}

func isAddressQuestion(qtype uint16) bool {
	return qtype == dns.TypeA || qtype == dns.TypeAAAA
}

func dnsRuleMatch(rule interface{ Match(string) bool }, domains []string) bool {
	for _, domain := range domains {
		if rule.Match(domain) {
			return true
		}
	}
	return false
}

func dnsRouteDomains(domain string, qtype uint16) []string {
	if !isServiceDiscoveryQuestion(qtype) {
		return []string{domain}
	}

	base, ok := stripDNSServicePrefix(domain)
	if !ok {
		return []string{domain}
	}
	return []string{domain, base}
}

func isServiceDiscoveryQuestion(qtype uint16) bool {
	switch qtype {
	case dns.TypeSRV, dns.TypeSVCB, dns.TypeHTTPS:
		return true
	default:
		return false
	}
}

func stripDNSServicePrefix(domain string) (string, bool) {
	trimmed := strings.TrimSuffix(domain, ".")
	labels := strings.Split(trimmed, ".")
	if len(labels) <= 2 || !strings.HasPrefix(labels[0], "_") || !strings.HasPrefix(labels[1], "_") {
		return "", false
	}
	return strings.Join(labels[2:], ".") + ".", true
}

func dnsReply(req *dns.Msg) *dns.Msg {
	reply := new(dns.Msg)
	reply.SetReply(req)
	return reply
}

func proxyReplyRecord(domain string, qtype uint16, localIP net.IP) (dns.RR, error) {
	switch qtype {
	case dns.TypeA:
		return &dns.A{
			Hdr: dns.RR_Header{Name: domain, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 20},
			A:   localIP,
		}, nil
	case dns.TypeAAAA:
		return &dns.AAAA{
			Hdr:  dns.RR_Header{Name: domain, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 20},
			AAAA: localIP,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported question type %d", qtype)
	}
}

func matchesQuestionTypeIP(ip net.IP, qtype uint16) bool {
	switch qtype {
	case dns.TypeA:
		return ip.To4() != nil
	case dns.TypeAAAA:
		return ip.To4() == nil
	default:
		return false
	}
}

const (
	dnsTimeout       = 2 * time.Second
	dnsUDPSize       = 1232
	dnsRetryInterval = 30 * time.Second
	dnsRefreshTTL    = 5 * time.Minute
)

func (r *Router) Exchange(req *dns.Msg) (_ *dns.Msg, err error) {
	addrs, index, shouldProbe, err := r.currentUpstreamState(time.Now())
	if err != nil {
		return nil, err
	}

	if shouldProbe {
		resp, probeErr := r.exchangeWithRetry(req, addrs[0])
		if probeErr == nil {
			r.promoteUpstream()
			return resp, nil
		}
		r.scheduleRetry(time.Now())
	}

	resp, err := r.exchangeWithRetry(req, addrs[index])
	if err != nil {
		nextIndex, switched := r.degradeUpstream(index)
		if switched {
			slog.Info("use upstream dns", "ip", addrs[nextIndex])
			resp, err = r.exchangeWithRetry(req, addrs[nextIndex])
		}
	}

	if resp != nil && isRetryableDNSResponseErr(err) {
		return resp, nil
	}
	return resp, err
}

func isRetryableDNSResponseErr(err error) bool {
	var retryableErr retryableDNSError
	return errors.As(err, &retryableErr)
}

func (r *Router) currentUpstreamState(now time.Time) ([]string, int, bool, error) {
	for {
		addrs, index, shouldProbe, needRefresh, err := r.prepareUpstreamState(now)
		if !needRefresh || err != nil {
			return addrs, index, shouldProbe, err
		}

		refreshedAddrs, refreshErr := r.buildUpstreamAddrs()
		r.finishUpstreamRefresh(now, refreshedAddrs, refreshErr)
	}
}

func (r *Router) degradeUpstream(index int) (int, bool) {
	r.dns.Lock()
	defer r.dns.Unlock()

	if len(r.dns.upstreamAddrs) == 0 || r.dns.upstreamIndex != index || index >= len(r.dns.upstreamAddrs)-1 {
		return r.dns.upstreamIndex, false
	}

	r.dns.upstreamIndex = index + 1
	r.dns.retryAt = time.Now().Add(dnsRetryInterval)
	r.dns.probeInFlight = false
	return r.dns.upstreamIndex, true
}

func (r *Router) promoteUpstream() {
	r.dns.Lock()
	defer r.dns.Unlock()

	r.dns.upstreamIndex = 0
	r.dns.retryAt = time.Time{}
	r.dns.probeInFlight = false
}

func (r *Router) scheduleRetry(now time.Time) {
	r.dns.Lock()
	defer r.dns.Unlock()

	if r.dns.upstreamIndex > 0 {
		r.dns.retryAt = now.Add(dnsRetryInterval)
		r.dns.probeInFlight = false
	}
}

func (r *Router) prepareUpstreamState(now time.Time) ([]string, int, bool, bool, error) {
	r.dns.Lock()
	defer r.dns.Unlock()

	needRefresh := false
	switch {
	case len(r.dns.upstreamAddrs) == 0:
		if r.dns.refreshInFlight {
			if fallbackAddrs, ok := r.fallbackOnlyUpstreamsLocked(); ok {
				return fallbackAddrs, 0, false, false, nil
			}
			return nil, 0, false, false, r.upstreamUnavailableErrLocked()
		}
		if !r.dns.refreshAt.IsZero() && now.Before(r.dns.refreshAt) {
			if fallbackAddrs, ok := r.fallbackOnlyUpstreamsLocked(); ok {
				return fallbackAddrs, 0, false, false, nil
			}
			return nil, 0, false, false, r.upstreamUnavailableErrLocked()
		}
		r.dns.refreshInFlight = true
		needRefresh = true
	case r.shouldRefreshUpstreams(now) && !r.dns.refreshInFlight:
		r.dns.refreshInFlight = true
		needRefresh = true
	}

	shouldProbe := !needRefresh &&
		len(r.dns.upstreamAddrs) > 0 &&
		r.dns.upstreamIndex > 0 &&
		!r.dns.retryAt.IsZero() &&
		!now.Before(r.dns.retryAt) &&
		!r.dns.probeInFlight
	if shouldProbe {
		r.dns.probeInFlight = true
		r.dns.retryAt = now.Add(dnsRetryInterval)
	}

	addrs := append([]string(nil), r.dns.upstreamAddrs...)
	return addrs, r.dns.upstreamIndex, shouldProbe, needRefresh, nil
}

func (r *Router) buildUpstreamAddrs() ([]string, error) {
	dnsIPs := []string{r.dns.upstreamDNS}
	if r.dns.upstreamDNS == "" {
		var err error
		dnsIPs, err = r.dns.getDNSServer()
		if err != nil {
			slog.Error("get dns server", "error", err, "dns", dnsIPs)
		}
	}

	addrs := make([]string, 0, len(dnsIPs)+1)
	seen := make(map[string]struct{}, len(dnsIPs)+1)
	appendAddr := func(ip string) {
		if ip == "" || r.isServeIP(ip) {
			return
		}
		addr := net.JoinHostPort(ip, "53")
		if _, ok := seen[addr]; ok {
			return
		}
		seen[addr] = struct{}{}
		addrs = append(addrs, addr)
	}

	for _, ip := range dnsIPs {
		appendAddr(ip)
	}
	appendAddr(r.dns.fallbackDNS)
	if len(addrs) == 0 {
		return nil, fmt.Errorf("no available upstream dns")
	}

	return addrs, nil
}

func (r *Router) shouldRefreshUpstreams(now time.Time) bool {
	return r.dns.upstreamDNS == "" && !r.dns.refreshAt.IsZero() && !now.Before(r.dns.refreshAt)
}

func (r *Router) scheduleRefreshLocked(now time.Time) {
	if r.dns.upstreamDNS == "" {
		r.dns.refreshAt = now.Add(dnsRefreshTTL)
	}
}

func (r *Router) applyUpstreamAddrsLocked(addrs []string, now time.Time) {
	currentAddr := ""
	retryAt := r.dns.retryAt
	if len(r.dns.upstreamAddrs) > 0 && r.dns.upstreamIndex >= 0 && r.dns.upstreamIndex < len(r.dns.upstreamAddrs) {
		currentAddr = r.dns.upstreamAddrs[r.dns.upstreamIndex]
	}

	r.dns.upstreamAddrs = addrs
	r.dns.upstreamIndex = 0
	if currentAddr != "" {
		for i, addr := range addrs {
			if addr == currentAddr {
				r.dns.upstreamIndex = i
				break
			}
		}
	}
	if r.dns.upstreamIndex == 0 {
		r.dns.retryAt = time.Time{}
	} else {
		r.dns.retryAt = retryAt
	}
	r.dns.lastRefreshErr = nil
	r.dns.refreshInFlight = false
	r.dns.probeInFlight = false
	r.scheduleRefreshLocked(now)
	slog.Info("use upstream dns", "ips", addrs)
}

func (r *Router) finishUpstreamRefresh(now time.Time, addrs []string, err error) {
	r.dns.Lock()
	defer r.dns.Unlock()

	if err != nil {
		r.dns.refreshInFlight = false
		r.dns.lastRefreshErr = err
		r.scheduleRefreshLocked(now)
		if len(r.dns.upstreamAddrs) > 0 {
			slog.Warn("refresh upstream dns", "error", err)
		}
		return
	}

	r.applyUpstreamAddrsLocked(addrs, now)
}

func (r *Router) upstreamUnavailableErrLocked() error {
	if r.dns.lastRefreshErr != nil {
		return r.dns.lastRefreshErr
	}
	return fmt.Errorf("upstream dns unavailable")
}

func (r *Router) fallbackOnlyUpstreamsLocked() ([]string, bool) {
	if r.dns.fallbackDNS == "" || r.isServeIP(r.dns.fallbackDNS) {
		return nil, false
	}
	return []string{net.JoinHostPort(r.dns.fallbackDNS, "53")}, true
}

func (r *Router) exchangeWithRetry(req *dns.Msg, addr string) (*dns.Msg, error) {
	resp, err := r.exchangeUDP(req, addr)
	if err != nil {
		return nil, err
	}
	if !resp.Truncated {
		return resp, dnsResponseErr(resp)
	}

	resp, err = r.exchangeTCP(req, addr)
	if err != nil {
		return nil, err
	}
	return resp, dnsResponseErr(resp)
}

func dnsResponseErr(resp *dns.Msg) error {
	if resp == nil {
		return fmt.Errorf("empty dns response")
	}
	switch resp.Rcode {
	case dns.RcodeServerFailure, dns.RcodeRefused, dns.RcodeNotImplemented:
		return retryableDNSError{rcode: resp.Rcode}
	default:
		return nil
	}
}

func (r *Router) exchangeUDP(req *dns.Msg, addr string) (*dns.Msg, error) {
	msg := req.Copy()
	if msg.IsEdns0() == nil {
		msg.SetEdns0(dnsUDPSize, false)
	}

	client := dns.Client{
		Net:     "udp",
		Timeout: dnsTimeout,
		UDPSize: dnsUDPSize,
	}
	resp, _, err := client.Exchange(msg, addr)
	return resp, err
}

func (r *Router) exchangeTCP(req *dns.Msg, addr string) (*dns.Msg, error) {
	client := dns.Client{
		Net:     "tcp",
		Timeout: dnsTimeout,
	}
	resp, _, err := client.Exchange(req.Copy(), addr)
	return resp, err
}

func (r *Router) isServeIP(raw string) bool {
	ip := net.ParseIP(strings.Trim(raw, "[]"))
	if ip == nil {
		return false
	}

	for _, serveIP := range r.dns.serveIPs {
		if serveIP.Equal(ip) {
			return true
		}
	}
	return false
}
