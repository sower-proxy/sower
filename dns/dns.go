package dns

import (
	"net"
	"strings"
	"time"

	"github.com/golang/glog"
	"github.com/miekg/dns"
	mem "github.com/wweir/mem-go"
	"github.com/wweir/sower/conf"
	"github.com/wweir/sower/util"
)

const colon = byte(':')

func StartDNS(dnsServer, listenIP string) {
	ip := net.ParseIP(listenIP)
	suggest := &intelliSuggest{listenIP, 2 * time.Second, []string{":80", ":443"}}
	mem.DefaultCache = mem.New(time.Hour)

	dns.HandleFunc(".", func(w dns.ResponseWriter, r *dns.Msg) {
		// *Msg r has an TSIG record and it was validated
		if r.IsTsig() != nil && w.TsigStatus() == nil {
			r.SetTsig(r.Extra[len(r.Extra)-1].(*dns.TSIG).Hdr.Name, dns.HmacMD5, 300, time.Now().Unix())
		}

		//https://stackoverflow.com/questions/4082081/requesting-a-and-aaaa-records-in-single-dns-query/4083071#4083071
		if len(r.Question) == 0 {
			return
		}

		domain := r.Question[0].Name
		if idx := strings.IndexByte(domain, colon); idx > 0 {
			domain = domain[:idx]
		}

		matchAndServe(w, r, domain, listenIP, dnsServer, ip, suggest)
	})

	server := &dns.Server{Addr: listenIP + ":53", Net: "udp"}
	glog.Fatalln(server.ListenAndServe())
}

func matchAndServe(w dns.ResponseWriter, r *dns.Msg, domain, listenIP,
	dnsServer string, ipNet net.IP, suggest *intelliSuggest) {

	inWriteList := whiteList.Match(domain)

	if !inWriteList && (blockList.Match(domain) || suggestList.Match(domain)) {
		glog.V(2).Infof("match %s suss", domain)
		w.WriteMsg(localA(r, domain, ipNet))
		return
	}

	if !inWriteList {
		go mem.Remember(suggest, domain)
	}

	msg, err := dns.Exchange(r, dnsServer+":53")
	if msg == nil { // expose any response except nil
		glog.V(1).Infof("get dns of %s fail: %s", domain, err)
		return
	}
	w.WriteMsg(msg)
}

type intelliSuggest struct {
	listenIP string
	timeout  time.Duration
	ports    []string
}

func (i *intelliSuggest) GetOne(domain interface{}) (iface interface{}, e error) {
	iface = struct{}{}

	// kill deadloop, for ugly wildcard setting dns setting
	addr := strings.TrimSuffix(domain.(string), ".")
	if len(strings.Split(addr, ".")) > 10 {
		return
	}

	for _, port := range i.ports {
		// give local dial a hand, make it not so easy to be added into suggestions
		util.HTTPPing(addr+port, addr, i.timeout/10)
		localCh := util.HTTPPing(addr+port, addr, i.timeout)
		remoteCh := util.HTTPPing(i.listenIP+port, addr, i.timeout)

		select {
		case err := <-localCh:
			if err == nil {
				glog.V(2).Infoln(addr, "local first, succ")
				return
			}
			if err = <-remoteCh; err != nil {
				glog.V(2).Infoln(addr, "local first, all fail")
				return
			}
			glog.V(2).Infoln(addr, "local first, remote succ")
			conf.AddSuggest(addr)

		case err := <-remoteCh:
			if err != nil {
				glog.V(2).Infoln(addr, "remote first, fail")
				return
			}
			glog.V(2).Infoln(addr, "remote first, succ")
			conf.AddSuggest(addr)
		}

		glog.Infof("added suggest domain: %s", addr)
		return
	}
	return
}
