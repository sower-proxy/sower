package admin

import (
	"container/heap"
	"sort"
	"time"
)

// Snapshot returns the window view of the current stats, advancing the
// history ring along the way. Domains stale for over staleDomainAge are
// hidden from the window but kept in the map for the cumulative Totals view.
// sort selects the domain ordering, source restricts domains to one entry
// point, and client restricts them to one client IP; invalid values fall back
// to DomainSortBytes and SourceAll, and an empty client disables the filter.
// Clients aggregates per-client counters over the source-filtered view and
// is intentionally unaffected by the client filter, so the console's client
// chips stay stable while one is selected.
func (s *Stats) Snapshot(sort DomainSort, source Source, client string) TrafficSnapshot {
	s.sample()

	now := time.Now()
	snap := TrafficSnapshot{
		Uptime:     int64(now.Sub(s.start).Seconds()),
		DNSQueries: s.dnsQueries.Load(),
		BytesUp:    s.bytesUp.Load(),
		BytesDown:  s.bytesDown.Load(),
		Domains:    []DomainStat{},
	}
	snap.Conns.HTTP = s.connsHTTP.Load()
	snap.Conns.HTTPS = s.connsHTTPS.Load()
	snap.Conns.Socks5 = s.connsSocks.Load()
	snap.Active.HTTP = s.activeHTTP.Load()
	snap.Active.HTTPS = s.activeHTTPS.Load()
	snap.Active.Socks5 = s.activeSocks.Load()
	snap.RuleHits.Block = s.ruleBlock.Load()
	snap.RuleHits.Direct = s.ruleDirect.Load()
	snap.RuleHits.Proxy = s.ruleProxy.Load()
	snap.Errors.Dial = s.dialFailed.Load()
	snap.Errors.DNS = s.dnsFailed.Load()
	snap.Errors.Accept = s.acceptFailed.Load()
	snap.Events = s.recentErrorEvents()
	snap.Rates.BytesUpPerSec, snap.Rates.BytesDownPerSec, snap.Rates.DNSPerSec, snap.Rates.ConnsPerSec = s.rates()
	snap.System.Goroutines = uint64(goroutineCount())
	snap.System.HeapAlloc = heapAlloc()

	s.mu.Lock()
	clientAgg := make(map[string]*ClientStat)
	for domain, d := range s.domains {
		if now.Sub(d.lastSeen) > staleDomainAge {
			// Window view only: the entry stays in the map so the cumulative
			// Totals view keeps its from-start counters.
			continue
		}
		ds := DomainStat{
			Domain:    domain,
			Conns:     d.conns,
			BytesUp:   d.bytesUp,
			BytesDown: d.bytesDown,
			LastSeen:  d.lastSeen,
		}
		if source != SourceAll {
			src, ok := d.bySource[source] // nil map read is safe
			if !ok || (src.conns == 0 && src.bytesUp == 0 && src.bytesDown == 0) {
				continue
			}
			ds.Conns = src.conns
			ds.BytesUp = src.bytesUp
			ds.BytesDown = src.bytesDown
			ds.LastSeen = src.lastSeen
		}
		// Aggregate the client chips over the source-filtered view but before
		// the client filter: the chip row is a navigation device and must stay
		// stable (and globally counted) while a client filter is active.
		for ip, c := range d.byClient {
			agg := clientAgg[ip]
			if agg == nil {
				agg = &ClientStat{IP: ip}
				clientAgg[ip] = agg
			}
			agg.Conns += c.conns
			agg.BytesUp += c.bytesUp
			agg.BytesDown += c.bytesDown
			if c.lastSeen.After(agg.LastSeen) {
				agg.LastSeen = c.lastSeen
			}
		}

		if client != "" {
			dc, ok := d.byClient[client] // nil map read is safe
			if !ok || (dc.conns == 0 && dc.bytesUp == 0 && dc.bytesDown == 0) {
				continue
			}
			ds.Conns = dc.conns
			ds.BytesUp = dc.bytesUp
			ds.BytesDown = dc.bytesDown
			ds.LastSeen = dc.lastSeen
			ds.LastClientIP = client
		} else {
			// Most recent client across all sources for the domain; the
			// per-client map has no source dimension, so this is the best
			// approximation for a source-filtered view.
			var latest time.Time
			for ip, c := range d.byClient {
				if c.lastSeen.After(latest) {
					latest, ds.LastClientIP = c.lastSeen, ip
				}
			}
		}
		snap.Domains = append(snap.Domains, ds)
	}
	// The blocked counter is global: block decisions carry no source or
	// client dimension, so the list is unaffected by the view filters.
	blocked := make([]BlockedStat, 0, len(s.blocked))
	for domain, b := range s.blocked {
		blocked = append(blocked, BlockedStat{Domain: domain, Count: b.count, LastSeen: b.lastSeen})
	}
	s.mu.Unlock()

	snap.Domains = topDomains(snap.Domains, snapshotTopN, sort)
	snap.Clients = topClients(clientAgg)
	snap.Blocked = topBlocked(blocked)
	return snap
}

// topBlocked orders blocked domains by count (ties break alphabetically) and
// caps the list at snapshotTopN.
func topBlocked(blocked []BlockedStat) []BlockedStat {
	sort.Slice(blocked, func(i, j int) bool {
		if blocked[i].Count != blocked[j].Count {
			return blocked[i].Count > blocked[j].Count
		}
		return blocked[i].Domain < blocked[j].Domain
	})
	if len(blocked) > snapshotTopN {
		blocked = blocked[:snapshotTopN]
	}
	return blocked
}

// domainLess reports whether a should appear before b in the requested order.
func domainLess(mode DomainSort) func(a, b DomainStat) bool {
	switch mode {
	case DomainSortRecent:
		return func(a, b DomainStat) bool {
			if !a.LastSeen.Equal(b.LastSeen) {
				return a.LastSeen.After(b.LastSeen)
			}
			return a.Domain < b.Domain
		}
	case DomainSortConns:
		return func(a, b DomainStat) bool {
			if a.Conns != b.Conns {
				return a.Conns > b.Conns
			}
			return a.Domain < b.Domain
		}
	default:
		return func(a, b DomainStat) bool {
			ta, tb := a.BytesUp+a.BytesDown, b.BytesUp+b.BytesDown
			if ta != tb {
				return ta > tb
			}
			return a.Domain < b.Domain
		}
	}
}

type domainTopHeap struct {
	values []DomainStat
	less   func(DomainStat, DomainStat) bool
}

func (h domainTopHeap) Len() int { return len(h.values) }

// Less puts the worst retained item at the root.
func (h domainTopHeap) Less(i, j int) bool { return h.less(h.values[j], h.values[i]) }

func (h domainTopHeap) Swap(i, j int) { h.values[i], h.values[j] = h.values[j], h.values[i] }

func (h *domainTopHeap) Push(x any) { h.values = append(h.values, x.(DomainStat)) }

func (h *domainTopHeap) Pop() any {
	old := h.values
	n := len(old)
	item := old[n-1]
	h.values = old[:n-1]
	return item
}

// topDomains returns the top limit domains in their requested display order.
func topDomains(domains []DomainStat, limit int, mode DomainSort) []DomainStat {
	if len(domains) <= limit {
		sortDomains(domains, mode)
		return domains
	}
	less := domainLess(mode)
	top := domainTopHeap{values: make([]DomainStat, 0, limit), less: less}
	for _, domain := range domains {
		if top.Len() < limit {
			heap.Push(&top, domain)
			continue
		}
		if less(domain, top.values[0]) {
			heap.Pop(&top)
			heap.Push(&top, domain)
		}
	}
	sort.Slice(top.values, func(i, j int) bool { return less(top.values[i], top.values[j]) })
	return top.values
}

type clientTopHeap []ClientStat

func (h clientTopHeap) Len() int { return len(h) }

func (h clientTopHeap) Less(i, j int) bool { return clientLess(h[j], h[i]) }

func (h clientTopHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *clientTopHeap) Push(x any) { *h = append(*h, x.(ClientStat)) }

func (h *clientTopHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}

func clientLess(a, b ClientStat) bool {
	ta, tb := a.BytesUp+a.BytesDown, b.BytesUp+b.BytesDown
	if ta == tb {
		return a.IP < b.IP
	}
	return ta > tb
}

// topClients orders per-IP aggregates by total bytes and caps the list.
func topClients(agg map[string]*ClientStat) []ClientStat {
	top := make(clientTopHeap, 0, min(clientsTopN, len(agg)))
	for _, c := range agg {
		item := *c
		if top.Len() < clientsTopN {
			heap.Push(&top, item)
			continue
		}
		if clientLess(item, top[0]) {
			heap.Pop(&top)
			heap.Push(&top, item)
		}
	}
	sort.Slice(top, func(i, j int) bool { return clientLess(top[i], top[j]) })
	return top
}

// Totals is the from-start cumulative traffic view, independent of the
// window's staleness filter.
type Totals struct {
	Domains []DomainStat `json:"domains"`
	Clients []ClientStat `json:"clients"`
}

// Totals returns the cumulative top domains and clients since start, each
// ordered by total bytes. Unlike Snapshot it ignores staleness: counters
// accumulate until the bounded maps evict their least-recently-seen entries.
func (s *Stats) Totals() Totals {
	s.mu.Lock()

	domains := make([]DomainStat, 0, len(s.domains))
	for domain, d := range s.domains {
		ds := DomainStat{
			Domain:    domain,
			Conns:     d.conns,
			BytesUp:   d.bytesUp,
			BytesDown: d.bytesDown,
			LastSeen:  d.lastSeen,
		}
		var latest time.Time
		for ip, c := range d.byClient {
			if c.lastSeen.After(latest) {
				latest, ds.LastClientIP = c.lastSeen, ip
			}
		}
		domains = append(domains, ds)
	}
	domains = topDomains(domains, snapshotTopN, DomainSortBytes)

	clients := make([]ClientStat, 0, len(s.clients))
	for ip, c := range s.clients {
		clients = append(clients, ClientStat{
			IP:        ip,
			Conns:     c.conns,
			BytesUp:   c.bytesUp,
			BytesDown: c.bytesDown,
			LastSeen:  c.lastSeen,
		})
	}
	s.mu.Unlock()

	sort.Slice(clients, func(i, j int) bool {
		ti := clients[i].BytesUp + clients[i].BytesDown
		tj := clients[j].BytesUp + clients[j].BytesDown
		if ti == tj {
			return clients[i].IP < clients[j].IP
		}
		return ti > tj
	})
	if len(clients) > clientsTopN {
		clients = clients[:clientsTopN]
	}

	return Totals{Domains: domains, Clients: clients}
}

// sortDomains orders domains by the selected mode: total bytes (default),
// most recently seen, or connection count. Ties break alphabetically.
func sortDomains(ds []DomainStat, mode DomainSort) {
	less := func(a, b DomainStat) bool { return a.Domain < b.Domain }
	switch mode {
	case DomainSortRecent:
		less = func(a, b DomainStat) bool {
			if !a.LastSeen.Equal(b.LastSeen) {
				return a.LastSeen.After(b.LastSeen)
			}
			return a.Domain < b.Domain
		}
	case DomainSortConns:
		less = func(a, b DomainStat) bool {
			if a.Conns != b.Conns {
				return a.Conns > b.Conns
			}
			return a.Domain < b.Domain
		}
	default:
		less = func(a, b DomainStat) bool {
			ta, tb := a.BytesUp+a.BytesDown, b.BytesUp+b.BytesDown
			if ta != tb {
				return ta > tb
			}
			return a.Domain < b.Domain
		}
	}
	sort.Slice(ds, func(i, j int) bool { return less(ds[i], ds[j]) })
}

// History returns a copy of the in-process history ring.
func (s *Stats) History() History {
	s.histMu.Lock()
	defer s.histMu.Unlock()
	out := make([]HistorySample, len(s.history))
	copy(out, s.history)
	return History{Samples: out}
}

// sample advances the history ring with the deltas since the last sample. It
// is called from Snapshot, so the ring advances at the poll cadence.
func (s *Stats) sample() {
	now := time.Now()
	cur := sampleCounters{
		at:        now,
		bytesUp:   s.bytesUp.Load(),
		bytesDown: s.bytesDown.Load(),
		dns:       s.dnsQueries.Load(),
		conns:     s.connsHTTP.Load() + s.connsHTTPS.Load() + s.connsSocks.Load(),
		block:     s.ruleBlock.Load(),
		direct:    s.ruleDirect.Load(),
		proxy:     s.ruleProxy.Load(),
	}

	s.histMu.Lock()
	defer s.histMu.Unlock()
	prev := s.lastSample
	if !prev.at.IsZero() && now.Sub(prev.at) < historyMinGap {
		return
	}
	s.lastSample = cur
	if prev.at.IsZero() {
		return // first sample establishes the baseline
	}

	sample := HistorySample{
		At:          now,
		Active:      s.activeHTTP.Load() + s.activeHTTPS.Load() + s.activeSocks.Load(),
		ActiveHTTP:  s.activeHTTP.Load(),
		ActiveHTTPS: s.activeHTTPS.Load(),
		ActiveSocks: s.activeSocks.Load(),
	}
	if now.Sub(prev.at) <= historyMaxGap {
		sample.BytesUp = cur.bytesUp - prev.bytesUp
		sample.BytesDown = cur.bytesDown - prev.bytesDown
		sample.DNS = cur.dns - prev.dns
		sample.Conns = cur.conns - prev.conns
		sample.Block = cur.block - prev.block
		sample.Direct = cur.direct - prev.direct
		sample.Proxy = cur.proxy - prev.proxy
	}
	s.history = append(s.history, sample)
	if len(s.history) > historyCapacity {
		s.history = s.history[len(s.history)-historyCapacity:]
	}
}

// rates computes per-second rates over the sliding rateWindow from the
// history ring.
func (s *Stats) rates() (up, down, dns, conns float64) {
	s.histMu.Lock()
	defer s.histMu.Unlock()
	now := time.Now()
	cutoff := now.Add(-rateWindow)
	var sumUp, sumDown, sumDNS, sumConns uint64
	var start time.Time
	for _, h := range s.history {
		if h.At.Before(cutoff) {
			continue
		}
		if start.IsZero() {
			start = h.At
		}
		sumUp += h.BytesUp
		sumDown += h.BytesDown
		sumDNS += h.DNS
		sumConns += h.Conns
	}
	if start.IsZero() || now.Sub(start) < time.Second {
		return 0, 0, 0, 0
	}
	elapsed := now.Sub(start).Seconds()
	return float64(sumUp) / elapsed, float64(sumDown) / elapsed, float64(sumDNS) / elapsed, float64(sumConns) / elapsed
}
