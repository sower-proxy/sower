package router

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	geoip2 "github.com/oschwald/geoip2-golang"
	"github.com/sower-proxy/conns/relay"
	"github.com/sower-proxy/deferlog/v2"
	"github.com/sower-proxy/sower/pkg/dhcp"
	"github.com/sower-proxy/sower/pkg/suffixtree"
)

type (
	ProxyDialFn func(network, host string, port uint16) (net.Conn, error)
	Router      struct {
		BlockRule  *RuleSet
		DirectRule *RuleSet
		ProxyRule  *RuleSet
		ProxyDial  ProxyDialFn

		routeObserver RouteObserver
		accessCache   *accessProbeCache

		dns struct {
			upstreamDNS  string
			fallbackDNS  string
			serveIPs     []net.IP
			getDNSServer func() ([]string, error)

			sync.Mutex
			upstreamAddrs     []string
			upstreamIndex     int
			refreshAt         time.Time
			refreshInFlight   bool
			refreshGeneration uint64
			lastRefreshErr    error
			retryAt           time.Time
			probeInFlight     bool
			// generation invalidates in-flight refreshes after SetDNS: a refresh
			// started under the previous upstream config is discarded on finish.
			generation uint64
		}

		country struct {
			sync.RWMutex
			*geoip2.Reader
			cidrs []*net.IPNet
		}
	}
)

var ErrBlocked = errors.New("route blocked")

// RouteCategory identifies a connection routing decision.
type RouteCategory string

const (
	RouteBlock  RouteCategory = "block"
	RouteDirect RouteCategory = "direct"
	RouteProxy  RouteCategory = "proxy"
)

// RouteObserver receives every connection routing decision, exactly once per
// connection: block/direct decisions in DialSmart, proxy decisions in
// DialProxyOnly (which also covers the HTTP/HTTPS proxy paths).
type RouteObserver func(category RouteCategory, domain string)

// SetRouteObserver installs the routing-decision observer, or clears it with
// a nil argument.
func (r *Router) SetRouteObserver(fn RouteObserver) {
	r.routeObserver = fn
}

func (r *Router) observe(category RouteCategory, domain string) {
	if r.routeObserver != nil {
		r.routeObserver(category, domain)
	}
}

func NewRouter(serveIPs []string, upstreamDNS, fallbackDNS, mmdbFile string, proxyDial ProxyDialFn) (*Router, error) {
	r := Router{
		BlockRule:   NewRuleSet(),
		DirectRule:  NewRuleSet(),
		ProxyRule:   NewRuleSet(),
		ProxyDial:   proxyDial,
		accessCache: newAccessCache(accessCacheTTL, httpPing),
	}

	r.dns.upstreamDNS = upstreamDNS
	r.dns.fallbackDNS = fallbackDNS
	r.dns.getDNSServer = dhcp.GetDNSServer
	for _, serveIP := range serveIPs {
		if ip := net.ParseIP(serveIP); ip != nil {
			r.dns.serveIPs = append(r.dns.serveIPs, ip)
		}
	}

	mmdbFile = strings.TrimSpace(mmdbFile)
	if mmdbFile != "" {
		var err error
		r.country.Reader, err = geoip2.Open(mmdbFile)
		if err != nil {
			return nil, fmt.Errorf("open geoip2 db %q: %w", mmdbFile, err)
		}
	}

	return &r, nil
}

func (r *Router) Close() error {
	r.country.Lock()
	defer r.country.Unlock()

	if r.country.Reader == nil {
		return nil
	}
	err := r.country.Reader.Close()
	r.country.Reader = nil
	return err
}

func (r *Router) AddCountryCIDRs(cidrs ...string) error {
	parsed := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		cidr = strings.TrimSpace(cidr)
		if cidr == "" {
			continue
		}
		_, ipnet, err := net.ParseCIDR(cidr)
		if err != nil {
			return fmt.Errorf("parse country CIDR %q: %w", cidr, err)
		}
		parsed = append(parsed, ipnet)
	}
	r.country.cidrs = append(r.country.cidrs, parsed...)
	r.country.cidrs = suffixtree.GCSlice(r.country.cidrs)
	return nil
}

func (r *Router) RouteHandle(conn net.Conn, domain string, port uint16) (err error) {
	start := time.Now()
	defer func() {
		deferlog.DebugWarn(err, "route handle", "domain", domain, "port", port, "took", time.Since(start))
	}()

	rc, err := r.DialSmart("tcp", domain, port)
	if err != nil {
		return err
	}
	defer rc.Close()

	if err := relay.Relay(conn, rc); err != nil {
		return fmt.Errorf("relay %s:%d: %w", domain, port, err)
	}
	return nil
}

func (r *Router) Dial(network, domain string, port uint16) (net.Conn, error) {
	return r.DialSmart(network, domain, port)
}

func (r *Router) DialSmart(network, domain string, port uint16) (net.Conn, error) {
	ctx := context.Background()
	addr := net.JoinHostPort(domain, strconv.FormatUint(uint64(port), 10))

	// 1. rule_based( block > direct > proxy )
	// 2. detect_based( CN IP || access site )
	// 3. fallback( proxy )
	switch {
	case r.BlockRule.Match(domain):
		r.observe(RouteBlock, domain)
		return nil, ErrBlocked
	case r.DirectRule.Match(domain):
		r.observe(RouteDirect, domain)
		return r.directDial(ctx, network, addr)
	case r.ProxyRule.Match(domain):
		return r.DialProxyOnly(network, domain, port)
	case r.localSite(ctx, domain), r.isAccess(domain, port):
		r.observe(RouteDirect, domain)
		return r.directDial(ctx, network, addr)
	default:
		return r.DialProxyOnly(network, domain, port)
	}
}

func (r *Router) DialProxyOnly(network, domain string, port uint16) (net.Conn, error) {
	if r.ProxyDial == nil {
		return nil, fmt.Errorf("proxy dialer unavailable")
	}
	r.observe(RouteProxy, domain)
	start := time.Now()
	rc, err := r.ProxyDial(network, domain, port)
	if err != nil {
		return nil, fmt.Errorf("proxy dial %s:%d, spend (%s): %w", domain, port, time.Since(start), err)
	}
	return rc, nil
}

func (r *Router) directDial(ctx context.Context, network, addr string) (net.Conn, error) {
	start := time.Now()
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, fmt.Errorf("direct dial %s, spend (%s): %w", addr, time.Since(start), err)
	}
	return conn, nil
}
