package router

import (
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wweir/sower/transport"
	"github.com/wweir/sower/util"
	"github.com/wweir/util-go/log"
	"github.com/wweir/util-go/mem"
)

// Route implement a router for each request
type Route struct {
	once  sync.Once
	cache *mem.Cache

	ProxyAddress string
	password     []byte

	DetectLevel int // dynamic detect proxy level
	blockRule   *util.Node
	proxyRule   *util.Node
	directRule  *util.Node
	PersistFn   func(string)
}

func NewRoute(address, password string, detectLevel int,
	blocklist, proxylist, directlist []string, persist func(string)) *Route {
	return &Route{
		cache:        mem.New(4 * time.Hour),
		ProxyAddress: address,
		password:     []byte(password),

		DetectLevel: detectLevel,
		blockRule:   util.NewNodeFromRules(blocklist...),
		proxyRule:   util.NewNodeFromRules(proxylist...),
		directRule:  util.NewNodeFromRules(directlist...),
		PersistFn:   persist,
	}
}

// ShouldProxy check if the domain shoule request though proxy
func (r *Route) GenProxyCheck(sync bool) func(string) (bool, bool) {
	detect := func(domain string) bool {
		go r.cache.Remember(r, domain)
		return true
	}
	if sync {
		detect = func(domain string) bool {
			r.cache.Remember(r, domain)
			if r.proxyRule.Match(domain) {
				return true
			}
			return !r.directRule.Match(domain)
		}
	}

	return func(domain string) (bool, bool) {
		domain = strings.TrimSuffix(domain, ".")
		// break deadlook, for wildcard
		if sepCount := strings.Count(domain, "."); sepCount == 0 || sepCount >= 5 {
			return false, false
		}

		if r.blockRule.Match(domain) {
			return true, false
		}

		if r.proxyRule.Match(domain) {
			return false, true
		}
		if r.directRule.Match(domain) {
			return false, false
		}

		return false, detect(domain)
	}
}

// Get implement for cache
func (r *Route) Get(key interface{}) (err error) {
	domain := key.(string)

	httpScore, httpsScore := r.detect(domain)
	log.Infow("detect", "domain", domain, "http", httpScore, "https", httpsScore)

	if httpScore+httpsScore >= r.DetectLevel {
		r.directRule.Add(domain)
		if r.PersistFn != nil {
			r.PersistFn(domain)
		}
	}
	return nil
}

// detect and caculate direct connection and proxy connection score
func (r *Route) detect(domain string) (http, https int) {
	wg := sync.WaitGroup{}
	httpScore, httpsScore := new(int32), new(int32)
	for _, ping := range [...]struct {
		shouldProxy bool
		port        Port
	}{
		{shouldProxy: true, port: HTTP},
		{shouldProxy: true, port: HTTPS},
		{shouldProxy: false, port: HTTP},
		{shouldProxy: false, port: HTTPS},
	} {
		wg.Add(1)
		go func(shouldProxy bool, port Port) {
			defer wg.Done()

			if err := port.Ping(domain, func(domain string) (net.Conn, error) {
				return transport.Dial(domain,
					func(domain string) (proxyAddr string, password []byte) {
						if shouldProxy {
							return r.ProxyAddress, r.password
						}
						return "", nil
					})
			}); err != nil {
				log.Warnw("sower dial", "proxy", shouldProxy, "host", domain, "port", port, "err", err)
				return
			}

			switch {
			case shouldProxy && port == HTTP:
				if !atomic.CompareAndSwapInt32(httpScore, 0, -2) {
					atomic.AddInt32(httpScore, -1)
				}
			case shouldProxy && port == HTTPS:
				if !atomic.CompareAndSwapInt32(httpsScore, 0, -2) {
					atomic.AddInt32(httpsScore, -1)
				}
			case !shouldProxy && port == HTTP:
				if !atomic.CompareAndSwapInt32(httpScore, 0, 2) {
					atomic.AddInt32(httpScore, 1)
				}
			case !shouldProxy && port == HTTPS:
				if !atomic.CompareAndSwapInt32(httpsScore, 0, 2) {
					atomic.AddInt32(httpsScore, 1)
				}
			}
		}(ping.shouldProxy, ping.port)
	}

	wg.Wait()
	return int(*httpScore), int(*httpsScore)
}
