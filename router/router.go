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

type ProxyDialFn func(network, host string, port uint16) (net.Conn, error)
type Router struct {
	blockRule  *util.Node
	directRule *util.Node
	proxyRule  *util.Node
	ProxyDial  ProxyDialFn
	cache      *mem.Cache

	dns struct {
		fallbackDNS string
		dns.Client
		connCh chan *dns.Conn
	}

	mmdb struct {
		*geoip2.Reader
		cidrs []*net.IPNet
	}
}

func NewRouter(fallbackDNS, mmdbFile string, proxyDial ProxyDialFn,
	blockList, directList, proxyList, directCIDRs []string) *Router {

	r := Router{
		blockRule:  util.NewNodeFromRules(blockList...),
		directRule: util.NewNodeFromRules(directList...),
		proxyRule:  util.NewNodeFromRules(proxyList...),
		ProxyDial:  proxyDial,
		cache:      mem.New(time.Hour), // TODO: config
	}

	r.dns.fallbackDNS = fallbackDNS
	r.dns.connCh = make(chan *dns.Conn, 1)
	go r.dialDNSConn()

	var err error
	r.mmdb.Reader, err = geoip2.Open(mmdbFile)
	log.Err(err).Str("file", mmdbFile).Msg("open geoip2 db")
	r.mmdb.cidrs = make([]*net.IPNet, 0, len(directCIDRs))
	for _, cidr := range directCIDRs {
		_, ipnet, err := net.ParseCIDR(cidr)
		if err != nil {
			log.Error().Err(err).Msg("Failed to parse CIDR")
		}
		r.mmdb.cidrs = append(r.mmdb.cidrs, ipnet)
	}
	return &r
}

func (r *Router) dialDNSConn() {
	for {
		server, err := dhcp.GetDNSServer()
		if server == "" {
			server = r.dns.fallbackDNS
		}
		log.Err(err).
			Str("DNS", server).
			Msg("get DNS server")

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
	case r.isAccess(domain, port):
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
