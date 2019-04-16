package dns

import (
	"net"

	"github.com/golang/glog"
	"github.com/miekg/dns"
	"github.com/wweir/sower/util"
)

var (
	blockList   *util.Node
	suggestList *util.Node
	whiteList   *util.Node
)

// LoadRules init rules from config
func LoadRules(blocklist, suggestions, whitelist []string, host string) {
	blockList = loadRules("block", blocklist)
	suggestList = loadRules("suggest", suggestions)
	whiteList = loadRules("white", whitelist)
	whiteList.Add(host)
	glog.V(1).Infoln("reloaded config")
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

//go:generate stringer -type=level $GOFILE
type level int32

const (
	DISABLE level = iota
	BLOCK
	SPEEDUP
	levelEnd
)

func ListSuggestLevels() []string {
	list := make([]string, 0, int(levelEnd))
	for i := level(0); i < levelEnd; i++ {
		list = append(list, i.String())
	}
	return list
}

func parseSuggestLevel(suggestLevel string) level {
	for i := level(0); i < levelEnd; i++ {
		if suggestLevel == i.String() {
			return i
		}
	}

	glog.Exitln("invalid suggest level: " + suggestLevel)
	return levelEnd
}
