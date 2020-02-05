package dns

import (
	"net"

	"github.com/miekg/dns"
	"github.com/wweir/sower/conf"
	"github.com/wweir/sower/util"
	"github.com/wweir/utils/log"
)

var blockList *util.Node
var suggestList *util.Node
var whiteList *util.Node

func init() {
	reloadFn := func() error {
		whiteList = util.NewNodeFromRules(".", conf.Conf.Router.DirectList...)
		blockList = util.NewNodeFromRules(".", conf.Conf.Router.ProxyList...)
		suggestList = util.NewNodeFromRules(".", conf.Conf.Router.DynamicList...)
		log.Infow("reload config rules")
		return nil
	}

	reloadFn()
	conf.AddReloadConfigHook("reload rules", reloadFn)
}

func localA(r *dns.Msg, domain string, localIP net.IP) *dns.Msg {
	m := new(dns.Msg)
	m.SetReply(r)
	if localIP.To4() != nil {
		m.Answer = []dns.RR{&dns.A{
			Hdr: dns.RR_Header{Name: domain, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 20},
			A:   localIP,
		}}
	} else {
		m.Answer = []dns.RR{&dns.AAAA{
			Hdr:  dns.RR_Header{Name: domain, Rrtype: dns.TypeAAAA, Class: dns.ClassINET, Ttl: 20},
			AAAA: localIP,
		}}
	}
	return m
}
