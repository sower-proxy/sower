package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
	"github.com/sower-proxy/sower/config"
	"github.com/sower-proxy/sower/internal/admin"
	"github.com/sower-proxy/sower/router"
)

const adminShutdownTimeout = 5 * time.Second

// adminRules adapts the router's rule sets to the admin RuleManager
// interface. Mutations persist through the StateStore first; the runtime
// rule set only changes after a successful write, so a persist failure
// surfaces as an API error without leaving memory and disk out of sync.
type adminRules struct {
	// mutationMu covers the persist-then-runtime sequence. StateStore alone
	// serializes disk candidates, but it cannot keep a later mutation from
	// overtaking an earlier RuleSet update.
	mutationMu sync.Mutex
	r          *router.Router
	state      *admin.StateStore
	baseline   map[admin.Category][]string
}

// newAdminRules builds the adapter. baseline holds the boot rule lists and
// must already be registered on the StateStore via SetBaseline.
func newAdminRules(r *router.Router, state *admin.StateStore, baseline map[admin.Category][]string) *adminRules {
	return &adminRules{r: r, state: state, baseline: baseline}
}

// snapshotBaseline captures the current rule lists as the boot baseline.
// Call after config and rule files are loaded, before applying state deltas.
func snapshotBaseline(r *router.Router) map[admin.Category][]string {
	return map[admin.Category][]string{
		admin.CategoryBlock:  r.BlockRule.List(),
		admin.CategoryDirect: r.DirectRule.List(),
		admin.CategoryProxy:  r.ProxyRule.List(),
	}
}

func (a *adminRules) RuleList(category admin.Category) ([]string, error) {
	rs, err := a.rules(category)
	if err != nil {
		return nil, err
	}
	return rs.List(), nil
}

func (a *adminRules) RuleSearch(category admin.Category, q string, offset, limit int) ([]string, uint64, error) {
	rs, err := a.rules(category)
	if err != nil {
		return nil, 0, err
	}
	rules, total := rs.ListFiltered(q, offset, limit)
	return rules, total, nil
}

func (a *adminRules) RuleAdd(category admin.Category, rules ...string) error {
	a.mutationMu.Lock()
	defer a.mutationMu.Unlock()

	rs, err := a.rules(category)
	if err != nil {
		return err
	}
	runtimeAdd, err := a.state.RuleAdd(category, rules...)
	if err != nil {
		return fmt.Errorf("persist rule additions: %w", err)
	}
	if len(runtimeAdd) == 0 {
		return nil
	}

	// Reinstating a baseline rule must restore the boot order. MatchRule is
	// intentionally first-match for diagnostics, so appending a restored
	// baseline rule would otherwise change its observable result.
	for _, rule := range runtimeAdd {
		if a.isBaselineRule(category, rule) {
			rs.Replace(a.effectiveRules(category)...)
			return nil
		}
	}
	rs.Add(runtimeAdd...)
	rs.Compact()
	return nil
}

func (a *adminRules) isBaselineRule(category admin.Category, rule string) bool {
	for _, baselineRule := range a.baseline[category] {
		if baselineRule == rule {
			return true
		}
	}
	return false
}

func (a *adminRules) RuleRemove(category admin.Category, rule string) (bool, error) {
	removed, err := a.RuleRemoveMany(category, rule)
	return len(removed) > 0, err
}

// RuleRemoveMany persists the full deletion candidate before touching the
// runtime RuleSet, so a write failure cannot partially apply a batch.
func (a *adminRules) RuleRemoveMany(category admin.Category, rules ...string) ([]string, error) {
	a.mutationMu.Lock()
	defer a.mutationMu.Unlock()

	rs, err := a.rules(category)
	if err != nil {
		return nil, err
	}
	removed, err := a.state.RuleRemoveBatch(category, rules...)
	if err != nil {
		return nil, fmt.Errorf("persist rule removals: %w", err)
	}
	if len(removed) == 0 {
		return nil, nil
	}

	removedSet := make(map[string]struct{}, len(removed))
	for _, rule := range removed {
		removedSet[rule] = struct{}{}
	}
	current := rs.List()
	kept := current[:0]
	for _, rule := range current {
		if _, removed := removedSet[rule]; !removed {
			kept = append(kept, rule)
		}
	}
	rs.Replace(kept...)
	return removed, nil
}

func (a *adminRules) RuleCount(category admin.Category) uint64 {
	rs, err := a.rules(category)
	if err != nil {
		return 0
	}
	return rs.Count()
}

func (a *adminRules) RuleChanges() admin.RuleChangeSet {
	return a.state.Changes()
}

// RuleReset clears deltas for one category — or all of them when category
// is empty — and rebuilds the runtime rule sets from the boot baseline.
func (a *adminRules) RuleReset(category admin.Category) error {
	a.mutationMu.Lock()
	defer a.mutationMu.Unlock()

	cats := []admin.Category{category}
	if category == "" {
		cats = []admin.Category{admin.CategoryBlock, admin.CategoryDirect, admin.CategoryProxy}
	}
	if err := a.state.RuleReset(cats...); err != nil {
		return fmt.Errorf("persist rule reset: %w", err)
	}
	for _, cat := range cats {
		rs, err := a.rules(cat)
		if err != nil {
			return err
		}
		rs.Replace(a.effectiveRules(cat)...)
	}
	return nil
}

// effectiveRules computes the runtime rule list for a category: baseline
// minus tombstones, plus admin additions.
func (a *adminRules) effectiveRules(category admin.Category) []string {
	d := a.state.Delta(category)
	tombstoned := make(map[string]struct{}, len(d.Remove))
	for _, rule := range d.Remove {
		tombstoned[rule] = struct{}{}
	}
	out := make([]string, 0, len(a.baseline[category])+len(d.Add))
	for _, rule := range a.baseline[category] {
		if _, ok := tombstoned[rule]; !ok {
			out = append(out, rule)
		}
	}
	return append(out, d.Add...)
}

func (a *adminRules) rules(category admin.Category) (*router.RuleSet, error) {
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
func (a *adminRules) TestDomain(domain string) (admin.DomainTest, error) {
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

func startAdminListener(ctx context.Context, cfg config.SowerConfig, rules admin.RuleManager, configMgr admin.ConfigManager, stats *admin.Stats, errCh chan<- error) error {
	if cfg.Admin.Disable || cfg.Admin.Addr == "" {
		return nil
	}

	srv := admin.NewServer(admin.Options{
		Password:    cfg.Admin.Password,
		Version:     version,
		Date:        date,
		Rules:       rules,
		Stats:       stats,
		SessionFile: cfg.Admin.SessionFile,
		Config:      configMgr,
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
func startSharedHTTPListener(ctx context.Context, cfg config.SowerConfig, r *router.Router, rules admin.RuleManager, configMgr admin.ConfigManager, stats *admin.Stats, errCh chan<- error) error {
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
		Rules:       rules,
		Stats:       stats,
		SessionFile: cfg.Admin.SessionFile,
		Config:      configMgr,
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
