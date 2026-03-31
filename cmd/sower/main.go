package main

import (
	"bufio"
	"compress/gzip"
	"context"
	stderrors "errors"
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
	"github.com/cristalhq/aconfig/aconfighcl"
	"github.com/cristalhq/aconfig/aconfigtoml"
	"github.com/cristalhq/aconfig/aconfigyaml"
	"github.com/lmittmann/tint"
	"github.com/miekg/dns"
	"github.com/pkg/errors"
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

	upstreamDNS := conf.DNS.Upstream
	if upstreamDNS == "" {
		upstreamDNS = conf.DNS.Fallback
	}
	proxyDial, err := GenProxyDial(conf.Remote.Type, conf.Remote.Addr, conf.Remote.Password, upstreamDNS, upstreamtls.Options{
		ServerName:         conf.Remote.TLS.ServerName,
		ClientHello:        conf.Remote.TLS.ClientHello,
		InsecureSkipVerify: conf.Remote.TLS.InsecureSkipVerify,
	})
	if err != nil {
		slog.Error("build proxy dialer", "error", err)
		os.Exit(1)
	}
	r := router.NewRouter([]string{conf.DNS.Serve, conf.DNS.Serve6}, upstreamDNS, conf.DNS.Fallback, conf.Router.Country.MMDB, proxyDial)
	r.BlockRule = suffixtree.NewNodeFromRules(conf.Router.Block.Rules...)
	r.DirectRule = suffixtree.NewNodeFromRules(conf.Router.Direct.Rules...)
	r.ProxyRule = suffixtree.NewNodeFromRules(conf.Router.Proxy.Rules...)
	r.AddCountryCIDRs(conf.Router.Country.Rules...)

	errCh := make(chan error, 8)
	go watchServeErrors(ctx, stop, errCh)

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
			if err := startTCPService(ctx, errCh, "http proxy", net.JoinHostPort(ip, "80"), func(ctx context.Context, ln net.Listener) error {
				return ServeHTTP(ctx, ln, r)
			}); err != nil {
				slog.Error("start http proxy", "error", err, "listen_on", net.JoinHostPort(ip, "80"))
				os.Exit(1)
			}

			if err := startTCPService(ctx, errCh, "https proxy", net.JoinHostPort(ip, "443"), func(ctx context.Context, ln net.Listener) error {
				return ServeHTTPS(ctx, ln, r)
			}); err != nil {
				slog.Error("start https proxy", "error", err, "listen_on", net.JoinHostPort(ip, "443"))
				os.Exit(1)
			}

			if err := startDNSService(ctx, errCh, net.JoinHostPort(ip, "53"), r); err != nil {
				slog.Error("start dns proxy", "error", err, "listen_on", net.JoinHostPort(ip, "53"))
				os.Exit(1)
			}
		}
	}

	if conf.Socks5.Disable {
		slog.Info("SOCKS5 proxy disabled")
	} else if err := startTCPService(ctx, errCh, "socks5 proxy", conf.Socks5.Addr, func(ctx context.Context, ln net.Listener) error {
		return ServeSocks5(ctx, ln, r)
	}); err != nil {
		slog.Error("start socks5 proxy", "error", err, "listen_on", conf.Socks5.Addr)
		os.Exit(1)
	}

	start := time.Now()
	loadRule(ctx, r.BlockRule, proxyDial, conf.Router.Block.File, conf.Router.Block.FilePrefix)
	loadRule(ctx, r.DirectRule, proxyDial, conf.Router.Direct.File, conf.Router.Direct.FilePrefix)
	loadRule(ctx, r.ProxyRule, proxyDial, conf.Router.Proxy.File, conf.Router.Proxy.FilePrefix)
	for line := range fetchRuleFile(ctx, proxyDial, conf.Router.Country.File) {
		r.AddCountryCIDRs(line)
	}

	slog.Info("loaded rules, proxy started", "took", time.Since(start),
		"blockRule", r.BlockRule.Count, "directRule", r.DirectRule.Count, "proxyRule", r.ProxyRule.Count)
	runtime.GC()

	<-ctx.Done()
	slog.Info("shutting down sower", "reason", ctx.Err())
}

func loadRule(ctx context.Context, rule *suffixtree.Node, proxyDial router.ProxyDialFn, file, linePrefix string) {
	for line := range fetchRuleFile(ctx, proxyDial, file) {
		rule.Add(linePrefix + line)
	}
	rule.GC()
}

func fetchRuleFile(ctx context.Context, proxyDial router.ProxyDialFn, file string) <-chan string {
	if file == "" {
		ch := make(chan string)
		close(ch)
		return ch
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
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					domain, port, _ := net.SplitHostPort(addr)
					p, _ := strconv.Atoi(port)
					return proxyDial("tcp", domain, uint16(p))
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
		if ctx.Err() != nil {
			slog.Error("fetch rule file", "error", ctx.Err(), "file", file)
			os.Exit(1)
		}

		// wait: 28.5s
		timer := time.NewTimer(i * i * 100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			slog.Error("fetch rule file", "error", ctx.Err(), "file", file)
			os.Exit(1)
		case <-timer.C:
		}
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

func closeOnDone(ctx context.Context, closer io.Closer) {
	<-ctx.Done()
	_ = closer.Close()
}

func shutdownDNSServerOnDone(ctx context.Context, server *dns.Server) {
	<-ctx.Done()
	_ = server.ShutdownContext(ctx)
}

func reportServeError(errCh chan<- error, service string, err error) {
	if err == nil || stderrors.Is(err, net.ErrClosed) {
		return
	}

	select {
	case errCh <- fmt.Errorf("%s: %w", service, err):
	default:
	}
}

func startTCPService(ctx context.Context, errCh chan<- error, service, addr string, serve func(context.Context, net.Listener) error) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}

	slog.Info("service listening", "service", service, "network", "tcp", "addr", addr)
	go closeOnDone(ctx, ln)
	go func() {
		reportServeError(errCh, service, serve(ctx, ln))
	}()
	return nil
}

func startDNSService(ctx context.Context, errCh chan<- error, addr string, handler dns.Handler) error {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}

	server := &dns.Server{
		PacketConn: pc,
		Handler:    handler,
	}
	slog.Info("service listening", "service", "dns proxy", "network", "udp", "addr", addr)
	go shutdownDNSServerOnDone(ctx, server)
	go func() {
		err := server.ActivateAndServe()
		if err != nil && !stderrors.Is(err, net.ErrClosed) {
			reportServeError(errCh, "dns proxy", fmt.Errorf("serve on %s: %w", addr, err))
		}
	}()
	return nil
}

func watchServeErrors(ctx context.Context, stop context.CancelFunc, errCh <-chan error) {
	select {
	case <-ctx.Done():
		return
	case err := <-errCh:
		if err == nil {
			return
		}
		slog.Error("serve failed", "error", err)
		stop()
	}
}
