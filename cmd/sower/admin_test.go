package main

import (
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/sower-proxy/sower/config"
	"github.com/sower-proxy/sower/internal/admin"
	"github.com/sower-proxy/sower/router"
)

// bootAdapter simulates one process lifetime: a fresh router, baseline
// snapshot, state deltas applied, and the admin adapter on top.
func bootAdapter(t *testing.T, statePath string) (*adminRules, *admin.StateStore) {
	t.Helper()
	r := newTestRouter()
	state := admin.LoadStateStore(statePath)
	baseline := snapshotBaseline(r)
	state.SetBaseline(baseline)
	applyRuleDeltas(r, state)
	return newAdminRules(r, state, baseline), state
}

func TestAdminRulesSurviveRestart(t *testing.T) {
	t.Parallel()
	statePath := filepath.Join(t.TempDir(), "admin-state.json")

	// First lifetime: add a proxy rule, tombstone the baseline block rule.
	a, _ := bootAdapter(t, statePath)
	if err := a.RuleAdd(admin.CategoryProxy, "**.example.com"); err != nil {
		t.Fatal(err)
	}
	found, err := a.RuleRemove(admin.CategoryBlock, "example.com")
	if err != nil || !found {
		t.Fatalf("tombstone baseline rule: found=%v err=%v", found, err)
	}

	// Second lifetime: the state file replays both changes.
	a2, _ := bootAdapter(t, statePath)
	if got := a2.r.ProxyRule.Count(); got != 1 {
		t.Fatalf("expected 1 proxy rule after restart, got %d", got)
	}
	if got := a2.r.BlockRule.Count(); got != 0 {
		t.Fatalf("expected block rule to stay removed after restart, got %d", got)
	}
	changes := a2.RuleChanges()
	if d := changes.Rules[admin.CategoryProxy]; len(d.Add) != 1 || d.Add[0] != "**.example.com" {
		t.Fatalf("unexpected proxy delta: %+v", d)
	}
	if d := changes.Rules[admin.CategoryBlock]; len(d.Remove) != 1 || d.Remove[0] != "example.com" {
		t.Fatalf("unexpected block delta: %+v", d)
	}
}

func TestAdminRulesResetRestoresBaseline(t *testing.T) {
	t.Parallel()
	statePath := filepath.Join(t.TempDir(), "admin-state.json")

	a, _ := bootAdapter(t, statePath)
	if err := a.RuleAdd(admin.CategoryProxy, "**.example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RuleRemove(admin.CategoryBlock, "example.com"); err != nil {
		t.Fatal(err)
	}

	if err := a.RuleReset(""); err != nil {
		t.Fatal(err)
	}
	if got := a.r.BlockRule.Count(); got != 1 {
		t.Fatalf("expected baseline block rule restored, got %d", got)
	}
	if got := a.r.ProxyRule.Count(); got != 0 {
		t.Fatalf("expected admin-added proxy rule dropped, got %d", got)
	}
	changes := a.RuleChanges()
	for cat, d := range changes.Rules {
		if len(d.Add) != 0 || len(d.Remove) != 0 {
			t.Fatalf("expected empty delta for %s after reset, got %+v", cat, d)
		}
	}
}

func TestApplyConfigOverrides(t *testing.T) {

	cfg := config.SowerConfig{LogLevel: slog.LevelInfo}
	cfg.DNS.Upstream = "8.8.8.8"
	cfg.DNS.Fallback = "223.5.5.5"

	applyConfigOverrides(&cfg, admin.ConfigOverrides{
		LogLevel:    "debug",
		DNSUpstream: "1.1.1.1",
		DNSFallback: "9.9.9.9",
	})
	if cfg.LogLevel != slog.LevelDebug {
		t.Fatalf("log level not overridden: %v", cfg.LogLevel)
	}
	if cfg.DNS.Upstream != "1.1.1.1" || cfg.DNS.Fallback != "9.9.9.9" {
		t.Fatalf("dns not overridden: %v %v", cfg.DNS.Upstream, cfg.DNS.Fallback)
	}
	if logLevel.Level() != slog.LevelDebug {
		t.Fatalf("runtime log level not applied: %v", logLevel.Level())
	}

	// Invalid values are dropped, keeping the previous config.
	applyConfigOverrides(&cfg, admin.ConfigOverrides{
		LogLevel:    "bogus",
		DNSUpstream: "not-an-ip",
	})
	if cfg.LogLevel != slog.LevelDebug {
		t.Fatalf("invalid log level must be ignored: %v", cfg.LogLevel)
	}
	if cfg.DNS.Upstream != "1.1.1.1" {
		t.Fatalf("invalid dns upstream must be ignored: %v", cfg.DNS.Upstream)
	}
}

func TestAdminConfigViewAndApply(t *testing.T) {
	logLevel.Set(slog.LevelInfo)
	base := config.SowerConfig{LogLevel: slog.LevelInfo}
	base.Remote.Type = "sower"
	base.Remote.Addr = "proxy.example.com"
	base.Remote.Password = "supersecret"
	base.DNS.Serve = "127.0.0.1"
	base.DNS.Upstream = "8.8.8.8"
	base.DNS.Fallback = "223.5.5.5"
	base.Socks5.Addr = "127.0.0.1:1080"
	base.Admin.Addr = "127.0.0.1:19090"
	base.Admin.Password = "adminsecret"
	base.Admin.StateFile = "/etc/sower/admin-state.json"

	state := admin.LoadStateStore("")
	ac := newAdminConfig(base, state, newTestRouter())

	// The view renders all sections and never exposes secret values.
	view := ac.ConfigView()
	if len(view.Sections) != 5 {
		t.Fatalf("expected 5 sections, got %d", len(view.Sections))
	}
	for _, sec := range view.Sections {
		for _, f := range sec.Fields {
			if f.Value == "supersecret" || f.Value == "adminsecret" {
				t.Fatalf("secret value exposed for %s", f.Key)
			}
			if f.Key == "remote.password" && (!f.Secret || !f.Configured) {
				t.Fatalf("remote.password must be secret+configured: %+v", f)
			}
		}
	}

	// Apply a log level and DNS change at the correct revision.
	debug := "debug"
	upstream := "1.1.1.1"
	view, err := ac.ApplyConfigChanges(admin.ConfigChanges{LogLevel: &debug, DNSUpstream: &upstream}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if view.Revision != 1 {
		t.Fatalf("expected revision 1, got %d", view.Revision)
	}
	if logLevel.Level() != slog.LevelDebug {
		t.Fatalf("log level not applied: %v", logLevel.Level())
	}
	if ac.effective.DNS.Upstream != "1.1.1.1" {
		t.Fatalf("dns upstream not applied: %v", ac.effective.DNS.Upstream)
	}

	// Stale revision is rejected.
	if _, err := ac.ApplyConfigChanges(admin.ConfigChanges{LogLevel: &debug}, 0); err == nil {
		t.Fatal("expected revision mismatch error")
	}

	// Clearing the upstream override reverts to the base config.
	empty := ""
	if _, err := ac.ApplyConfigChanges(admin.ConfigChanges{DNSUpstream: &empty}, 1); err != nil {
		t.Fatal(err)
	}
	if ac.effective.DNS.Upstream != "8.8.8.8" {
		t.Fatalf("clearing override must revert to base, got %v", ac.effective.DNS.Upstream)
	}

	// Clearing a log-level override returns the handler to the base config.
	if _, err := ac.ApplyConfigChanges(admin.ConfigChanges{LogLevel: &empty}, 2); err != nil {
		t.Fatal(err)
	}
	if logLevel.Level() != slog.LevelInfo {
		t.Fatalf("clearing log level must restore base level, got %v", logLevel.Level())
	}
}

func TestAdminRulesRestoresBaselineOrder(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "admin-state.json")
	r := newTestRouter()
	r.ProxyRule = router.NewRuleSet("**.example.com", "api.example.com")

	state := admin.LoadStateStore(statePath)
	baseline := snapshotBaseline(r)
	state.SetBaseline(baseline)
	rules := newAdminRules(r, state, baseline)

	if _, err := rules.RuleRemove(admin.CategoryProxy, "**.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := rules.RuleAdd(admin.CategoryProxy, "**.example.com"); err != nil {
		t.Fatal(err)
	}
	matched, ok := r.ProxyRule.MatchRule("api.example.com")
	if !ok || matched != "**.example.com" {
		t.Fatalf("restored baseline order changed MatchRule: matched=%q ok=%v", matched, ok)
	}
}

func TestAdminRulesBatchRemovePersistFailureLeavesRuntimeUntouched(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := newTestRouter()
	r.ProxyRule = router.NewRuleSet("one.example", "two.example")
	state := admin.LoadStateStore(filepath.Join(blocker, "admin-state.json"))
	baseline := snapshotBaseline(r)
	state.SetBaseline(baseline)
	rules := newAdminRules(r, state, baseline)

	removed, err := rules.RuleRemoveMany(admin.CategoryProxy, "one.example", "two.example")
	if err == nil {
		t.Fatal("expected persistence failure")
	}
	if removed != nil {
		t.Fatalf("runtime removals must be nil on persist failure: %v", removed)
	}
	if got := r.ProxyRule.List(); len(got) != 2 || got[0] != "one.example" || got[1] != "two.example" {
		t.Fatalf("runtime changed despite persist failure: %v", got)
	}
	if delta := state.Delta(admin.CategoryProxy); len(delta.Add) != 0 || len(delta.Remove) != 0 {
		t.Fatalf("state changed despite persist failure: %+v", delta)
	}
}

func TestAdminRulesBatchRemoveRebuildsOnceInOrder(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "admin-state.json")
	r := newTestRouter()
	r.ProxyRule = router.NewRuleSet("one.example", "two.example", "three.example")
	state := admin.LoadStateStore(statePath)
	baseline := snapshotBaseline(r)
	state.SetBaseline(baseline)
	rules := newAdminRules(r, state, baseline)
	if err := rules.RuleAdd(admin.CategoryProxy, "four.example"); err != nil {
		t.Fatal(err)
	}

	removed, err := rules.RuleRemoveMany(admin.CategoryProxy, "two.example", "four.example")
	if err != nil || len(removed) != 2 {
		t.Fatalf("batch remove: removed=%v err=%v", removed, err)
	}
	if got := r.ProxyRule.List(); len(got) != 2 || got[0] != "one.example" || got[1] != "three.example" {
		t.Fatalf("batch rebuild did not preserve remaining order: %v", got)
	}
}

func TestAdminRulesConcurrentMutationsRemainConsistent(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "admin-state.json")
	r := newTestRouter()
	r.ProxyRule = router.NewRuleSet("one.example", "two.example")
	state := admin.LoadStateStore(statePath)
	baseline := snapshotBaseline(r)
	state.SetBaseline(baseline)
	rules := newAdminRules(r, state, baseline)

	var wg sync.WaitGroup
	errs := make(chan error, 48)
	for i := 0; i < 48; i++ {
		wg.Add(1)
		go runAdminRulesMutation(i, rules, errs, &wg)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	want := rules.effectiveRules(admin.CategoryProxy)
	if got := r.ProxyRule.List(); !slices.Equal(got, want) {
		t.Fatalf("runtime rules diverged from state: got=%v want=%v", got, want)
	}

	// A new process must reconstruct the same effective order from disk.
	r2 := newTestRouter()
	r2.ProxyRule = router.NewRuleSet("one.example", "two.example")
	state2 := admin.LoadStateStore(statePath)
	baseline2 := snapshotBaseline(r2)
	state2.SetBaseline(baseline2)
	applyRuleDeltas(r2, state2)
	if got := r2.ProxyRule.List(); !slices.Equal(got, want) {
		t.Fatalf("reloaded rules diverged from state: got=%v want=%v", got, want)
	}
}

func runAdminRulesMutation(i int, rules *adminRules, errs chan<- error, wg *sync.WaitGroup) {
	defer wg.Done()
	var err error
	switch i % 3 {
	case 0:
		err = rules.RuleAdd(admin.CategoryProxy, "custom.example")
	case 1:
		_, err = rules.RuleRemoveMany(admin.CategoryProxy, "custom.example", "one.example")
	default:
		err = rules.RuleReset(admin.CategoryProxy)
	}
	if err != nil {
		errs <- err
	}
}
