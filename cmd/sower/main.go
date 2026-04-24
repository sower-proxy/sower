package main

import (
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cristalhq/aconfig"
	"github.com/cristalhq/aconfig/aconfigtoml"
	"github.com/cristalhq/aconfig/aconfigyaml"
	"github.com/lmittmann/tint"
	"github.com/miekg/dns"
	"github.com/sower-proxy/deferlog/v2"
	"github.com/sower-proxy/sower/config"
	"github.com/sower-proxy/sower/pkg/suffixtree"
	"github.com/sower-proxy/sower/pkg/upstreamtls"
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
	if strings.HasSuffix(os.Args[0], ".test") {
		return
	}

	if err := aconfig.LoaderFor(&conf, aconfig.Config{
		AllowUnknownFields: false,
		FileFlag:           "c",
		Files: []string{
			"sower.toml",
			"sower.yaml",
			"sower.yml",
			"/etc/sower/sower.toml",
			"/etc/sower/sower.yaml",
			"/etc/sower/sower.yml",
		},
		FileDecoders: map[string]aconfig.FileDecoder{
			".yml":  aconfigyaml.New(),
			".yaml": aconfigyaml.New(),
			".toml": aconfigtoml.New(),
		},
	}).Load(); err != nil {
		slog.Error("load config", "error", err, "config", conf)
		os.Exit(1)
	}

	if err := conf.Validate(); err != nil {
		slog.Error("validate config", "error", err)
		os.Exit(1)
	}
	slog.Info("starting sower",
		"version", version,
		"date", date,
		"log_level", conf.LogLevel,
		"remote_type", conf.Remote.Type,
		"remote_addr", conf.Remote.Addr,
		"remote_password", deferlog.Secret(conf.Remote.Password),
		"remote_tls", conf.Remote.TLS,
		"dns", conf.DNS,
		"socks5", conf.Socks5,
		"router", conf.Router)
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, stop, conf); err != nil {
		slog.Error("run sower", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, stop context.CancelFunc, cfg config.SowerConfig) error {
	upstreamDNS := effectiveUpstreamDNS(cfg)
	proxyDial, err := GenProxyDial(cfg.Remote.Type, cfg.Remote.Addr, cfg.Remote.Password, upstreamDNS, upstreamtls.Options{
		ServerName:         cfg.Remote.TLS.ServerName,
		ClientHello:        cfg.Remote.TLS.ClientHello,
		InsecureSkipVerify: cfg.Remote.TLS.InsecureSkipVerify,
	})
	if err != nil {
		return fmt.Errorf("build proxy dialer: %w", err)
	}
	r := newRouter(cfg, upstreamDNS, proxyDial)

	start := time.Now()
	if err := loadRouterRules(ctx, r, proxyDial, cfg); err != nil {
		return err
	}

	errCh := make(chan error, 8)
	if err := startDNSListeners(ctx, cfg, r, errCh); err != nil {
		return err
	}
	if err := startSocks5Listener(ctx, cfg, r, errCh); err != nil {
		return err
	}

	slog.Info("loaded rules, proxy started", "took", time.Since(start),
		"blockRule", r.BlockRule.Count, "directRule", r.DirectRule.Count, "proxyRule", r.ProxyRule.Count)
	runtime.GC()

	select {
	case <-ctx.Done():
		slog.Info("shutting down sower", "reason", ctx.Err())
	case err := <-errCh:
		slog.Error("serve failed", "error", err)
		stop()
	}
	return nil
}

func effectiveUpstreamDNS(cfg config.SowerConfig) string {
	if cfg.DNS.Upstream != "" {
		return cfg.DNS.Upstream
	}
	return cfg.DNS.Fallback
}

func newRouter(cfg config.SowerConfig, upstreamDNS string, proxyDial router.ProxyDialFn) *router.Router {
	r := router.NewRouter([]string{cfg.DNS.Serve, cfg.DNS.Serve6}, upstreamDNS, cfg.DNS.Fallback, cfg.Router.Country.MMDB, proxyDial)
	r.BlockRule = suffixtree.NewNodeFromRules(cfg.Router.Block.Rules...)
	r.DirectRule = suffixtree.NewNodeFromRules(cfg.Router.Direct.Rules...)
	r.ProxyRule = suffixtree.NewNodeFromRules(cfg.Router.Proxy.Rules...)
	r.AddCountryCIDRs(cfg.Router.Country.Rules...)
	return r
}

func loadRouterRules(ctx context.Context, r *router.Router, proxyDial router.ProxyDialFn, cfg config.SowerConfig) error {
	if err := loadRule(ctx, r.BlockRule, proxyDial, cfg.Router.Block.File, cfg.Router.Block.FilePrefix, cfg.Router.Block.FileSkipRules); err != nil {
		return fmt.Errorf("load block rules: %w", err)
	}
	if err := loadRule(ctx, r.DirectRule, proxyDial, cfg.Router.Direct.File, cfg.Router.Direct.FilePrefix, cfg.Router.Direct.FileSkipRules); err != nil {
		return fmt.Errorf("load direct rules: %w", err)
	}
	if err := loadRule(ctx, r.ProxyRule, proxyDial, cfg.Router.Proxy.File, cfg.Router.Proxy.FilePrefix, cfg.Router.Proxy.FileSkipRules); err != nil {
		return fmt.Errorf("load proxy rules: %w", err)
	}
	countryLines, err := fetchRuleFile(ctx, proxyDial, cfg.Router.Country.File)
	if err != nil {
		return fmt.Errorf("load country rules: %w", err)
	}
	for _, line := range countryLines {
		r.AddCountryCIDRs(line)
	}
	return nil
}

func startDNSListeners(ctx context.Context, cfg config.SowerConfig, r *router.Router, errCh chan<- error) error {
	if cfg.DNS.Disable {
		slog.Info("DNS proxy disabled")
		return nil
	}

	for _, ip := range dnsListenIPs(cfg) {
		if err := startHTTPListener(ctx, ip, r, errCh); err != nil {
			return err
		}
		if err := startHTTPSListener(ctx, ip, r, errCh); err != nil {
			return err
		}
		if err := startDNSUDPListener(ctx, ip, r, errCh); err != nil {
			return err
		}
	}
	return nil
}

func dnsListenIPs(cfg config.SowerConfig) []string {
	ips := make([]string, 0, 2)
	if strings.TrimSpace(cfg.DNS.Serve) != "" {
		ips = append(ips, cfg.DNS.Serve)
	}
	if strings.TrimSpace(cfg.DNS.Serve6) != "" {
		ips = append(ips, cfg.DNS.Serve6)
	}
	return ips
}

func startHTTPListener(ctx context.Context, ip string, r *router.Router, errCh chan<- error) error {
	addr := net.JoinHostPort(ip, "80")
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen http proxy on %s: %w", addr, err)
	}
	slog.Info("service listening", "service", "http proxy", "network", "tcp", "addr", addr)
	go closeOnDone(ctx, ln)
	go serveAndReport(errCh, "http proxy", func() error {
		return ServeHTTP(ctx, ln, r)
	})
	return nil
}

func startHTTPSListener(ctx context.Context, ip string, r *router.Router, errCh chan<- error) error {
	addr := net.JoinHostPort(ip, "443")
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen https proxy on %s: %w", addr, err)
	}
	slog.Info("service listening", "service", "https proxy", "network", "tcp", "addr", addr)
	go closeOnDone(ctx, ln)
	go serveAndReport(errCh, "https proxy", func() error {
		return ServeHTTPS(ctx, ln, r)
	})
	return nil
}

func startDNSUDPListener(ctx context.Context, ip string, r *router.Router, errCh chan<- error) error {
	addr := net.JoinHostPort(ip, "53")
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("listen dns proxy on %s: %w", addr, err)
	}

	server := &dns.Server{
		PacketConn: pc,
		Handler:    r,
	}
	slog.Info("service listening", "service", "dns proxy", "network", "udp", "addr", addr)
	go shutdownDNSServerOnDone(ctx, server)
	go func() {
		if err := server.ActivateAndServe(); err != nil && !errors.Is(err, net.ErrClosed) {
			reportServeError(errCh, "dns proxy", fmt.Errorf("serve on %s: %w", addr, err))
		}
	}()
	return nil
}

func startSocks5Listener(ctx context.Context, cfg config.SowerConfig, r *router.Router, errCh chan<- error) error {
	if cfg.Socks5.Disable {
		slog.Info("SOCKS5 proxy disabled")
		return nil
	}

	ln, err := net.Listen("tcp", cfg.Socks5.Addr)
	if err != nil {
		return fmt.Errorf("listen socks5 proxy on %s: %w", cfg.Socks5.Addr, err)
	}
	slog.Info("service listening", "service", "socks5 proxy", "network", "tcp", "addr", cfg.Socks5.Addr)
	go closeOnDone(ctx, ln)
	go serveAndReport(errCh, "socks5 proxy", func() error {
		return ServeSocks5(ctx, ln, r)
	})
	return nil
}

func loadRule(ctx context.Context, rule *suffixtree.Node, proxyDial router.ProxyDialFn, file, linePrefix string, skipRules []string) error {
	skipRule := suffixtree.NewNodeFromRules(skipRules...)
	lines, err := fetchRuleFile(ctx, proxyDial, file)
	if err != nil {
		return err
	}
	for _, line := range lines {
		item := linePrefix + line
		if skipRule.Match(line) || skipRule.Match(item) {
			continue
		}
		rule.Add(item)
	}
	rule.GC()
	return nil
}

func fetchRuleFile(ctx context.Context, proxyDial router.ProxyDialFn, file string) ([]string, error) {
	if file == "" {
		return nil, nil
	}

	var loadFn func() (io.ReadCloser, error)
	if _, err := os.Stat(file); err == nil {
		loadFn = func() (io.ReadCloser, error) {
			return os.Open(file)
		}
	} else {
		if proxyDial == nil {
			return nil, fmt.Errorf("remote rule file %q requires upstream proxy dialer", file)
		}
		var lastDialErr error
		client := &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					domain, port, _ := net.SplitHostPort(addr)
					p, _ := strconv.Atoi(port)
					conn, err := proxyDial("tcp", domain, uint16(p))
					if err != nil {
						lastDialErr = err
					}
					return conn, err
				},
			},
		}

		loadFn = func() (io.ReadCloser, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, file, nil)
			if err != nil {
				return nil, err
			}
			req.Header.Add("Accept-Encoding", "gzip")
			resp, err := client.Do(req)
			if err != nil {
				if ctx.Err() != nil && lastDialErr != nil {
					return nil, fmt.Errorf("proxy dial failed before request cancellation: %v: %w", lastDialErr, ctx.Err())
				}
				return nil, err
			}

			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				return nil, fmt.Errorf("status code: %d", resp.StatusCode)
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
		if ctx.Err() != nil {
			return nil, fmt.Errorf("fetch rule file %q canceled after previous error %v: %w", file, err, ctx.Err())
		}

		// wait: 28.5s
		timer := time.NewTimer(i * i * 100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("fetch rule file %q canceled after previous error %v: %w", file, err, ctx.Err())
		case <-timer.C:
		}
		rc, err = loadFn()
	}
	if err != nil {
		return nil, fmt.Errorf("fetch rule file %q: %w", file, err)
	}

	lines, err := readRuleLines(rc)
	if err != nil {
		return nil, fmt.Errorf("read rule file %q: %w", file, err)
	}
	return lines, nil
}

func readRuleLines(rc io.ReadCloser) ([]string, error) {
	defer rc.Close()

	br := bufio.NewReader(rc)
	reader := io.Reader(br)
	magic, err := br.Peek(2)
	if err != nil && err != io.EOF && err != bufio.ErrBufferFull {
		return nil, err
	}
	if len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gr, err := gzip.NewReader(br)
		if err != nil {
			return nil, err
		}
		defer gr.Close()
		reader = gr
	}

	lines := make([]string, 0)
	lineReader := bufio.NewReader(reader)
	for {
		line, err := lineReader.ReadString('\n')
		if err != nil && err != io.EOF {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
		if err == io.EOF {
			return lines, nil
		}
	}
}

func closeOnDone(ctx context.Context, closer io.Closer) {
	<-ctx.Done()
	_ = closer.Close()
}

func shutdownDNSServerOnDone(ctx context.Context, server *dns.Server) {
	<-ctx.Done()
	_ = server.ShutdownContext(ctx)
}

func reportServeError(errCh chan<- error, service string, err error) {
	if err == nil || errors.Is(err, net.ErrClosed) {
		return
	}

	select {
	case errCh <- fmt.Errorf("%s: %w", service, err):
	default:
	}
}

func serveAndReport(errCh chan<- error, service string, serve func() error) {
	reportServeError(errCh, service, serve())
}
