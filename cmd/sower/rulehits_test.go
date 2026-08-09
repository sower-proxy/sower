package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/sower-proxy/sower/router"
)

func TestRuleHitTrackerCountsPerRule(t *testing.T) {
	rs := router.NewRuleSet("**.ads.example", "tracker.example.net")
	tracker := newRuleHitTracker(rs, maxRuleHits)

	tracker.OnHit("a.ads.example")
	tracker.OnHit("b.ads.example")
	tracker.OnHit("b.ads.example")
	tracker.OnHit("tracker.example.net")
	tracker.OnHit("unmatched.example") // matches no rule, ignored

	if count, _ := tracker.Lookup("**.ads.example"); count != 3 {
		t.Fatalf("expected 3 hits for **.ads.example, got %d", count)
	}
	if count, _ := tracker.Lookup("tracker.example.net"); count != 1 {
		t.Fatalf("expected 1 hit for tracker.example.net, got %d", count)
	}
	if count, _ := tracker.Lookup("unmatched.example"); count != 0 {
		t.Fatalf("unmatched domain must not be counted, got %d", count)
	}
}

func TestRuleHitTrackerLookup(t *testing.T) {
	rs := router.NewRuleSet("**.ads.example")
	tracker := newRuleHitTracker(rs, maxRuleHits)

	if count, last := tracker.Lookup("**.ads.example"); count != 0 || !last.IsZero() {
		t.Fatalf("expected zero stat for unknown rule, got %d %v", count, last)
	}

	tracker.OnHit("a.ads.example")
	tracker.OnHit("b.ads.example")

	count, last := tracker.Lookup("**.ads.example")
	if count != 2 {
		t.Fatalf("expected count 2, got %d", count)
	}
	if last.IsZero() || time.Since(last) > time.Minute {
		t.Fatalf("unexpected last seen: %v", last)
	}
}

func TestRuleHitTrackerInvalidateReparses(t *testing.T) {
	rs := router.NewRuleSet("example.com")
	tracker := newRuleHitTracker(rs, maxRuleHits)

	tracker.OnHit("evil.example.com") // example.com does not match subdomains
	if count, _ := tracker.Lookup("**.example.com"); count != 0 {
		t.Fatalf("expected no hits before invalidation, got %d", count)
	}

	rs.Add("**.example.com")
	tracker.Invalidate()
	tracker.OnHit("evil.example.com")

	if count, _ := tracker.Lookup("**.example.com"); count != 1 {
		t.Fatalf("expected 1 hit after invalidation, got %d", count)
	}
}

func TestRuleHitTrackerCapEvictsOldest(t *testing.T) {
	rs := router.NewRuleSet("fresh.example")
	tracker := newRuleHitTracker(rs, maxRuleHits)

	now := time.Now()
	tracker.mu.Lock()
	for i := 0; i < maxRuleHits; i++ {
		tracker.hits[fmt.Sprintf("rule-%d", i)] = &ruleHit{count: 1, last: now.Add(time.Duration(i) * time.Nanosecond)}
	}
	tracker.mu.Unlock()

	tracker.OnHit("fresh.example")

	tracker.mu.Lock()
	n := len(tracker.hits)
	_, oldestPresent := tracker.hits["rule-0"]
	_, freshPresent := tracker.hits["fresh.example"]
	tracker.mu.Unlock()
	if n != maxRuleHits {
		t.Fatalf("expected %d hits after eviction, got %d", maxRuleHits, n)
	}
	if oldestPresent || !freshPresent {
		t.Fatalf("unexpected eviction: oldest=%v fresh=%v", oldestPresent, freshPresent)
	}
}
