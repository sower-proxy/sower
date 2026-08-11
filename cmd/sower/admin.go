package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
	"github.com/sower-proxy/sower/config"
	"github.com/sower-proxy/sower/internal/admin"
	"github.com/sower-proxy/sower/router"
)

const adminShutdownTimeout = 5 * time.Second

// resolveAdminPassword returns the configured admin password, or a random one
// generated at startup when none is configured, plus whether the fallback was
// used. The generated password is printed once in the startup log
// (intentionally: it is the only way to log in) and changes on every restart;
// persisted sessions keep an already logged-in browser working across
// restarts. The temporary flag drives the /api/login-info hint on the login
// page.
func resolveAdminPassword(configured string) (password string, temporary bool) {
	if configured != "" {
		return configured, false
	}
	pw := admin.GeneratePassword()
	slog.Warn("admin enabled without a password; generated a temporary password",
		"password", pw,
		"hint", "set [admin].password to use a fixed password")
	return pw, true
}

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
	// hits resolve and count rule hits per category; any rule mutation must
	// invalidate the matching tracker's domain cache via invalidateHits.
	blockHits  *ruleHitTracker
	directHits *ruleHitTracker
	proxyHits  *ruleHitTracker
	// missHits counts rule-less connections per domain.
	missHits *ruleMissTracker
}

// newAdminRules builds the adapter. baseline holds the boot rule lists and
// must already be registered on the StateStore via SetBaseline.
func newAdminRules(r *router.Router, state *admin.StateStore, baseline map[admin.Category][]string, blockHits, directHits, proxyHits *ruleHitTracker, missHits *ruleMissTracker) *adminRules {
	return &adminRules{r: r, state: state, baseline: baseline, blockHits: blockHits, directHits: directHits, proxyHits: proxyHits, missHits: missHits}
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

func (a *adminRules) RuleSearch(category admin.Category, q string, offset, limit int, sortBy admin.RuleSort, dir admin.SortDir) ([]admin.RuleEntry, uint64, error) {
	rs, err := a.rules(category)
	if err != nil {
		return nil, 0, err
	}
	if sortBy != admin.RuleSortRule && sortBy != admin.RuleSortHits && sortBy != admin.RuleSortLastSeen {
		rules, total := rs.ListFiltered(q, offset, limit)
		entries := make([]admin.RuleEntry, len(rules))
		for i, rule := range rules {
			entries[i] = a.entry(category, rule)
		}
		return entries, total, nil
	}

	// Non-default ordering needs the full filtered set. Stat lookups are
	// O(1) per rule and every sort keeps file order as the stable tiebreak,
	// so the scan stays cheap even at 280k rules.
	all, total := rs.ListFiltered(q, 0, math.MaxInt)
	entries := make([]admin.RuleEntry, len(all))
	for i, rule := range all {
		entries[i] = a.entry(category, rule)
	}
	switch sortBy {
	case admin.RuleSortRule:
		sort.SliceStable(entries, func(i, j int) bool {
			if dir == admin.SortDirAsc {
				return entries[i].Rule < entries[j].Rule
			}
			return entries[i].Rule > entries[j].Rule
		})
	case admin.RuleSortHits:
		entries = partitionByStat(entries, func(e admin.RuleEntry) (int64, bool) {
			return int64(e.Count), e.Count > 0
		}, dir)
	case admin.RuleSortLastSeen:
		entries = partitionByStat(entries, func(e admin.RuleEntry) (int64, bool) {
			if e.LastSeen == nil {
				return 0, false
			}
			return e.LastSeen.UnixNano(), true
		}, dir)
	}
	if offset >= len(entries) {
		return []admin.RuleEntry{}, total, nil
	}
	end := offset + limit
	if end > len(entries) {
		end = len(entries)
	}
	return entries[offset:end], total, nil
}

// partitionByStat splits entries into those carrying a stat and those
// without, sorts the stat-bearing ones by key in dir, and keeps the rest in
// file order on the side the direction implies: a missing stat sorts below
// any real value, so desc appends it and asc prepends it.
func partitionByStat(entries []admin.RuleEntry, key func(admin.RuleEntry) (int64, bool), dir admin.SortDir) []admin.RuleEntry {
	type keyed struct {
		entry admin.RuleEntry
		key   int64
	}
	hit := make([]keyed, 0, 64)
	miss := make([]admin.RuleEntry, 0, len(entries))
	for _, e := range entries {
		if k, ok := key(e); ok {
			hit = append(hit, keyed{e, k})
		} else {
			miss = append(miss, e)
		}
	}
	sort.SliceStable(hit, func(i, j int) bool {
		if dir == admin.SortDirAsc {
			return hit[i].key < hit[j].key
		}
		return hit[i].key > hit[j].key
	})
	ordered := make([]admin.RuleEntry, 0, len(entries))
	if dir == admin.SortDirAsc {
		ordered = append(ordered, miss...)
	}
	for _, h := range hit {
		ordered = append(ordered, h.entry)
	}
	if dir != admin.SortDirAsc {
		ordered = append(ordered, miss...)
	}
	return ordered
}

// entry builds a listing row for one rule, attaching hit stats from the
// category's tracker when it has them.
func (a *adminRules) entry(category admin.Category, rule string) admin.RuleEntry {
	e := admin.RuleEntry{Rule: rule}
	t := a.tracker(category)
	if t == nil {
		return e
	}
	count, last := t.Lookup(rule)
	e.Count = count
	if !last.IsZero() {
		e.LastSeen = &last
	}
	return e
}

// tracker returns the hit tracker for one category, or nil when the
// category carries no hit stats.
func (a *adminRules) tracker(category admin.Category) *ruleHitTracker {
	switch category {
	case admin.CategoryBlock:
		return a.blockHits
	case admin.CategoryDirect:
		return a.directHits
	case admin.CategoryProxy:
		return a.proxyHits
	default:
		return nil
	}
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
			a.invalidateHits(category)
			return nil
		}
	}
	rs.Add(runtimeAdd...)
	rs.Compact()
	a.invalidateHits(category)
	return nil
}

// invalidateHits drops the rule hit cache of one category after a rule
// mutation. An empty category (reset-all) invalidates all trackers.
func (a *adminRules) invalidateHits(category admin.Category) {
	switch category {
	case admin.CategoryBlock:
		if a.blockHits != nil {
			a.blockHits.Invalidate()
		}
	case admin.CategoryDirect:
		if a.directHits != nil {
			a.directHits.Invalidate()
		}
	case admin.CategoryProxy:
		if a.proxyHits != nil {
			a.proxyHits.Invalidate()
		}
	case "":
		a.invalidateHits(admin.CategoryBlock)
		a.invalidateHits(admin.CategoryDirect)
		a.invalidateHits(admin.CategoryProxy)
	}
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
	a.invalidateHits(category)
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
	a.invalidateHits(category)
	return nil
}

// RuleMiss implements admin.RuleMissProvider, forwarding to the rule-miss
// tracker. byCount orders by connection count, otherwise by recency.
func (a *adminRules) RuleMiss(byCount bool, limit int) []admin.RuleHit {
	if a.missHits == nil {
		return nil
	}
	if byCount {
		return a.missHits.Top(limit)
	}
	return a.missHits.Recent(limit)
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
		h.stats.RecordDNS(req.Question[0].Name, router.ClientIPOf(req, w.RemoteAddr()))
	}
	h.Handler.ServeDNS(w, req)
}

func startAdminListener(ctx context.Context, wg *sync.WaitGroup, cfg config.SowerConfig, rules admin.RuleManager, configMgr admin.ConfigManager, stats *admin.Stats, errCh chan<- error, restartCh chan<- struct{}) error {
	if cfg.Admin.Disable || cfg.Admin.Addr == "" {
		return nil
	}

	password, temporary := resolveAdminPassword(cfg.Admin.Password.Value())
	srv := admin.NewServer(admin.Options{
		Password:          password,
		TemporaryPassword: temporary,
		Version:           version,
		Date:              date,
		Rules:             rules,
		Stats:             stats,
		SessionFile:       cfg.AdminSessionFile(),
		CookieSecure:      cfg.Admin.CookieSecure,
		Config:            configMgr,
		Restart:           restartFn(restartCh),
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
	wg.Add(1)
	go closeOnDone(ctx, wg, ln)
	go serveAndReport(errCh, "admin", func() error {
		return srv.Serve(ln)
	})
	return nil
}

// startSharedHTTPListener serves the admin console and the HTTP proxy from
// one listener on the DNS HTTP address. It is used when admin.addr exactly
// matches dns.serve:80.
func startSharedHTTPListener(ctx context.Context, wg *sync.WaitGroup, cfg config.SowerConfig, r *router.Router, rules admin.RuleManager, configMgr admin.ConfigManager, stats *admin.Stats, errCh chan<- error, restartCh chan<- struct{}) error {
	addr, ok := sharedAdminHTTPAddr(cfg)
	if !ok {
		return nil
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen shared http on %s: %w", addr, err)
	}
	slog.Info("service listening", "service", "http proxy + admin", "network", "tcp", "addr", addr)

	password, temporary := resolveAdminPassword(cfg.Admin.Password.Value())
	srv := admin.NewServer(admin.Options{
		Password:          password,
		TemporaryPassword: temporary,
		Version:           version,
		Date:              date,
		Rules:             rules,
		Stats:             stats,
		SessionFile:       cfg.AdminSessionFile(),
		CookieSecure:      cfg.Admin.CookieSecure,
		Config:            configMgr,
		Restart:           restartFn(restartCh),
	})
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), adminShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Warn("shutdown admin server", "error", err)
		}
	}()
	wg.Add(1)
	go closeOnDone(ctx, wg, ln)
	go serveAndReport(errCh, "http proxy + admin", func() error {
		return ServeSharedHTTP(ctx, ln, r, stats, srv, cfg.DNS.Serve, errCh)
	})
	return nil
}

// restartFn returns the admin restart callback: it coalesces requests into
// the buffered channel and returns immediately so the HTTP response is
// delivered before the process replaces itself. On platforms without
// in-place restart support it returns an error so the endpoint reports 500
// instead of acknowledging a restart that would fail.
func restartFn(restartCh chan<- struct{}) func() error {
	return func() error {
		if !restartSupported() {
			return errors.New("process restart is unsupported on this platform")
		}
		select {
		case restartCh <- struct{}{}:
		default: // restart already pending
		}
		return nil
	}
}
