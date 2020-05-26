package proxy

import (
	"net"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
	"github.com/wweir/sower/dhcp"
	"github.com/wweir/utils/log"
)

type msgCache struct {
	*dns.Msg
	time.Time
}

var cache sync.Map

func StartDNS(redirectIP, relayServer string, shouldProxy func(string) bool) {
	serveIP := net.ParseIP(redirectIP)
	if redirectIP == "" || serveIP.String() != redirectIP {
		log.Fatalw("invalid listen ip", "ip", redirectIP)
	}

	var err error
	if relayServer, err = pickRelayAddr(relayServer); err != nil {
		log.Fatalw("pick upstream dns server", "err", err)
	}
	log.Infow("upstream dns", "addr", relayServer)

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
		if idx := strings.IndexByte(domain, ':'); idx > 0 {
			domain = domain[:idx] // trim port
		}

		if shouldProxy(domain) {
			w.WriteMsg(localA(r, domain, serveIP))

		} else if val, ok := cache.Load(domain); ok && val.(*msgCache).After(time.Now()) {
			msg := val.(*msgCache)
			for _, rr := range msg.Answer {
				rr.Header().Ttl = uint32(time.Until(msg.Time).Seconds())
			}
			msg.SetReply(r)
			w.WriteMsg(msg.Msg)

		} else if msg, err := dns.Exchange(r, relayServer); err != nil || msg == nil {
			cache.Delete(domain)
			if server, err := pickRelayAddr(relayServer); err != nil {
				log.Errorw("detect upstream dns", "err", err)
			} else if relayServer != server {
				relayServer = server
				log.Infow("detect upstream dns", "addr", relayServer)
			}

		} else {
			cache.Delete(domain)
			if len(msg.Answer) != 0 {
				deadline := time.Now().Add(
					time.Duration(msg.Answer[0].Header().Ttl) * time.Second)
				cache.Store(domain, &msgCache{
					Msg:  msg,
					Time: deadline,
				})
			}

			w.WriteMsg(msg)
		}
	})

	server := &dns.Server{Addr: net.JoinHostPort(redirectIP, "53"), Net: "udp"}
	log.Infow("start dns", "addr", server.Addr)
	log.Fatalw("dns serve fail", "err", server.ListenAndServe())
}

func pickRelayAddr(relayServer string) (_ string, err error) {
	if relayServer == "" {
		if relayServer, err = dhcp.GetDefaultDNSServer(); err != nil {
			return "", err
		}
	}

	if _, _, err := net.SplitHostPort(relayServer); err != nil {
		return net.JoinHostPort(relayServer, "53"), nil
	}
	return relayServer, nil
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
