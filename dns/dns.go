package dns

import (
	"net"
	"strings"
	"time"

	"github.com/golang/glog"
	"github.com/miekg/dns"
)

func StartDNS(dnsServer string, blocklist []string) {
	var handle func(w dns.ResponseWriter, r *dns.Msg, domain, dnsServer string)
	if len(blocklist) == 0 {
		handle = bestTry
	} else {
		initRule(blocklist)
		handle = manual
	}

	dns.HandleFunc(".", func(w dns.ResponseWriter, r *dns.Msg) {
		// *Msg r has an TSIG record and it was validated
		if r.IsTsig() != nil && w.TsigStatus() == nil {
			r.SetTsig(r.Extra[len(r.Extra)-1].(*dns.TSIG).Hdr.Name, dns.HmacMD5, 300, time.Now().Unix())
		}

		//https://stackoverflow.com/questions/4082081/requesting-a-and-aaaa-records-in-single-dns-query/4083071#4083071
		if len(r.Question) == 0 {
			return
		}

		handle(w, r, r.Question[0].Name, dnsServer)
	})

	server := &dns.Server{Addr: ":53", Net: "udp"}
	server.TsigSecret = map[string]string{"axfr.": "so6ZGir4GPAqINNh9U5c3A=="}
	glog.Fatalln(server.ListenAndServe())
}

// TODO: delete me
func bestTry(w dns.ResponseWriter, r *dns.Msg, domain, dnsServer string) {
	msg, err := dns.Exchange(r, dnsServer+":53")
	if err != nil || len(msg.Answer) == 0 {
		w.WriteMsg(localA(r, domain))
		return
	}

	var ip string
	switch msg.Answer[0].(type) {
	case *dns.A:
		ip = msg.Answer[0].(*dns.A).A.String()
	case *dns.AAAA:
		ip = "[" + msg.Answer[0].(*dns.AAAA).AAAA.String() + "]"
	default:
		w.WriteMsg(msg)
	}

	if _, err := net.DialTimeout("tcp", ip+":http", time.Second); err != nil {
		glog.V(2).Infoln(ip+":80", err)
		w.WriteMsg(localA(r, domain))
		return
	}
	w.WriteMsg(msg)
}

var rule *Node

func initRule(blocklist []string) {
	rule = NewNode()

	for i := range blocklist {
		rule.Add(strings.Split(blocklist[i], "."))
	}
	glog.V(2).Infof("block rule:\n%s", rule)
}

func manual(w dns.ResponseWriter, r *dns.Msg, domain, dnsServer string) {
	if rule.Match(strings.TrimSuffix(domain, ".")) {
		glog.V(2).Infof("match %s suss", domain)
		w.WriteMsg(localA(r, domain))
		return
	}
	glog.V(2).Infof("match %s fail", domain)

	msg, err := dns.Exchange(r, dnsServer+":53")
	if err != nil || len(msg.Answer) == 0 {
		w.WriteMsg(localA(r, domain))
		return
	}
	w.WriteMsg(msg)
}

func localA(r *dns.Msg, domain string) *dns.Msg {
	m := new(dns.Msg)
	m.SetReply(r)
	m.Answer = []dns.RR{&dns.A{
		Hdr: dns.RR_Header{Name: domain, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 0},
		A:   net.IPv4(127, 0, 0, 1),
	}}
	return m
}
