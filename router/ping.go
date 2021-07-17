package router

import (
	"net"
	"net/http"
	"time"
)

var pingClient = http.Client{
	Timeout: 2 * time.Second,
}

func (r *Router) isAccess(domain string) bool {
	p := &ping{}
	r.cache.Remember(p, domain)
	return p.isAccess
}

type ping struct {
	isAccess bool
}

func (p *ping) Fulfill(key interface{}) error {
	_, err := http.Head(net.JoinHostPort(key.(string), "80"))
	p.isAccess = (err == nil)
	return nil
}
