package router

import (
	"context"
	"net"
	"time"

	"github.com/rs/zerolog/log"
)

func (r *Router) localSite(domain string) bool {
	// parse domain to IP
	ip := net.ParseIP(domain)
	if ip == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		ips, err := net.DefaultResolver.LookupIP(ctx, "ip", domain)
		if err != nil || len(ips) == 0 {
			log.Warn().Err(err).
				Str("domain", domain).
				Int("ips", len(ips)).
				Msg("resolve domain")
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
			log.Warn().Err(err).
				Str("domain", domain).
				IPAddr("ip", ip).
				Msg("mmdb search")
			return false
		}

		if city.Country.IsoCode == "CN" {
			return true
		}
	}

	return false
}
