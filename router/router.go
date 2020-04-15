package router

import (
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wweir/sower/transport"
	"github.com/wweir/sower/util"
	"github.com/wweir/utils/log"
	"github.com/wweir/utils/mem"
)

// Route implement a router for each request
type Route struct {
	once  sync.Once
	cache *mem.Cache

	ProxyAddress  string
	ProxyPassword string
	password      []byte

	DetectLevel int // dynamic detect proxy level
	DirectList  []string
	directRule  *util.Node
	ProxyList   []string
	proxyRule   *util.Node
	PersistFn   func(string)
}

// ShouldProxy check if the domain shoule request though proxy
func (r *Route) ShouldProxy(domain string) bool {
	r.once.Do(func() {
		r.cache = mem.New(4 * time.Hour)
		r.password = []byte(r.ProxyPassword)
		r.directRule = util.NewNodeFromRules(r.DirectList...)
		r.proxyRule = util.NewNodeFromRules(r.ProxyList...)
	})

	// break deadlook, for wildcard
	if strings.Count(domain, ".") > 4 {
		return false
	}
	domain = strings.TrimSuffix(domain, ".")

	if domain == r.ProxyAddress {
		return false
	}

	if r.proxyRule.Match(domain) {
		return true
	}
	if r.directRule.Match(domain) {
		return false
	}

	go r.cache.Remember(r, domain)
	return true
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

			target := net.JoinHostPort(domain, port.String())
			conn, err := transport.Dial(target, func(string) (string, []byte) {
				if shouldProxy {
					return r.ProxyAddress, r.password
				}
				return "", nil
			})
			if err != nil {
				log.Warnw("sower dial", "proxy", shouldProxy, "address", target, "err", err)
				return
			}

			if err := port.PingWithConn(domain, conn, 5*time.Second); err != nil {
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
