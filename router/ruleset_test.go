package router

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func TestRuleSetAddListCount(t *testing.T) {
	rs := NewRuleSet("example.com", "**.cn")

	rs.Add("github.com", "example.com") // duplicate example.com must be ignored
	if got := rs.Count(); got != 3 {
		t.Fatalf("unexpected count: %d", got)
	}
	rules := rs.List()
	if len(rules) != 3 {
		t.Fatalf("unexpected rule list: %v", rules)
	}
	seen := make(map[string]bool, len(rules))
	for _, r := range rules {
		seen[r] = true
	}
	for _, want := range []string{"example.com", "**.cn", "github.com"} {
		if !seen[want] {
			t.Fatalf("missing rule %q in %v", want, rules)
		}
	}
}

func TestRuleSetMatch(t *testing.T) {
	rs := NewRuleSet("example.com", "**.example.org", "*")
	if !rs.Match("example.com") {
		t.Fatal("expected example.com to match")
	}
	if rs.Match("sub.example.com") {
		t.Fatal("expected sub.example.com to not match bare example.com rule")
	}
	if !rs.Match("sub.example.org") {
		t.Fatal("expected sub.example.org to match wildcard rule")
	}
	if !rs.Match("anything") {
		t.Fatal("expected * to match anything")
	}
	if rs.Match("github.com") {
		t.Fatal("expected github.com to not match")
	}
}

func TestRuleSetMatchRule(t *testing.T) {
	rs := NewRuleSet("example.com", "**.example.org", "ads.*.com")

	cases := []struct {
		item string
		want string
	}{
		{"example.com", "example.com"},
		{"EXAMPLE.COM.", "example.com"}, // normalized like Match
		{"sub.example.com", ""},
		{"sub.example.org", "**.example.org"},
		{"a.b.example.org", "**.example.org"},
		{"example.org", "**.example.org"},
		{"ads.cdn.com", "ads.*.com"},
		{"ads.a.b.com", ""}, // mid-rule * matches one label only
		{"github.com", ""},
	}
	for _, tc := range cases {
		got, ok := rs.MatchRule(tc.item)
		if tc.want == "" {
			if ok {
				t.Fatalf("MatchRule(%q) = %q, want no match", tc.item, got)
			}
			continue
		}
		if !ok || got != tc.want {
			t.Fatalf("MatchRule(%q) = %q, %v; want %q", tc.item, got, ok, tc.want)
		}
	}

	// MatchRule agrees with Match on every case the tree sees
	for _, item := range []string{"example.com", "sub.example.com", "sub.example.org", "anything"} {
		if got, ok := rs.MatchRule(item); ok != rs.Match(item) {
			t.Fatalf("MatchRule(%q) = %q, %v diverges from Match %v", item, got, ok, rs.Match(item))
		}
	}

	var nilRS *RuleSet
	if _, ok := nilRS.MatchRule("x"); ok {
		t.Fatal("expected nil RuleSet MatchRule to miss")
	}
}

func TestRuleSetRemoveRebuildsTree(t *testing.T) {
	rs := NewRuleSet("example.com", "github.com", "**.cn")

	if !rs.Remove("example.com") {
		t.Fatal("expected remove to report present rule")
	}
	if rs.Match("example.com") {
		t.Fatal("expected removed rule to stop matching")
	}
	if !rs.Match("github.com") || !rs.Match("a.cn") {
		t.Fatal("expected remaining rules to keep matching")
	}
	if got := rs.Count(); got != 2 {
		t.Fatalf("unexpected count after remove: %d", got)
	}

	if rs.Remove("missing.example") {
		t.Fatal("expected remove to report absent rule")
	}
	if got := rs.List(); len(got) != 2 {
		t.Fatalf("unexpected list after absent remove: %v", got)
	}
}

func TestRuleSetNilMatch(t *testing.T) {
	var rs *RuleSet
	if rs.Match("anything") {
		t.Fatal("expected nil RuleSet to never match")
	}
}

func TestRuleSetConcurrentAccess(t *testing.T) {
	rs := NewRuleSet("seed.example.com")

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rule := fmt.Sprintf("host%d.example.com", i)
			for j := 0; j < 200; j++ {
				rs.Add(rule)
				_ = rs.Match(rule)
				_ = rs.Count()
				_ = rs.List()
				if j%10 == 0 {
					rs.Remove(fmt.Sprintf("host%d.example.com", (i+1)%16))
					rs.Add(fmt.Sprintf("host%d.example.com", (i+1)%16))
				}
			}
		}(i)
	}
	wg.Wait()

	if !rs.Match("seed.example.com") {
		t.Fatal("expected seed rule to survive concurrent access")
	}
}

func TestRuleSetReplaceConcurrentWithMatch(t *testing.T) {
	rs := NewRuleSet("seed.example.com")
	var stop atomic.Bool
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				_ = rs.Match("seed.example.com")
				_ = rs.Match("replacement.example.com")
			}
		}()
	}

	for i := 0; i < 100; i++ {
		rs.Replace(fmt.Sprintf("replacement%d.example.com", i), "replacement.example.com")
	}
	stop.Store(true)
	wg.Wait()

	if !rs.Match("replacement.example.com") || rs.Match("seed.example.com") {
		t.Fatalf("unexpected final rules: %v", rs.List())
	}
}

func TestListFilteredPaginationAndSearch(t *testing.T) {
	rs := NewRuleSet("example.com", "**.cn", "GitHub.com", "mail.google.com", "a.cn")

	// empty query: paginated over the full list
	rules, total := rs.ListFiltered("", 1, 2)
	if total != 5 {
		t.Fatalf("expected total 5, got %d", total)
	}
	if len(rules) != 2 || rules[0] != "**.cn" || rules[1] != "GitHub.com" {
		t.Fatalf("unexpected page: %v", rules)
	}

	// case-insensitive substring search
	rules, total = rs.ListFiltered("GITHUB", 0, 10)
	if total != 1 || len(rules) != 1 || rules[0] != "GitHub.com" {
		t.Fatalf("unexpected search result: %v (total %d)", rules, total)
	}

	// search with pagination ("cn" matches **.cn and a.cn)
	rules, total = rs.ListFiltered("cn", 0, 1)
	if total != 2 || len(rules) != 1 || rules[0] != "**.cn" {
		t.Fatalf("unexpected search page: %v (total %d)", rules, total)
	}
	rules, _ = rs.ListFiltered("cn", 1, 1)
	if len(rules) != 1 || rules[0] != "a.cn" {
		t.Fatalf("unexpected second page: %v", rules)
	}

	// offset beyond the end yields an empty page with the full total
	rules, total = rs.ListFiltered("", 99, 10)
	if len(rules) != 0 || total != 5 {
		t.Fatalf("expected empty page with total 5, got %v (total %d)", rules, total)
	}
}
