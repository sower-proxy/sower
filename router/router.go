package router

import (
	"errors"
	"fmt"
	"log/slog"
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
		BlockRule  *suffixtree.Node
		DirectRule *suffixtree.Node
		ProxyRule  *suffixtree.Node
		ProxyDial  ProxyDialFn

		dns struct {
			upstreamDNS  string
			fallbackDNS  string
			serveIPs     []net.IP
			getDNSServer func() ([]string, error)

			sync.Mutex
			upstreamAddrs   []string
			upstreamIndex   int
			refreshAt       time.Time
			refreshInFlight bool
			lastRefreshErr  error
			retryAt         time.Time
			probeInFlight   bool
		}

		country struct {
			*geoip2.Reader
			cidrs []*net.IPNet
		}
	}
)

var ErrBlocked = errors.New("route blocked")

func NewRouter(serveIPs []string, upstreamDNS, fallbackDNS, mmdbFile string, proxyDial ProxyDialFn) *Router {
	r := Router{
		ProxyDial: proxyDial,
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
			slog.Warn("open geoip2 db", "error", err, "file", mmdbFile)
		}
	}

	return &r
}

func (r *Router) AddCountryCIDRs(cidrs ...string) {
	for _, cidr := range cidrs {
		_, ipnet, err := net.ParseCIDR(cidr)
		if err != nil {
			slog.Error("failed to parse CIDR", "error", err, "cidr", cidr)
			continue
		}
		r.country.cidrs = append(r.country.cidrs, ipnet)
	}
	r.country.cidrs = suffixtree.GCSlice(r.country.cidrs)
}

func (r *Router) RouteHandle(conn net.Conn, domain string, port uint16) (err error) {
	start := time.Now()
	defer func() {
		deferlog.DebugWarn(err, "route handle", "domain", domain, "port", port, "took", time.Since(start))
	}()

	rc, err := r.Dial("tcp", domain, port)
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
	addr := net.JoinHostPort(domain, strconv.FormatUint(uint64(port), 10))

	// 1. rule_based( block > direct > proxy )
	// 2. detect_based( CN IP || access site )
	// 3. fallback( proxy )
	switch {
	case r.BlockRule.Match(domain):
		return nil, ErrBlocked
	case r.DirectRule.Match(domain):
		return r.directDial(network, addr)
	case r.ProxyRule.Match(domain):
		return r.proxyDial(network, domain, port)
	case r.localSite(domain), r.isAccess(domain, port):
		return r.directDial(network, addr)
	default:
		return r.proxyDial(network, domain, port)
	}
}

func (r *Router) proxyDial(network, domain string, port uint16) (net.Conn, error) {
	start := time.Now()
	rc, err := r.ProxyDial(network, domain, port)
	if err != nil {
		return nil, fmt.Errorf("proxy dial %s:%d, spend (%s): %w", domain, port, time.Since(start), err)
	}
	return rc, nil
}

func (r *Router) directDial(network, addr string) (net.Conn, error) {
	start := time.Now()
	conn, err := net.DialTimeout(network, addr, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("direct dial %s, spend (%s): %w", addr, time.Since(start), err)
	}
	return conn, nil
}
