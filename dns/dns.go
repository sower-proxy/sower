package dns

import (
	"io"
	"net"
	"strings"
	"time"

	"github.com/golang/glog"
	"github.com/miekg/dns"
	mem "github.com/wweir/mem-go"
	"github.com/wweir/sower/conf"
)

const colon = byte(':')

func StartDNS(dnsServer, listenIP string) {
	ip := net.ParseIP(listenIP)
	suggest := &intelliSuggest{listenIP, 2 * time.Second}
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

func matchAndServe(w dns.ResponseWriter, r *dns.Msg, domain, listenIP, dnsServer string, ipNet net.IP, suggest *intelliSuggest) {
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
}

func (i *intelliSuggest) GetOne(domain interface{}) (interface{}, error) {
	addr := strings.TrimSuffix(domain.(string), ".")

	{ // First: test direct connect
		conn, err := net.DialTimeout("tcp", addr+":http", i.timeout)
		if err == nil {
			conn.Close()
			return false, nil
		}
		glog.V(2).Infoln("first dial fail:", addr)
	}
	{ // Second: test remote connect
		conn, err := net.DialTimeout("tcp", i.listenIP+":http", i.timeout/100)
		if err != nil {
			glog.V(1).Infoln("dial self service fail:", err)
			return false, err
		}
		defer conn.Close()

		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, err = conn.Write([]byte("TRACE / HTTP/1.1\r\nHost: " + addr + "\r\n\r\n")); err != nil {
			glog.V(1).Infoln("dial self service fail:", err)
			return false, err
		}
		if _, err = conn.Read(make([]byte, 1)); err != nil && err != io.EOF {
			return false, nil
		}
		glog.V(2).Infoln("remote dial succ:", addr)
	}
	{ // Third: retest direct connect
		conn, err := net.DialTimeout("tcp", addr+":http", i.timeout)
		if err == nil {
			whiteList.Add(addr)
			conn.Close()
			return false, nil
		}
		glog.V(2).Infoln("retry dial fail:", addr)
	}

	// After three round test, most probably the addr is blocked
	conf.AddSuggest(addr)
	glog.Infof("added suggest domain: %s", addr)
	return true, nil
}
