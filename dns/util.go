package dns

import (
	"net"

	"github.com/golang/glog"
	"github.com/miekg/dns"
	"github.com/wweir/sower/conf"
	"github.com/wweir/sower/util"
)

var (
	blockList   *util.Node
	whiteList   *util.Node
	suggestList *util.Node
)

func init() {
	host, _, _ := net.SplitHostPort(conf.Conf.ServerAddr)

	//first init
	blockList = loadRules("block", conf.Conf.BlockList)
	suggestList = loadRules("suggest", conf.Conf.Suggestions)
	whiteList = loadRules("white", conf.Conf.WhiteList)
	whiteList.Add(host)

	conf.OnRefreash = append(conf.OnRefreash, func() error {
		blockList = loadRules("block", conf.Conf.BlockList)
		suggestList = loadRules("suggest", conf.Conf.Suggestions)
		whiteList = loadRules("white", conf.Conf.WhiteList)
		whiteList.Add(host)
		return nil
	})
}

func loadRules(name string, list []string) *util.Node {
	rule := util.NewNodeFromRules(".", list...)
	glog.V(3).Infof("load %s rule:\n%s", name, rule)
	return rule
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
