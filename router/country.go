package router

import (
	"context"
	"log/slog"
	"net"
	"time"
)

func (r *Router) localSite(ctx context.Context, domain string) bool {
	if ctx == nil {
		ctx = context.Background()
	}

	if ip := net.ParseIP(domain); ip != nil {
		return r.localIP(domain, ip)
	}

	resolveCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	ips, err := net.DefaultResolver.LookupIP(resolveCtx, "ip", domain)
	if err != nil || len(ips) == 0 {
		slog.Warn("resolve domain", "error", err, "domain", domain, "ips", len(ips))
		return false
	}

	for _, ip := range ips {
		if r.localIP(domain, ip) {
			return true
		}
	}
	return false
}

func (r *Router) localIP(domain string, ip net.IP) bool {
	// CIDR match
	for _, cidr := range r.country.cidrs {
		if cidr.Contains(ip) {
			return true
		}
	}

	r.country.RLock()
	reader := r.country.Reader
	if reader == nil {
		r.country.RUnlock()
		return false
	}
	city, err := reader.City(ip)
	r.country.RUnlock()
	if err != nil {
		slog.Warn("mmdb search", "error", err, "domain", domain, "ip", ip)
		return false
	}

	return city.Country.IsoCode == "CN"
}
