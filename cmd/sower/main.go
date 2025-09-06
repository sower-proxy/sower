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
	"github.com/sower-proxy/sower/config"
	"github.com/sower-proxy/sower/pkg/suffixtree"
	"github.com/sower-proxy/sower/router"
)

var (
	version, date string
	conf          config.SowerConfig
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

	if err := conf.Validate(); err != nil {
		slog.Error("validate config", "error", err)
		os.Exit(1)
	}
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
