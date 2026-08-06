package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	"github.com/miekg/dns"
	"github.com/sower-proxy/sower/config"
	"github.com/sower-proxy/sower/internal/admin"
	"github.com/sower-proxy/sower/router"
)

const adminShutdownTimeout = 5 * time.Second

// adminRules adapts the router's rule sets to the admin RuleManager interface.
type adminRules struct {
	r *router.Router
}

func (a adminRules) RuleList(category admin.Category) ([]string, error) {
	rs, err := a.rules(category)
	if err != nil {
		return nil, err
	}
	return rs.List(), nil
}

func (a adminRules) RuleSearch(category admin.Category, q string, offset, limit int) ([]string, uint64, error) {
	rs, err := a.rules(category)
	if err != nil {
		return nil, 0, err
	}
	rules, total := rs.ListFiltered(q, offset, limit)
	return rules, total, nil
}

func (a adminRules) RuleAdd(category admin.Category, rules ...string) error {
	rs, err := a.rules(category)
	if err != nil {
		return err
	}
	rs.Add(rules...)
	rs.Compact()
	return nil
}

func (a adminRules) RuleRemove(category admin.Category, rule string) (bool, error) {
	rs, err := a.rules(category)
	if err != nil {
		return false, err
	}
	return rs.Remove(rule), nil
}

func (a adminRules) RuleCount(category admin.Category) uint64 {
	rs, err := a.rules(category)
	if err != nil {
		return 0
	}
	return rs.Count()
}

func (a adminRules) rules(category admin.Category) (*router.RuleSet, error) {
	switch category {
	case admin.CategoryBlock:
		return a.r.BlockRule, nil
	case admin.CategoryDirect:
		return a.r.DirectRule, nil
	case admin.CategoryProxy:
		return a.r.ProxyRule, nil
	default:
		return nil, fmt.Errorf("unknown rule category %q", category)
	}
}

// TestDomain reports which rule sets match the domain and the route a
// connection to it would take. It mirrors DialSmart's rule priority
// (block > direct > proxy); when no rule matches it reports "auto" without
// performing live detection.
func (a adminRules) TestDomain(domain string) (admin.DomainTest, error) {
	domain = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(domain, ".")))
	if domain == "" {
		return admin.DomainTest{}, fmt.Errorf("domain is required")
	}
	blockRule, blockOK := a.r.BlockRule.MatchRule(domain)
	directRule, directOK := a.r.DirectRule.MatchRule(domain)
	proxyRule, proxyOK := a.r.ProxyRule.MatchRule(domain)
	res := admin.DomainTest{
		Domain: domain,
		Matches: []admin.CategoryTest{
			{Category: admin.CategoryBlock, Matched: blockOK, Rule: blockRule},
			{Category: admin.CategoryDirect, Matched: directOK, Rule: directRule},
			{Category: admin.CategoryProxy, Matched: proxyOK, Rule: proxyRule},
		},
	}
	switch {
	case blockOK:
		res.Route = "block"
	case directOK:
		res.Route = "direct"
	case proxyOK:
		res.Route = "proxy"
	default:
		res.Route = "auto"
		res.Note = "未命中任何规则，将按自动检测（本地站点/可直连）或默认代理路由"
	}
	return res, nil
}

// dnsStatsHandler counts DNS queries before delegating to the router.
type dnsStatsHandler struct {
	dns.Handler
	stats *admin.Stats
}

func (h dnsStatsHandler) ServeDNS(w dns.ResponseWriter, req *dns.Msg) {
	if len(req.Question) == 1 {
		h.stats.RecordDNS(req.Question[0].Name, admin.ClientIPOf(w.RemoteAddr()))
	}
	h.Handler.ServeDNS(w, req)
}

func startAdminListener(ctx context.Context, cfg config.SowerConfig, r *router.Router, stats *admin.Stats, errCh chan<- error) error {
	if cfg.Admin.Disable || cfg.Admin.Addr == "" {
		return nil
	}

	srv := admin.NewServer(admin.Options{
		Password:    cfg.Admin.Password,
		Version:     version,
		Date:        date,
		Rules:       adminRules{r: r},
		Stats:       stats,
		SessionFile: cfg.Admin.SessionFile,
	})

	ln, err := net.Listen("tcp", cfg.Admin.Addr)
	if err != nil {
		return fmt.Errorf("listen admin on %s: %w", cfg.Admin.Addr, err)
	}
	slog.Info("service listening", "service", "admin", "network", "tcp", "addr", cfg.Admin.Addr)

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), adminShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Warn("shutdown admin server", "error", err)
		}
	}()
	go serveAndReport(errCh, "admin", func() error {
		return srv.Serve(ln)
	})
	return nil
}

// startSharedHTTPListener serves the admin console and the HTTP proxy from
// one listener on the DNS HTTP address. It is used when admin.addr exactly
// matches dns.serve:80.
func startSharedHTTPListener(ctx context.Context, cfg config.SowerConfig, r *router.Router, stats *admin.Stats, errCh chan<- error) error {
	addr, ok := sharedAdminHTTPAddr(cfg)
	if !ok {
		return nil
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen shared http on %s: %w", addr, err)
	}
	slog.Info("service listening", "service", "http proxy + admin", "network", "tcp", "addr", addr)

	srv := admin.NewServer(admin.Options{
		Password:    cfg.Admin.Password,
		Version:     version,
		Date:        date,
		Rules:       adminRules{r: r},
		Stats:       stats,
		SessionFile: cfg.Admin.SessionFile,
	})
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), adminShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Warn("shutdown admin server", "error", err)
		}
	}()
	go closeOnDone(ctx, ln)
	go serveAndReport(errCh, "http proxy + admin", func() error {
		return ServeSharedHTTP(ctx, ln, r, stats, srv, cfg.DNS.Serve, errCh)
	})
	return nil
}
