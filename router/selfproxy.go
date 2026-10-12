package router

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	// selfProxyLookupTTL bounds how long a resolved name is reused for the
	// self-target check. A proxied dial must not pay a DNS round trip per
	// connection, and a name that points at this client keeps pointing at it
	// for far longer than this window.
	selfProxyLookupTTL = time.Minute
	// selfProxyLookupTimeout matches localSite's resolution budget.
	selfProxyLookupTimeout = 2 * time.Second
	selfProxyCacheMaxNames = 1024
)

// ErrSelfProxyTarget reports a proxy dial to an address this client serves
// proxy traffic on. Forwarding it would re-enter this proxy, which forwards it
// again, so a single request becomes a self-sustaining chain between this
// client and its remote relay: measured 2026-10-12, one request to a name whose
// A record is the client's own serve address grew to 601 connections on r6c and
// OOM-killed the relay host.
var ErrSelfProxyTarget = errors.New("target is this client's own listener")

// selfProxyGuard is the set of TCP addresses this client serves proxy traffic
// on. A proxy dial to one of them can only re-enter the proxy.
type selfProxyGuard struct {
	// sockets holds concrete "ip:port" listeners.
	sockets map[string]struct{}
	// anyIP holds ports bound to an unspecified or non-IP host: those are
	// reachable at every local address, so they match any resolved IP.
	anyIP map[uint16]struct{}
	// ports is every tracked port, so a dial to a port this client does not
	// serve proxy traffic on skips resolution entirely.
	ports map[uint16]struct{}
}

func newSelfProxyGuard(addrs []string) *selfProxyGuard {
	g := &selfProxyGuard{
		sockets: make(map[string]struct{}, len(addrs)),
		anyIP:   make(map[uint16]struct{}, 1),
		ports:   make(map[uint16]struct{}, len(addrs)),
	}
	for _, addr := range addrs {
		host, portStr, err := net.SplitHostPort(addr)
		if err != nil {
			// The addresses come from a config whose listeners already bound
			// successfully; an unparsable one is not a listener this process
			// serves, so it cannot be a self target.
			continue
		}
		port, err := strconv.ParseUint(portStr, 10, 16)
		if err != nil {
			continue
		}
		g.ports[uint16(port)] = struct{}{}

		ip := net.ParseIP(strings.Trim(host, "[]"))
		if ip == nil || ip.IsUnspecified() {
			g.anyIP[uint16(port)] = struct{}{}
			continue
		}
		g.sockets[net.JoinHostPort(ip.String(), strconv.FormatUint(port, 10))] = struct{}{}
	}
	return g
}

func (g *selfProxyGuard) port(port uint16) bool {
	_, ok := g.ports[port]
	return ok
}

func (g *selfProxyGuard) matches(ip net.IP, port uint16) bool {
	if _, ok := g.anyIP[port]; ok {
		return true
	}
	_, ok := g.sockets[net.JoinHostPort(ip.String(), strconv.FormatUint(uint64(port), 10))]
	return ok
}

// SetSelfProxyAddrs records the TCP addresses this client serves proxy traffic
// on — the HTTP and HTTPS proxy listeners plus SOCKS5. DialProxyOnly refuses a
// target that resolves to one of them; callers pass every address they listen
// on for proxying, not just the one that received a given request.
func (r *Router) SetSelfProxyAddrs(addrs ...string) {
	r.selfProxy.Store(newSelfProxyGuard(addrs))
}

// selfTarget reports whether dialing domain:port reaches one of this client's
// own proxy listeners, and the address it resolved to.
func (r *Router) selfTarget(domain string, port uint16) (net.IP, bool) {
	guard := r.selfProxy.Load()
	if guard == nil || !guard.port(port) {
		return nil, false
	}
	for _, ip := range r.selfProxyIPs(domain) {
		if guard.matches(ip, port) {
			return ip, true
		}
	}
	return nil, false
}

// selfProxyIPs resolves domain for the self-target check. The relay resolves
// the target remotely, so this resolution exists only to answer "is that
// address mine?"; its answer is cached, so only a name that is new within
// selfProxyLookupTTL costs a lookup. A resolution failure is not evidence of
// self-targeting: it returns no addresses and the dial proceeds, leaving the
// relay to report its own resolution failure.
func (r *Router) selfProxyIPs(domain string) []net.IP {
	if ip := net.ParseIP(strings.Trim(domain, "[]")); ip != nil {
		return []net.IP{ip}
	}
	if ips, ok := r.selfProxyCache.GetIfPresent(domain); ok {
		return ips
	}

	ctx, cancel := context.WithTimeout(context.Background(), selfProxyLookupTimeout)
	defer cancel()

	ips, err := r.selfProxyLookup(ctx, domain)
	if err != nil || len(ips) == 0 {
		return nil
	}
	r.selfProxyCache.Set(domain, ips)
	return ips
}

// selfProxyError renders a refused proxy dial.
func selfProxyError(domain string, port uint16, ip net.IP) error {
	return fmt.Errorf("%w: %s resolves to %s",
		ErrSelfProxyTarget,
		net.JoinHostPort(domain, strconv.FormatUint(uint64(port), 10)),
		net.JoinHostPort(ip.String(), strconv.FormatUint(uint64(port), 10)))
}
