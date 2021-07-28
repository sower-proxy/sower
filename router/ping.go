package router

import (
	"net"
	"net/http"
	"time"
)

var pingClient = http.Client{
	Timeout: 2 * time.Second,
}

func (r *Router) isAccess(domain string, port uint16) bool {
	switch port {
	case 80:
	case 443:
	default:
		return false
	}

	p := &ping{}
	r.accessCache.Remember(p, domain)
	return p.isAccess
}

type ping struct {
	isAccess bool
}

func (p *ping) Fulfill(key string) error {
	_, err := http.Head(net.JoinHostPort(key, "80"))
	p.isAccess = (err == nil)
	return nil
}
