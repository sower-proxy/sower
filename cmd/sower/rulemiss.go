package main

import (
	"sort"
	"sync"
	"time"

	"github.com/sower-proxy/sower/internal/admin"
)

const (
	// maxRuleMissDomains bounds the total distinct domains retained with
	// access stats for connections that matched no rule. Detection-based
	// and fallback traffic covers most of the long tail, so each shard
	// evicts its least-recently-seen entry once full.
	maxRuleMissDomains = 50000
	// missShards stripes the tracker so the connection hot path (OnMiss)
	// never contends on a single global lock. Shards are power-of-two for a
	// mask-based index; the read path (Top/Recent) aggregates all shards,
	// which is fine because it runs only on admin queries.
	missShards = 16
	// missShardLimit is the per-shard capacity: total budget spread evenly.
	missShardLimit = maxRuleMissDomains / missShards
	ruleMissTop    = 100
)

type ruleMiss struct {
	count uint64
	last  time.Time
}

// ruleMissTracker counts connections per domain for domains that matched no
// block/direct/proxy rule — detection-based direct or fallback proxy. It is
// fed from the router's rule-miss observer on the connection path. Domains
// are striped across shards by hash so concurrent connections update
// independent maps; each shard holds a short critical section.
type ruleMissTracker struct {
	shards [missShards]missShard
}

type missShard struct {
	mu    sync.Mutex
	miss  map[string]*ruleMiss
	limit int
}

// newRuleMissTracker builds an empty tracker.
func newRuleMissTracker() *ruleMissTracker {
	t := &ruleMissTracker{}
	for i := range t.shards {
		t.shards[i].miss = make(map[string]*ruleMiss)
		t.shards[i].limit = missShardLimit
	}
	return t
}

// missShardIndex maps a domain to its shard with FNV-1a (cheap, no map
// allocation) masked to the power-of-two shard count.
func missShardIndex(domain string) uint32 {
	const (
		fnvOffset = 2166136261
		fnvPrime  = 16777619
	)
	var h uint32 = fnvOffset
	for i := 0; i < len(domain); i++ {
		h ^= uint32(domain[i])
		h *= fnvPrime
	}
	return h & (missShards - 1)
}

// OnMiss counts one rule-less connection for the domain.
func (t *ruleMissTracker) OnMiss(domain string) {
	domain = normalizeHitDomain(domain)
	if domain == "" {
		return
	}

	s := &t.shards[missShardIndex(domain)]
	s.mu.Lock()
	defer s.mu.Unlock()

	m := s.miss[domain]
	if m == nil {
		if len(s.miss) >= s.limit {
			s.evictOldestLocked()
		}
		m = &ruleMiss{}
		s.miss[domain] = m
	}
	m.count++
	m.last = time.Now()
}

// evictOldestLocked drops the least-recently-seen domain of one shard when
// it is full. The scan is bounded by the per-shard capacity (~3k), which is
// 16x cheaper than scanning the whole map, and eviction happens only after
// a shard fills up.
func (s *missShard) evictOldestLocked() {
	var oldest string
	var oldestAt time.Time
	for domain, m := range s.miss {
		if oldest == "" || m.last.Before(oldestAt) {
			oldest, oldestAt = domain, m.last
		}
	}
	delete(s.miss, oldest)
}

// Top returns up to limit domains ordered by connection count, descending,
// with the most recent access as the tiebreak.
func (t *ruleMissTracker) Top(limit int) []admin.RuleHit {
	out := t.snapshot()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].LastSeen.After(out[j].LastSeen)
	})
	return truncateRuleHits(out, limit)
}

// Recent returns up to limit domains ordered by most recent access,
// descending, with the connection count as the tiebreak.
func (t *ruleMissTracker) Recent(limit int) []admin.RuleHit {
	out := t.snapshot()
	sort.Slice(out, func(i, j int) bool {
		if !out[i].LastSeen.Equal(out[j].LastSeen) {
			return out[i].LastSeen.After(out[j].LastSeen)
		}
		return out[i].Count > out[j].Count
	})
	return truncateRuleHits(out, limit)
}

// snapshot aggregates every shard into one slice under per-shard locks.
func (t *ruleMissTracker) snapshot() []admin.RuleHit {
	out := make([]admin.RuleHit, 0, maxRuleMissDomains)
	for i := range t.shards {
		s := &t.shards[i]
		s.mu.Lock()
		for domain, m := range s.miss {
			out = append(out, admin.RuleHit{Rule: domain, Count: m.count, LastSeen: m.last})
		}
		s.mu.Unlock()
	}
	return out
}

func truncateRuleHits(out []admin.RuleHit, limit int) []admin.RuleHit {
	if limit <= 0 || limit > len(out) {
		limit = len(out)
	}
	if limit > ruleMissTop {
		limit = ruleMissTop
	}
	return out[:limit]
}
