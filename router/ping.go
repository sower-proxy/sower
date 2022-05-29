package router

import (
	"net/http"
	"sync"
	"time"

	"github.com/sower-proxy/deferlog/log"
	"github.com/sower-proxy/mem"
)

var accessCache = mem.NewRotateCache(time.Hour, httpPing)

func (r *Router) isAccess(domain string, port uint16) bool {
	switch port {
	case 80:
	case 443:
	default:
		return false
	}

	ok, _ := accessCache.Get(domain)
	return ok
}

var pingClient = http.Client{
	Timeout: 2 * time.Second,
}

func httpPing(key string) (bool, error) {
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

	return err80 == nil && err443 == nil, nil
}
