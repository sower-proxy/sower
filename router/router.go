package router

import (
	"net"
	"strconv"
	"time"

	"github.com/miekg/dns"
	geoip2 "github.com/oschwald/geoip2-golang"
	"github.com/pkg/errors"
	"github.com/sower-proxy/conns/relay"
	"github.com/sower-proxy/deferlog"
	"github.com/sower-proxy/deferlog/log"
	"github.com/wweir/sower/pkg/suffixtree"
)

type ProxyDialFn func(network, host string, port uint16) (net.Conn, error)
type Router struct {
	BlockRule  *suffixtree.Node
	DirectRule *suffixtree.Node
	ProxyRule  *suffixtree.Node
	ProxyDial  ProxyDialFn

	dns struct {
		dns.Client
		fallbackDNS string
		serveIP     net.IP
	}

	country struct {
		*geoip2.Reader
		cidrs []*net.IPNet
	}
}

func NewRouter(serveIP, fallbackDNS, mmdbFile string, proxyDial ProxyDialFn) *Router {
	r := Router{
		ProxyDial: proxyDial,
	}

	r.dns.serveIP = net.ParseIP(serveIP)
	r.dns.fallbackDNS = fallbackDNS

	var err error
	r.country.Reader, err = geoip2.Open(mmdbFile)
	log.InfoWarn(err).Str("file", mmdbFile).Msg("open geoip2 db")

	return &r
}

func (r *Router) AddCountryCIDRs(cidrs ...string) {
	for _, cidr := range cidrs {
		_, ipnet, err := net.ParseCIDR(cidr)
		if err != nil {
			log.Error().Err(err).Msg("Failed to parse CIDR")
		}
		r.country.cidrs = append(r.country.cidrs, ipnet)
	}
	r.country.cidrs = suffixtree.GCSlice(r.country.cidrs)
}

func (r *Router) RouteHandle(conn net.Conn, domain string, port uint16) (err error) {
	start := time.Now()
	defer func() {
		deferlog.DebugWarn(err).
			Str("domain", domain).
			Uint16("port", port).
			Dur("spend", time.Since(start)).
			Msg("serve socks5")
	}()

	addr := net.JoinHostPort(domain, strconv.FormatUint(uint64(port), 10))

	// 1. rule_based( block > direct > proxy )
	// 2. detect_based( CN IP || access site )
	// 3. fallback( proxy )
	switch {
	case r.BlockRule.Match(domain):
		return nil

	case r.DirectRule.Match(domain):
		return r.DirectHandle(conn, addr)

	case r.ProxyRule.Match(domain):
		return r.ProxyHandle(conn, domain, port)

	case r.localSite(domain), r.isAccess(domain, port):
		return r.DirectHandle(conn, addr)
	default:
		return r.ProxyHandle(conn, domain, port)
	}
}

func (r *Router) ProxyHandle(conn net.Conn, domain string, port uint16) error {
	start := time.Now()
	rc, err := r.ProxyDial("tcp", domain, port)
	if err != nil {
		return errors.Wrapf(err, "proxy dial (%s:%d), spend (%s)", domain, port, time.Since(start))
	}
	defer rc.Close()

	_ = relay.Relay(conn, rc)
	return nil
}

func (r *Router) DirectHandle(conn net.Conn, addr string) error {
	dur, err := relay.RelayTo(conn, addr)
	return errors.Wrapf(err, "spend (%s)", dur)
}
