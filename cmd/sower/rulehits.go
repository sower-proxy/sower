package main

import (
	"strings"
	"sync"
	"time"

	"github.com/sower-proxy/sower/router"
)

const (
	// maxRuleHitDomains bounds the domain -> rule resolution cache. The
	// cache exists so a hit domain is resolved against the full rule set
	// only once; repeats resolve in O(1). When full, the whole cache is
	// dropped: resolution cost is amortized and bounded, losing a cache is
	// cheaper than an LRU scan on the hot path.
	maxRuleHitDomains = 10000
	// maxRuleHits bounds the distinct rules retained with hit counts for the
	// block set. A pathological stream can hit thousands of wildcard rules;
	// the map evicts its least-recently-seen entry once full.
	maxRuleHits = 10000
	// maxRuleHitsWide bounds retained hit rules for the direct/proxy sets,
	// which are far larger and see far more distinct hits than block.
	maxRuleHitsWide = 50000
)

// ruleHitTracker counts routing decisions per matched rule of one rule set.
// It resolves the rule for a domain through the rule set's suffix tree —
// once per domain, cached afterwards — so the linear rule scan never runs
// on the connection hot path. Rule mutations invalidate the domain cache
// through Invalidate; hit totals survive rule removal and are reset only
// by restart.
type ruleHitTracker struct {
	ruleSet *router.RuleSet
	maxHits int

	mu      sync.Mutex
	domains map[string]string // domain -> matched rule
	hits    map[string]*ruleHit
}

type ruleHit struct {
	count uint64
	last  time.Time
}

// newRuleHitTracker builds a tracker over one rule set. The rule set is
// read under its own lock; the tracker never mutates it.
func newRuleHitTracker(ruleSet *router.RuleSet, maxHits int) *ruleHitTracker {
	return &ruleHitTracker{
		ruleSet: ruleSet,
		maxHits: maxHits,
		domains: make(map[string]string),
		hits:    make(map[string]*ruleHit),
	}
}

// OnHit counts one routing decision for the domain, resolving the matched
// rule on first sight of the domain and caching the mapping.
func (t *ruleHitTracker) OnHit(domain string) {
	domain = normalizeHitDomain(domain)
	if domain == "" {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	rule, ok := t.domains[domain]
	if !ok {
		if len(t.domains) >= maxRuleHitDomains {
			t.domains = make(map[string]string)
		}
		var matched bool
		rule, matched = t.ruleSet.MatchRuleFast(domain)
		if !matched || rule == "" {
			return // rule set changed under us; a later decision will retry
		}
		t.domains[domain] = rule
	}

	h := t.hits[rule]
	if h == nil {
		if len(t.hits) >= t.maxHits {
			t.evictOldestHitLocked()
		}
		h = &ruleHit{}
		t.hits[rule] = h
	}
	h.count++
	h.last = time.Now()
}

// evictOldestHitLocked drops the least-recently-seen rule hit when the map
// is full. It scans the map — acceptable here because the eviction happens
// only after maxHits distinct rules have been hit.
func (t *ruleHitTracker) evictOldestHitLocked() {
	var oldest string
	var oldestAt time.Time
	for rule, h := range t.hits {
		if oldest == "" || h.last.Before(oldestAt) {
			oldest, oldestAt = rule, h.last
		}
	}
	delete(t.hits, oldest)
}

// Invalidate drops the domain -> rule cache after a rule-set mutation, so
// subsequent decisions re-resolve against the updated rule set. Hit counts
// are kept: they describe what has been routed since start.
func (t *ruleHitTracker) Invalidate() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.domains = make(map[string]string)
}

// Lookup reports the hit count and most recent hit time of one rule; zero
// values when the rule never hit.
func (t *ruleHitTracker) Lookup(rule string) (uint64, time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if h := t.hits[rule]; h != nil {
		return h.count, h.last
	}
	return 0, time.Time{}
}

// normalizeHitDomain lowercases and strips the trailing dot of a domain.
func normalizeHitDomain(domain string) string {
	domain = strings.TrimSpace(domain)
	domain = strings.TrimSuffix(domain, ".")
	return strings.ToLower(domain)
}
