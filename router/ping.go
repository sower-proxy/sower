package router

import (
	"net/http"
	"sync"
	"time"

	"github.com/sower-proxy/deferlog/log"
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
	wg := sync.WaitGroup{}
	wg.Add(2)
	var err80, err443 error
	go func() {
		_, err80 = pingClient.Head("http://" + key)
		wg.Done()
	}()
	go func() {
		_, err443 = pingClient.Head("https://" + key)
		wg.Done()
	}()
	wg.Wait()

	if err80 != nil && err443 != nil {
		log.Warn().
			Errs("errs", []error{err80, err443}).
			Msg("Failed to ping")
	}

	p.isAccess = (err80 == nil && err443 == nil)
	return nil
}
