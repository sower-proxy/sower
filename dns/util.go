package dns

import (
	"net"

	"github.com/golang/glog"
	"github.com/miekg/dns"
	"github.com/wweir/sower/conf"
)

var (
	blockList   *Node
	whiteList   *Node
	suggestList *Node
)

func init() {
	//first init
	blockList = loadRules("block", conf.Conf.BlockList)
	whiteList = loadRules("white", conf.Conf.WhiteList)
	suggestList = loadRules("suggest", conf.Conf.BlockList)

	conf.OnRefreash = append(conf.OnRefreash, func() error {
		blockList = loadRules("block", conf.Conf.BlockList)
		whiteList = loadRules("white", conf.Conf.WhiteList)
		suggestList = loadRules("suggest", conf.Conf.Suggestions)
		return nil
	})
}

func loadRules(name string, list []string) *Node {
	rule := NewNodeFromRules(".", list...)
	glog.V(2).Infof("load %s rule:\n%s", name, rule)
	return rule
}

func localA(r *dns.Msg, domain string, localIP net.IP) *dns.Msg {
	m := new(dns.Msg)
	m.SetReply(r)
	m.Answer = []dns.RR{&dns.A{
		Hdr: dns.RR_Header{Name: domain, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 20},
		A:   localIP,
	}}
	return m
}
