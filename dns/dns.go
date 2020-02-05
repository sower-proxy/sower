package dns

/*
 * Deep integration with package conf: github.com/wweir/sower/conf
 */
import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/miekg/dns"
	"github.com/wweir/sower/conf"
	"github.com/wweir/sower/internal/http"
	internal_net "github.com/wweir/sower/internal/net"
	"github.com/wweir/sower/internal/socks5"
	"github.com/wweir/utils/log"
	"github.com/wweir/utils/mem"
)

func ServeDNS() {
	serveIP := net.ParseIP(conf.Conf.Downstream.ServeIP)
	if conf.Conf.Downstream.ServeIP == "" || serveIP.String() != conf.Conf.Downstream.ServeIP {
		log.Fatalw("invalid listen ip", "ip", conf.Conf.Downstream.ServeIP)
	}
	dnsServer, err := PickUpstreamDNS(serveIP.String(), conf.Conf.Upstream.DNS)
	if err != nil {
		log.Fatalw("")
	}

	d := &detect{proxy: conf.Conf.Upstream.Socks5}

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

		if err := matchAndServe(w, r, serveIP, d, domain, dnsServer); err != nil {
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
		return internal_net.GetDefaultDNSServer()
	}

	if _, port, err := net.SplitHostPort(dnsServer); err != nil {
		return "", fmt.Errorf("parse upstream dns(%s) server fail: %w", dnsServer, err)
	} else if port == "" {
		return net.JoinHostPort(dnsServer, "53"), nil
	}
	return dnsServer, nil
}

const timeout = 200 * time.Millisecond

func matchAndServe(w dns.ResponseWriter, r *dns.Msg, serveIP net.IP, d *detect, domain, dnsServer string) error {
	if (!whiteList.Match(domain)) &&
		(blockList.Match(domain) || suggestList.Match(domain)) {
		w.WriteMsg(localA(r, domain, serveIP))
		return nil
	}

	if err := mem.Remember(d, domain); err != nil {
		panic(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	msg, err := dns.ExchangeContext(ctx, r, dnsServer)
	if err != nil || msg == nil {
		return err
	}

	w.WriteMsg(msg)
	return nil
}

type detect struct {
	proxy string
	port  http.Port
}

func (d *detect) Get(key interface{}) error {
	// break deadloop, for ugly wildcard setting dns setting
	domain := strings.TrimSuffix(key.(string), ".")
	if strings.Count(domain, ".") > 10 {
		return nil
	}

	wg := sync.WaitGroup{}
	httpScore, httpsScore := new(int32), new(int32)
	for _, ping := range [...]detect{{"", http.HTTP}, {"", http.HTTPS}} {
		wg.Add(1)
		go func(ping detect) {
			defer wg.Done()

			if err := ping.port.Ping(domain, timeout); err != nil {
				return
			}

			switch ping.port {
			case http.HTTP:
				if !atomic.CompareAndSwapInt32(httpScore, 0, 2) {
					atomic.AddInt32(httpScore, 1)
				}
			case http.HTTPS:
				if !atomic.CompareAndSwapInt32(httpsScore, 0, 2) {
					atomic.AddInt32(httpScore, 1)
				}
			}
		}(ping)
	}
	for _, ping := range [...]detect{{d.proxy, http.HTTP}, {d.proxy, http.HTTPS}} {
		wg.Add(1)
		go func(ping detect) {
			defer wg.Done()

			if conn, err := net.Dial("tcp", ping.proxy); err != nil {
				return
			} else {
				conn = socks5.ToSocks5(conn, domain, ping.port.String())
				if err := ping.port.PingWithConn(domain, conn, timeout); err != nil {
					return
				}
			}

			switch ping.port {
			case http.HTTP:
				if !atomic.CompareAndSwapInt32(httpScore, 0, -2) {
					atomic.AddInt32(httpScore, -1)
				}
			case http.HTTPS:
				if !atomic.CompareAndSwapInt32(httpsScore, 0, -2) {
					atomic.AddInt32(httpScore, -1)
				}
			}
		}(ping)
	}

	wg.Wait()
	if int(*httpScore+*httpsScore) >= conf.Conf.Router.ProxyLevel {
		conf.AddDynamic(domain)
	}
	return nil
}
