package dns

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/miekg/dns"
	"github.com/wweir/sower/conf"
	_net "github.com/wweir/sower/internal/net"
	"github.com/wweir/utils/log"
)

func ServeDNS(redirectIP, relayServer string) {
	serveIP := net.ParseIP(redirectIP)
	if redirectIP == "" || serveIP.String() != redirectIP {
		log.Fatalw("invalid listen ip", "ip", redirectIP)
	}
	dnsServer, err := PickUpstreamDNS(serveIP.String(), relayServer)
	if err != nil {
		log.Fatalw("")
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
		if idx := strings.IndexByte(domain, ':'); idx > 0 {
			domain = domain[:idx] // trim port
		}

		if err := matchAndServe(w, r, serveIP, domain, dnsServer); err != nil {
			server, err := PickUpstreamDNS(serveIP.String(), dnsServer)
			if err != nil {
				log.Errorw("detect upstream dns fail", "err", err)
			} else {
				dnsServer = server
			}
		}
	})

	server := &dns.Server{Addr: ":53", Net: "udp"}
	log.Fatalw("dns serve fail", "err", server.ListenAndServe())
}

func PickUpstreamDNS(listenIP string, dnsServer string) (string, error) {
	if dnsServer == "" {
		return _net.GetDefaultDNSServer()
	}

	if _, port, err := net.SplitHostPort(dnsServer); err != nil {
		return "", fmt.Errorf("parse upstream dns(%s) server fail: %w", dnsServer, err)
	} else if port == "" {
		return net.JoinHostPort(dnsServer, "53"), nil
	}
	return dnsServer, nil
}

func matchAndServe(w dns.ResponseWriter, r *dns.Msg, serveIP net.IP, domain, dnsServer string) error {
	if conf.ShouldProxy(domain) {
		w.WriteMsg(localA(r, domain, serveIP))
		return nil
	}

	msg, err := dns.Exchange(r, dnsServer)
	if err != nil || msg == nil {
		return err
	}

	w.WriteMsg(msg)
	return nil
}
