package router

import (
	"context"
	"log/slog"
	"net"
	"time"
)

func (r *Router) localSite(domain string) bool {
	// parse domain to IP
	ip := net.ParseIP(domain)
	if ip == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		ips, err := net.DefaultResolver.LookupIP(ctx, "ip", domain)
		if err != nil || len(ips) == 0 {
			slog.Warn("resolve domain", "error", err, "domain", domain, "ips", len(ips))
			return false
		}

		ip = ips[0]
	}

	// CIDR match
	for _, cidr := range r.country.cidrs {
		if cidr.Contains(ip) {
			return true
		}
	}

	// MMDB match CN
	if r.country.Reader != nil {
		city, err := r.country.City(ip)
		if err != nil {
			slog.Warn("mmdb search", "error", err, "domain", domain, "ip", ip)
			return false
		}

		if city.Country.IsoCode == "CN" {
			return true
		}
	}

	return false
}
