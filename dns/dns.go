package dns

import (
	"net"
	"strings"
	"sync/atomic"
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
	suggest := &intelliSuggest{listenIP, 2 * time.Second}
	mem.DefaultCache = mem.New(time.Hour)
	var dhcpCh chan struct{}
	if dnsServer != "" {
		dnsServer = net.JoinHostPort(dnsServer, "53")
	} else {
		dhcpCh = make(chan struct{})
		go func() {
			for {
				<-dhcpCh
				host := GetDefaultDNSServer()
				if host == "" {
					continue
				}
				// atomic action
				dnsServer = net.JoinHostPort(host, "53")
				glog.Infoln("set dns server to", host)
			}
		}()
	}

	dns.HandleFunc(".", func(w dns.ResponseWriter, r *dns.Msg) {
		// *Msg r has an TSIG record and it was validated
		if r.IsTsig() != nil && w.TsigStatus() == nil {
			lastTsig := r.Extra[len(r.Extra)-1].(*dns.TSIG)
			r.SetTsig(lastTsig.Hdr.Name, dns.HmacMD5, 300, time.Now().Unix())
		}

		//https://stackoverflow.com/questions/4082081/requesting-a-and-aaaa-records-in-single-dns-query/4083071#4083071
		if len(r.Question) == 0 {
			return
		}

		domain := r.Question[0].Name
		if idx := strings.IndexByte(domain, colon); idx > 0 {
			domain = domain[:idx]
		}

		matchAndServe(w, r, domain, listenIP, dnsServer, dhcpCh, ip, suggest)
	})

	server := &dns.Server{Addr: net.JoinHostPort(listenIP, "53"), Net: "udp"}
	glog.Fatalln(server.ListenAndServe())
}

func matchAndServe(w dns.ResponseWriter, r *dns.Msg, domain, listenIP, dnsServer string,
	dhcpCh chan struct{}, ipNet net.IP, suggest *intelliSuggest) {

	inWriteList := whiteList.Match(domain)

	if !inWriteList && (blockList.Match(domain) || suggestList.Match(domain)) {
		glog.V(2).Infof("match %s suss", domain)
		w.WriteMsg(localA(r, domain, ipNet))
		return
	}

	if !inWriteList {
		go mem.Remember(suggest, domain)
	}

	msg, err := dns.Exchange(r, dnsServer)
	if err != nil && dhcpCh != nil {
		select {
		case dhcpCh <- struct{}{}:
		default:
		}
	}
	if msg == nil { // expose any response except nil
		glog.V(1).Infof("get dns of %s fail: %s", domain, err)
		return
	}
	w.WriteMsg(msg)
}

type intelliSuggest struct {
	listenIP string
	timeout  time.Duration
}

func (i *intelliSuggest) GetOne(domain interface{}) (iface interface{}, e error) {
	iface, e = struct{}{}, nil

	// kill deadloop, for ugly wildcard setting dns setting
	addr := strings.TrimSuffix(domain.(string), ".")
	if len(strings.Split(addr, ".")) > 10 {
		return
	}

	// give local dial a hand, make it not so easy to be added into suggestions
	util.HTTPPing(addr, addr, util.Http, i.timeout/50)

	pings := []struct {
		viaAddr string
		port    util.Port
	}{
		{addr, util.Http},
		{addr, util.Https},
		{i.listenIP, util.Http},
		{i.listenIP, util.Https},
	}
	var finish = new(uint32)
	for idx := range pings {
		go func(idx int) {
			err := util.HTTPPing(pings[idx].viaAddr, addr, pings[idx].port, i.timeout)
			if err != nil {
				if atomic.LoadUint32(finish) == 0 { // fails before first succ
					glog.V(1).Infof("PING %s via %s fail: %s", addr, pings[idx].viaAddr, err)
				}
			} else if atomic.CompareAndSwapUint32(finish, 0, 1) { // first succ
				if pings[idx].viaAddr == i.listenIP {
					conf.AddSuggest(addr)
					glog.Infof("added suggest domain: %s\t via: %s", addr, pings[idx].viaAddr)
				}
			}
		}(idx)
	}
	return
}
