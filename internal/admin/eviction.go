package admin

import (
	"container/heap"
	"time"
)

// evictionCandidate is a potential least-recently-seen entry. The heap keeps
// its newest element at the root, allowing a single scan to retain only the
// oldest candidates required for a batch eviction.
type evictionCandidate struct {
	key      string
	lastSeen time.Time
}

type oldestCandidates []evictionCandidate

func (h oldestCandidates) Len() int { return len(h) }

func (h oldestCandidates) Less(i, j int) bool {
	if h[i].lastSeen.Equal(h[j].lastSeen) {
		return h[i].key > h[j].key
	}
	return h[i].lastSeen.After(h[j].lastSeen)
}

func (h oldestCandidates) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *oldestCandidates) Push(x any) { *h = append(*h, x.(evictionCandidate)) }

func (h *oldestCandidates) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

func older(a, b evictionCandidate) bool {
	if a.lastSeen.Equal(b.lastSeen) {
		return a.key < b.key
	}
	return a.lastSeen.Before(b.lastSeen)
}

func keepOldest(candidates *oldestCandidates, candidate evictionCandidate, limit int) {
	if candidates.Len() < limit {
		heap.Push(candidates, candidate)
		return
	}
	if older(candidate, (*candidates)[0]) {
		heap.Pop(candidates)
		heap.Push(candidates, candidate)
	}
}

// evictOldestDomainsLocked frees a batch of least-recently-seen domains so
// a high-cardinality stream amortizes the O(N) scan across many insertions.
func (s *Stats) evictOldestDomainsLocked(count int) {
	count = min(count, len(s.domains))
	if count == 0 {
		return
	}
	candidates := make(oldestCandidates, 0, count)
	for domain, d := range s.domains {
		keepOldest(&candidates, evictionCandidate{key: domain, lastSeen: d.lastSeen}, count)
	}
	for _, candidate := range candidates {
		delete(s.domains, candidate.key)
	}
}

// evictOldestClientsLocked frees a batch of least-recently-seen client totals.
func (s *Stats) evictOldestClientsLocked(count int) {
	count = min(count, len(s.clients))
	if count == 0 {
		return
	}
	candidates := make(oldestCandidates, 0, count)
	for ip, c := range s.clients {
		keepOldest(&candidates, evictionCandidate{key: ip, lastSeen: c.lastSeen}, count)
	}
	for _, candidate := range candidates {
		delete(s.clients, candidate.key)
	}
}
