package router

import (
	"context"
	"net"
	"time"

	"github.com/rs/zerolog/log"
)

func (r *Router) localSite(domain string) bool {

	ip := net.ParseIP(domain)
	if ip == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		ips, err := r.mmdb.Resolver.LookupIP(ctx, "ip", domain)
		if err != nil || len(ips) == 0 {
			log.Warn().Err(err).
				Str("domain", domain).
				Int("ips", len(ips)).
				Msg("resolve domain")
			return false
		}

		ip = ips[0]
	}

	for _, cidr := range r.mmdb.cidrs {
		if cidr.Contains(ip) {
			return true
		}
	}

	if r.mmdb.Reader != nil {
		city, err := r.mmdb.City(ip)
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
