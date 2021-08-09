package router

import (
	"net"
	"net/http"
	"time"

	"github.com/wweir/deferlog"
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
	_ = r.accessCache.Remember(p, domain)
	return p.isAccess
}

type ping struct {
	isAccess bool
}

func (p *ping) Fulfill(key string) error {
	_, err := pingClient.Head(net.JoinHostPort(key, "80"))
	deferlog.Std.DebugWarn(err).
		Str("domain", key).
		Msg("detect if site is accessible")
	p.isAccess = (err == nil)
	return nil
}
