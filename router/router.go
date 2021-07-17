package router

import (
	"net"
	"strconv"
	"time"

	"github.com/miekg/dns"
	geoip2 "github.com/oschwald/geoip2-golang"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/wweir/sower/pkg/dhcp"
	"github.com/wweir/sower/pkg/mem"
	"github.com/wweir/sower/util"
)

type Router struct {
	blockRule  *util.Node
	directRule *util.Node
	proxyRule  *util.Node

	ProxyDial func(network, host string, port uint16) (net.Conn, error)
	cache     *mem.Cache

	dns struct {
		dns.Client
		connCh chan *dns.Conn
	}

	mmdb struct {
		*geoip2.Reader
		*net.Resolver

		cidrs []*net.IPNet
	}
}

func NewRouter(proxyDial func(network, host string, port uint16) (net.Conn, error)) *Router {
	r := Router{
		blockRule:  util.NewNodeFromRules(),
		directRule: util.NewNodeFromRules(),
		proxyRule:  util.NewNodeFromRules("google.*"),
		ProxyDial:  proxyDial,
		cache:      mem.New(time.Hour), // TODO: config
	}

	r.dns.connCh = make(chan *dns.Conn, 1)
	go r.dialDNSConn()

	return &r
}

func (r *Router) dialDNSConn() {
	for {
		server, err := dhcp.GetDNSServer()
		if err != nil {
			time.Sleep(time.Second)
			continue
		}

		log.Info().
			Str("ip", server).
			Msg("get upstream dns server")

		for {
			conn, err := dns.DialTimeout("udp", net.JoinHostPort(server, "53"), time.Second)
			if err != nil {
				log.Error().Err(err).Str("ip", server).Msg("dial dns server")
				break
			}

			r.dns.connCh <- conn
		}
	}
}

func (r *Router) RouteHandle(conn net.Conn, domain string, port uint16) error {
	addr := net.JoinHostPort(domain, strconv.FormatUint(uint64(port), 10))

	switch {
	case r.blockRule.Match(domain):
		return nil

	case r.directRule.Match(domain):
		return r.DirectHandle(conn, addr)

	case r.proxyRule.Match(domain):
		return r.ProxyHandle(conn, domain, port)

	case r.localSite(domain):
		return r.DirectHandle(conn, addr)
	case r.isAccess(domain):
		return r.DirectHandle(conn, addr)
	case port == 80:
		return r.DirectHandle(conn, addr)
	case port == 443:
		return r.DirectHandle(conn, addr)
	default:
		return r.ProxyHandle(conn, domain, port)
	}
}

func (r *Router) ProxyHandle(conn net.Conn, domain string, port uint16) error {
	start := time.Now()
	rc, err := r.ProxyDial("tcp", domain, port)
	if err != nil {
		return errors.Wrapf(err, "dial proxy to (%s:%d), spend (%s)", domain, port, time.Since(start))
	}
	defer rc.Close()

	util.Relay(conn, rc)
	return nil
}

func (r *Router) DirectHandle(conn net.Conn, addr string) error {
	dur, err := util.RelayTo(conn, addr)
	return errors.Wrapf(err, "direct relay to (%s), spend (%s)", addr, dur)
}
