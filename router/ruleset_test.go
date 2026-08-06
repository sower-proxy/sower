package router

import (
	"fmt"
	"sync"
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
