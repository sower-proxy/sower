package main

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/cristalhq/aconfig"
	"github.com/cristalhq/aconfig/aconfighcl"
	"github.com/cristalhq/aconfig/aconfigtoml"
	"github.com/cristalhq/aconfig/aconfigyaml"
	"github.com/lmittmann/tint"
	"github.com/miekg/dns"
	"github.com/pkg/errors"
	"github.com/sower-proxy/deferlog/v2"
	"github.com/sower-proxy/sower/pkg/suffixtree"
	"github.com/sower-proxy/sower/router"
)

var (
	version, date string

	conf = struct {
		Debug bool `default:"false" usage:"debug mode"`

		Remote struct {
			Type     string `default:"sower" required:"true" usage:"option: sower/trojan/socks5"`
			Addr     string `required:"true" usage:"proxy address, eg: proxy.com/127.0.0.1:7890"`
			Password string `usage:"remote proxy password"`
		}

		DNS struct {
			Disable    bool   `default:"false" usage:"disable DNS proxy"`
			Serve      string `usage:"dns server ip"`
			Serve6     string `usage:"dns server ipv6, eg: ::1"`
			ServeIface string `usage:"use the IP in the net interface, if serve ip not setted. eg: eth0"`
			Upstream   string `usage:"upstream dns server"`
			Fallback   string `default:"223.5.5.5" required:"true" usage:"fallback dns server"`
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
	fi, _ := os.Stdout.Stat()
	noColor := (fi.Mode() & os.ModeCharDevice) == 0
	deferlog.SetDefault(slog.New(tint.NewHandler(os.Stdout,
		&tint.Options{AddSource: true, NoColor: noColor})))

	if err := aconfig.LoaderFor(&conf, aconfig.Config{
		AllowUnknownFields: true,
		FileFlag:           "c",
		Files: []string{
			"sower.hcl",
			"sower.toml",
			"sower.yaml",
			"sower.yml",
			"/etc/sower/sower.hcl",
			"/etc/sower/sower.toml",
			"/etc/sower/sower.yaml",
			"/etc/sower/sower.yml",
		},
		FileDecoders: map[string]aconfig.FileDecoder{
			".yml":  aconfigyaml.New(),
			".yaml": aconfigyaml.New(),
			".toml": aconfigtoml.New(),
			".hcl":  aconfighcl.New(),
		},
	}).Load(); err != nil {
		slog.Error("load config", "error", err, "config", conf)
		os.Exit(1)
	}

	if conf.DNS.ServeIface != "" {
		iface, err := net.InterfaceByName(conf.DNS.ServeIface)
		if err != nil {
			slog.Error("get interface", "error", err, "iface", conf.DNS.ServeIface)
			os.Exit(1)
		}
		addrs, err := iface.Addrs()
		if err != nil {
			slog.Error("get interface addresses", "error", err, "iface", conf.DNS.ServeIface)
			os.Exit(1)
		}
		for _, addr := range addrs {
			ip, _, err := net.ParseCIDR(addr.String())
			if err != nil {
				slog.Error("parse interface IP", "error", err, "iface", conf.DNS.ServeIface, "ip", ip.String())
				os.Exit(1)
			}

			if ip.To4() != nil { // ipv4
				if conf.DNS.Serve == "" {
					conf.DNS.Serve = ip.String()
				}
			} else if ip.IsGlobalUnicast() { // ipv6 must be global unicast
				if conf.DNS.Serve6 == "" {
					conf.DNS.Serve6 = ip.String()
				}
			}
		}
	}

	if !conf.DNS.Disable && conf.DNS.Serve == "" {
		slog.Error("dns serve ip and serve interface not set")
		os.Exit(1)
	}

	conf.Router.Direct.Rules = append(conf.Router.Direct.Rules,
		conf.Remote.Addr, "**.in-addr.arpa", "**.ip6.arpa")
	slog.Info("starting sower", "version", version, "date", date, "config", fmt.Sprint(conf))
}

func main() {
	upstreamDNS := conf.DNS.Upstream
	if upstreamDNS == "" {
		upstreamDNS = conf.DNS.Fallback
	}
	proxyDial := GenProxyDial(conf.Remote.Type, conf.Remote.Addr, conf.Remote.Password, upstreamDNS)
	r := router.NewRouter(conf.DNS.Serve, conf.DNS.Upstream, conf.DNS.Fallback, conf.Router.Country.MMDB, proxyDial)
	r.BlockRule = suffixtree.NewNodeFromRules(conf.Router.Block.Rules...)
	r.DirectRule = suffixtree.NewNodeFromRules(conf.Router.Direct.Rules...)
	r.ProxyRule = suffixtree.NewNodeFromRules(conf.Router.Proxy.Rules...)
	r.AddCountryCIDRs(conf.Router.Country.Rules...)

	if conf.DNS.Disable {
		slog.Info("DNS proxy disabled")
	} else {
		ips := make([]string, 0, 2)
		if strings.TrimSpace(conf.DNS.Serve) != "" {
			ips = append(ips, conf.DNS.Serve)
		}
		if strings.TrimSpace(conf.DNS.Serve6) != "" {
			ips = append(ips, conf.DNS.Serve6)
		}

		for _, ip := range ips {
			lnHTTP, err := net.Listen("tcp", net.JoinHostPort(ip, "80"))
			if err != nil {
				slog.Error("listen port 80", "error", err, "listen_on", net.JoinHostPort(ip, "80"))
				os.Exit(1)
			}
			go ServeHTTP(lnHTTP, r)

			lnHTTPS, err := net.Listen("tcp", net.JoinHostPort(ip, "443"))
			if err != nil {
				slog.Error("listen port 443", "error", err, "listen_on", net.JoinHostPort(ip, "443"))
				os.Exit(1)
			}
			go ServeHTTPS(lnHTTPS, r)

			go func(ip string) {
				err := dns.ListenAndServe(net.JoinHostPort(ip, "53"), "udp", r)
				if err != nil {
					slog.Error("serve dns", "error", err, "listen_on", ip)
					os.Exit(1)
				}
			}(ip)
		}
	}

	go func() {
		if conf.Socks5.Disable {
			slog.Info("SOCKS5 proxy disabled")
			return
		}

		ln, err := net.Listen("tcp", conf.Socks5.Addr)
		if err != nil {
			slog.Error("listen port", "error", err)
			os.Exit(1)
		}
		slog.Info("SOCKS5 proxy listening", "addr", conf.Socks5.Addr)
		go ServeSocks5(ln, r)
	}()

	start := time.Now()
	loadRule(r.BlockRule, proxyDial, conf.Router.Block.File, conf.Router.Block.FilePrefix)
	loadRule(r.DirectRule, proxyDial, conf.Router.Direct.File, conf.Router.Direct.FilePrefix)
	loadRule(r.ProxyRule, proxyDial, conf.Router.Proxy.File, conf.Router.Proxy.FilePrefix)
	for line := range fetchRuleFile(proxyDial, conf.Router.Country.File) {
		r.AddCountryCIDRs(line)
	}

	slog.Info("loaded rules, proxy started", "took", time.Since(start),
		"blockRule", r.BlockRule.Count, "directRule", r.DirectRule.Count, "proxyRule", r.ProxyRule.Count)
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
	if err != nil {
		slog.Error("fetch rule file", "error", err, "file", file)
		os.Exit(1)
	}

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
				slog.Error("read line", "error", err, "file", file)
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
