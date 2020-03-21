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
	port  Port
	once  sync.Once
	cache *mem.Cache

	ProxyAddress  string
	ProxyPassword string
	password      []byte

	DetectLevel   int    // dynamic detect proxy level
	DetectTimeout string // dynamic detect timeout
	timeout       time.Duration

	DirectList  []string
	directRule  *util.Node
	ProxyList   []string
	proxyRule   *util.Node
	DynamicList []string
	dynamicRule *util.Node
	PersistFn   func(string)
}

// ShouldProxy check if the domain shoule request though proxy
func (r *Route) ShouldProxy(domain string) bool {
	r.once.Do(func() {
		r.cache = mem.New(4 * time.Hour)
		r.password = []byte(r.ProxyPassword)
		r.directRule = util.NewNodeFromRules(r.DirectList...)
		r.proxyRule = util.NewNodeFromRules(r.ProxyList...)
		r.dynamicRule = util.NewNodeFromRules(r.DynamicList...)

		if timeout, err := time.ParseDuration(r.DetectTimeout); err != nil {
			r.timeout = 200 * time.Millisecond
			log.Warnw("parse detect timeout", "err", err, "default", "t.timeout")
		} else {
			r.timeout = timeout
		}
	})

	// break deadlook, for wildcard
	if strings.Count(domain, ".") > 4 {
		return false
	}
	domain = strings.TrimSuffix(domain, ".")

	if domain == r.ProxyAddress {
		return false
	}
	if r.directRule.Match(domain) {
		return false
	}
	if r.proxyRule.Match(domain) {
		return true
	}
	if r.dynamicRule.Match(domain) {
		return true
	}

	r.cache.Remember(r, domain)
	return r.dynamicRule.Match(domain)
}

// Get implement for cache
func (r *Route) Get(key interface{}) (err error) {
	domain := key.(string)

	httpScore, httpsScore := r.detect(domain)
	log.Infow("detect", "domain", domain, "http", httpScore, "https", httpsScore)

	if httpScore+httpsScore >= r.DetectLevel {
		r.dynamicRule.Add(domain)
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
	for _, ping := range [...]*Route{{port: HTTP}, {port: HTTPS}} {
		wg.Add(1)
		go func(ping *Route) {
			defer wg.Done()

			if err := ping.port.Ping(domain, r.timeout); err != nil {
				return
			}

			switch ping.port {
			case HTTP:
				if !atomic.CompareAndSwapInt32(httpScore, 0, -2) {
					atomic.AddInt32(httpScore, -1)
				}
			case HTTPS:
				if !atomic.CompareAndSwapInt32(httpsScore, 0, -2) {
					atomic.AddInt32(httpScore, -1)
				}
			}
		}(ping)
	}
	for _, ping := range [...]*Route{{port: HTTP}, {port: HTTPS}} {
		wg.Add(1)
		go func(ping *Route) {
			defer wg.Done()

			target := net.JoinHostPort(domain, ping.port.String())
			conn, err := transport.Dial(r.ProxyAddress, target, r.password)
			if err != nil {
				log.Errorw("sower dial", "addr", r.ProxyAddress, "err", err)
				return
			}

			if err := ping.port.PingWithConn(domain, conn, r.timeout); err != nil {
				return
			}

			switch ping.port {
			case HTTP:
				if !atomic.CompareAndSwapInt32(httpScore, 0, 2) {
					atomic.AddInt32(httpScore, 1)
				}
			case HTTPS:
				if !atomic.CompareAndSwapInt32(httpsScore, 0, 2) {
					atomic.AddInt32(httpScore, 1)
				}
			}
		}(ping)
	}

	wg.Wait()
	return int(*httpScore), int(*httpsScore)
}
