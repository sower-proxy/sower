package main

import (
	"bufio"
	"compress/gzip"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/bobg/go-generics/v3/slices"
	"github.com/cristalhq/aconfig"
	"github.com/cristalhq/aconfig/aconfighcl"
	"github.com/cristalhq/aconfig/aconfigtoml"
	"github.com/cristalhq/aconfig/aconfigyaml"
	"github.com/miekg/dns"
	"github.com/pkg/errors"
	"github.com/sower-proxy/deferlog/log"
	"github.com/wweir/sower/pkg/suffixtree"
	"github.com/wweir/sower/router"
)

var (
	version, date string

	conf = struct {
		Remote struct {
			Type     string `default:"sower" required:"true" usage:"option: sower/trojan/socks5"`
			Addr     string `required:"true" usage:"proxy address, eg: proxy.com/127.0.0.1:7890"`
			Password string `usage:"remote proxy password"`
		}

		DNS struct {
			Disable    bool     `default:"false" usage:"disable DNS proxy"`
			Serve      string   `usage:"Deprecated, use 'serve_ips' instead"`
			ServeIps   []string `usage:"dns server ip, eg: 127.0.0.1,::1"`
			ServeIface string   `usage:"dns server interface, eg: eth0"`
			Fallback   string   `default:"223.5.5.5" required:"true" usage:"fallback dns server"`
		}
		Socks5 struct {
			Disable bool   `default:"false" usage:"disable sock5 proxy"`
			Addr    string `default:"127.0.0.1:1080" usage:"socks5 listen address"`
		} `flag:"socks5"`

		Router struct {
			Block struct {
				File       string   `usage:"block list file, local file or remote"`
				FilePrefix string   `default:"**." usage:"parsed as '<prefix>line_text'"`
				Rules      []string `usage:"block list rules"`
			}
			Direct struct {
				File       string   `usage:"direct list file, local file or remote"`
				FilePrefix string   `default:"**." usage:"parsed as '<prefix>line_text'"`
				Rules      []string `usage:"direct list rules"`
			}
			Proxy struct {
				File       string   `usage:"proxy list file, local file or remote"`
				FilePrefix string   `default:"**." usage:"parsed as '<prefix>line_text'"`
				Rules      []string `usage:"proxy list rules"`
			}

			Country struct {
				MMDB       string   `usage:"mmdb file"`
				File       string   `usage:"CIDR block list file, local file or remote"`
				FilePrefix string   `default:"" usage:"parsed as '<prefix>line_text'"`
				Rules      []string `usage:"CIDR list rules"`
			}
		}
	}{}
)

func init() {
	if err := aconfig.LoaderFor(&conf, aconfig.Config{
		AllowUnknownFields: true,
		FileFlag:           "f",
		FileDecoders: map[string]aconfig.FileDecoder{
			".yml":  aconfigyaml.New(),
			".yaml": aconfigyaml.New(),
			".toml": aconfigtoml.New(),
			".hcl":  aconfighcl.New(),
		},
	}).Load(); err != nil {
		log.Fatal().Err(err).
			Interface("config", conf).
			Msg("Load config")
	}

	if !slices.Contains(conf.DNS.ServeIps, conf.DNS.Serve) {
		conf.DNS.ServeIps = append(conf.DNS.ServeIps, conf.DNS.Serve)
	}
	if conf.DNS.ServeIface != "" {
		iface, err := net.InterfaceByName(conf.DNS.ServeIface)
		log.DebugFatal(err).Str("iface", conf.DNS.ServeIface).Msg("get iface")
		addrs, err := iface.Addrs()
		log.InfoFatal(err).Str("iface", conf.DNS.ServeIface).Msg("get iface addrs")
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			log.InfoFatal(err).Str("iface", conf.DNS.ServeIface).
				Msg("parse iface addr: " + addr.String())

			if !slices.Contains(conf.DNS.ServeIps, ip.String()) {
				conf.DNS.ServeIps = append(conf.DNS.ServeIps, ip.String())
			}
		}
	}

	conf.Router.Direct.Rules = append(conf.Router.Direct.Rules,
		conf.Remote.Addr, "**.in-addr.arpa", "**.ip6.arpa")
	log.Info().
		Str("version", version).
		Str("date", date).
		Interface("config", conf).
		Msg("Starting")
}

func main() {
	proxyDial := GenProxyDial(conf.Remote.Type, conf.Remote.Addr, conf.Remote.Password)
	r := router.NewRouter(conf.DNS.Serve, conf.DNS.Fallback, conf.Router.Country.MMDB, proxyDial)
	r.BlockRule = suffixtree.NewNodeFromRules(conf.Router.Block.Rules...)
	r.DirectRule = suffixtree.NewNodeFromRules(conf.Router.Direct.Rules...)
	r.ProxyRule = suffixtree.NewNodeFromRules(conf.Router.Proxy.Rules...)
	r.AddCountryCIDRs(conf.Router.Country.Rules...)

	if conf.DNS.Disable {
		log.Info().Msg("DNS proxy disabled")
		return
	}

	for _, ip := range conf.DNS.ServeIps {
		lnHTTP, err := net.Listen("tcp", net.JoinHostPort(ip, "80"))
		log.DebugFatal(err).Str("listen_on", ip).Msg("listen port 80")
		go ServeHTTP(lnHTTP, r)

		lnHTTPS, err := net.Listen("tcp", net.JoinHostPort(ip, "443"))
		log.DebugFatal(err).Str("listen_on", ip).Msg("listen port 443")
		go ServeHTTPS(lnHTTPS, r)

		go func(ip string) {
			err := dns.ListenAndServe(net.JoinHostPort(ip, "53"), "udp", r)
			log.InfoFatal(err).Str("listen_on", ip).Msg("serve dns")
		}(ip)
	}

	go func() {
		if conf.Socks5.Disable {
			log.Info().Msg("SOCKS5 proxy disabled")
			return
		}

		ln, err := net.Listen("tcp", conf.Socks5.Addr)
		log.InfoFatal(err).Str("listen_on", conf.Socks5.Addr).Msg("listen SOCKS5 proxy")
		go ServeSocks5(ln, r)
	}()

	start := time.Now()
	loadRule(r.BlockRule, proxyDial, conf.Router.Block.File, conf.Router.Block.FilePrefix)
	loadRule(r.DirectRule, proxyDial, conf.Router.Direct.File, conf.Router.Direct.FilePrefix)
	loadRule(r.ProxyRule, proxyDial, conf.Router.Proxy.File, conf.Router.Proxy.FilePrefix)
	for line := range fetchRuleFile(proxyDial, conf.Router.Country.File) {
		r.AddCountryCIDRs(line)
	}

	log.Info().
		Dur("took", time.Since(start)).
		Uint64("blockRule", r.BlockRule.Count).
		Uint64("directRule", r.DirectRule.Count).
		Uint64("proxyRule", r.ProxyRule.Count).
		Msg("Loaded rules, proxy started")
	runtime.GC()
	select {}
}

func loadRule(rule *suffixtree.Node, proxyDial router.ProxyDialFn, file, linePrefix string) {
	for line := range fetchRuleFile(proxyDial, file) {
		rule.Add(linePrefix + line)
	}
	rule.GC()
}
func fetchRuleFile(proxyDial router.ProxyDialFn, file string) <-chan string {
	if file == "" {
		return make(chan string)
	}

	var loadFn func() (io.ReadCloser, error)
	if _, err := os.Stat(file); err == nil {
		// load rule file from local file
		loadFn = func() (io.ReadCloser, error) {
			return os.Open(file)
		}
	} else {
		// load rule file from remote by HTTP
		client := &http.Client{
			Transport: &http.Transport{
				Dial: func(network, addr string) (net.Conn, error) {
					domain, port, _ := net.SplitHostPort(addr)
					p, _ := strconv.Atoi(port)
					return proxyDial("tcp", domain, uint16(p))
				},
			},
		}

		loadFn = func() (io.ReadCloser, error) {
			req, _ := http.NewRequest(http.MethodGet, file, nil)
			req.Header.Add("Accept-Encoding", "gzip")
			resp, err := client.Do(req)
			if err != nil {
				return nil, err
			}

			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				return nil, errors.Errorf("status code: %d", resp.StatusCode)
			}

			return resp.Body, nil
		}
	}

	// load rule file, retry 10 times
	rc, err := loadFn()
	for i := time.Duration(1); i < 10; i++ {
		if err == nil {
			break
		}

		// wait: 28.5s
		time.Sleep(i * i * 100 * time.Millisecond)
		rc, err = loadFn()
	}
	log.InfoFatal(err).
		Str("file", file).
		Msg("fetch rule file")

	ch := make(chan string, 100)
	go func() {
		defer rc.Close()
		defer close(ch)
		if gr, err := gzip.NewReader(rc); err == nil {
			rc = gr
			defer gr.Close()
		}

		br := bufio.NewReader(rc)
		for {
			line, _, err := br.ReadLine()
			if err == io.EOF {
				return
			} else if err != nil {
				log.Error().Err(err).
					Str("file", file).
					Msg("read line")
				return
			}

			if strings.TrimSpace(string(line)) == "" {
				continue
			}

			ch <- string(line)
		}
	}()

	return ch
}
