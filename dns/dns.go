package dns

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/golang/glog"
	"github.com/miekg/dns"
	mem "github.com/wweir/mem-go"
)

const colon = byte(':')

func StartDNS(dnsServer, listenIP string, suggestCh chan<- string, suggestLevel string) {
	ip := net.ParseIP(listenIP)

	suggest := &intelliSuggest{suggestCh, parseSuggestLevel(suggestLevel), listenIP, time.Second}
	mem.DefaultCache = mem.New(time.Hour)

	dhcpCh := make(chan struct{})
	if dnsServer != "" {
		dnsServer = net.JoinHostPort(dnsServer, "53")
	} else {
		go dynamicSetUpstreamDNS(listenIP, &dnsServer, dhcpCh)
		dhcpCh <- struct{}{}
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
			domain = domain[:idx] // trim port
		}

		matchAndServe(w, r, domain, listenIP, dnsServer, dhcpCh, ip, suggest)
	})

	server := &dns.Server{Addr: net.JoinHostPort(listenIP, "53"), Net: "udp"}
	glog.Fatalln(server.ListenAndServe())
}

func dynamicSetUpstreamDNS(listenIP string, dnsServer *string, dhcpCh <-chan struct{}) {
	addr, _ := dns.ReverseAddr(listenIP)
	msg := &dns.Msg{
		MsgHdr: dns.MsgHdr{
			Id:               dns.Id(),
			RecursionDesired: false,
		},
		Question: []dns.Question{{
			Name:   addr,
			Qtype:  dns.TypeA,
			Qclass: dns.ClassINET,
		}},
	}

	for {
		<-dhcpCh
		if _, err := dns.Exchange(msg, *dnsServer); err == nil {
			continue
		}

		host, err := GetDefaultDNSServer()
		if err != nil {
			glog.Errorln(err)
			continue
		}

		// atomic action
		*dnsServer = net.JoinHostPort(host, "53")
		glog.Infoln("set dns server to", host)
	}
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
		go func() {
			ip, err := net.LookupIP(domain)
			if err != nil || len(ip) == 0 {
				glog.V(1).Infoln(ip, err)
				return
			}

			mem.Remember(suggest, addr{domain, ip[0].String()})
		}()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	msg, err := dns.ExchangeContext(ctx, r, dnsServer)
	if err != nil {
		if dhcpCh != nil {
			select {
			case dhcpCh <- struct{}{}:
			default:
			}
		}
		glog.V(1).Infof("get dns of %s from %s fail: %s", domain, dnsServer, err)
		return
	} else if msg == nil { // expose any response except nil
		glog.V(1).Infof("get dns of %s from %s return empty", domain, dnsServer)
		return
	}

	w.WriteMsg(msg)
}

type intelliSuggest struct {
	suggestCh    chan<- string
	suggestLevel suggestLevel
	listenIP     string
	timeout      time.Duration
}
type addr struct {
	domain string
	ip     string
}

func (i *intelliSuggest) GetOne(key interface{}) (iface interface{}, e error) {
	iface, e = struct{}{}, nil
	if i.suggestLevel == DISABLE {
		return
	}

	domain := key.(addr).domain
	ip := key.(addr).ip

	// kill deadloop, for ugly wildcard setting dns setting
	domain = strings.TrimSuffix(domain, ".")
	if strings.Count(domain, ".") > 10 {
		return
	}

	var (
		pings = [...]struct {
			viaAddr string
			port    Port
		}{
			{ip, HTTP},
			{i.listenIP, HTTP},
			{ip, HTTPS},
			{i.listenIP, HTTPS},
		}
		protos = [...]*int32{
			new(int32), /*HTTP*/
			new(int32), /*HTTPS*/
		}
		score = new(int32)
	)
	for idx := range pings {
		go func(idx int) {
			if err := HTTPPing(pings[idx].viaAddr, domain, pings[idx].port, i.timeout); err != nil {
				// local ping fail
				if pings[idx].viaAddr == ip {
					atomic.AddInt32(score, 1)
					glog.V(1).Infof("local ping %s fail", domain)
				} else {
					atomic.AddInt32(score, -1)
					glog.V(1).Infof("remote ping %s fail", domain)
				}

				// remote ping faster
			} else if pings[idx].viaAddr == i.listenIP {
				if atomic.CompareAndSwapInt32(protos[idx/2], 0, 1) && i.suggestLevel == SPEEDUP {
					atomic.AddInt32(score, 1)
				}
				glog.V(1).Infof("remote ping %s faster", domain)

			} else {
				atomic.CompareAndSwapInt32(protos[idx/2], 0, 2)
				return // score change trigger add suggestion
			}

			// check all remote pings are faster
			if atomic.LoadInt32(score) == int32(len(protos)) {
				for i := range protos {
					if atomic.LoadInt32(protos[i]) != 1 {
						return
					}
				}
			}

			// 1. local fail and remote success
			// 2. all remote pings are faster
			if atomic.LoadInt32(score) >= int32(len(protos)) {
				old := atomic.SwapInt32(score, -1) // avoid readd the suggestion
				i.suggestCh <- domain
				glog.Infof("suggested domain: %s with score: %d", domain, old)
			}
		}(idx)
	}
	return
}
