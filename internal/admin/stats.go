package admin

import (
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	maxDomainEntries = 10000
	staleDomainAge   = time.Hour
	snapshotTopN     = 100
)

// DomainStat aggregates traffic for a single domain. Conns counts both proxy
// connections and DNS queries involving the domain.
type DomainStat struct {
	Domain    string    `json:"domain"`
	Conns     uint64    `json:"conns"`
	BytesUp   uint64    `json:"bytesUp"`
	BytesDown uint64    `json:"bytesDown"`
	LastSeen  time.Time `json:"lastSeen"`
}

// TrafficSnapshot is an immutable point-in-time view of traffic stats.
type TrafficSnapshot struct {
	Uptime     time.Duration `json:"uptime"`
	DNSQueries uint64        `json:"dnsQueries"`
	Conns      struct {
		HTTP   uint64 `json:"http"`
		HTTPS  uint64 `json:"https"`
		Socks5 uint64 `json:"socks5"`
	} `json:"conns"`
	BytesUp   uint64       `json:"bytesUp"`
	BytesDown uint64       `json:"bytesDown"`
	Domains   []DomainStat `json:"domains"`
}

// Stats records proxied payload bytes and request/connection counters. It is
// not packet-level network accounting. Byte direction is relative to the
// client: reads from the client are uploads, writes to the client downloads.
type Stats struct {
	start      time.Time
	dnsQueries atomic.Uint64
	connsHTTP  atomic.Uint64
	connsHTTPS atomic.Uint64
	connsSocks atomic.Uint64
	bytesUp    atomic.Uint64
	bytesDown  atomic.Uint64

	mu      sync.Mutex
	domains map[string]*domainStat
}

type domainStat struct {
	conns     uint64
	bytesUp   uint64
	bytesDown uint64
	lastSeen  time.Time
}

func NewStats() *Stats {
	return &Stats{
		start:   time.Now(),
		domains: make(map[string]*domainStat),
	}
}

// RecordDNS counts a DNS query for the given qname.
func (s *Stats) RecordDNS(qname string) {
	s.dnsQueries.Add(1)
	domain := normalizeDomain(qname)
	if domain == "" {
		return
	}
	s.record(domain, func(d *domainStat) { d.conns++ })
}

// WrapConn wraps the client connection before protocol parsing so header and
// handshake bytes are counted. The per-kind connection counter is bumped
// immediately; per-domain attribution happens later via BindConn.
func (s *Stats) WrapConn(conn net.Conn, kind string) net.Conn {
	switch kind {
	case "http":
		s.connsHTTP.Add(1)
	case "https":
		s.connsHTTPS.Add(1)
	case "socks5":
		s.connsSocks.Add(1)
	}
	return &countingConn{Conn: conn, stats: s}
}

// BindConn attributes an already-wrapped connection to a domain and bumps the
// domain's connection counter. It is a no-op when conn was not created by
// WrapConn.
func (s *Stats) BindConn(conn net.Conn, domain string) {
	cc, ok := conn.(*countingConn)
	if !ok {
		return
	}
	domain = normalizeDomain(domain)
	if domain == "" {
		return
	}
	cc.bind(s, domain)
	s.record(domain, func(d *domainStat) { d.conns++ })
}

// Snapshot returns an immutable view of the current stats, evicting stale
// domains along the way.
func (s *Stats) Snapshot() TrafficSnapshot {
	snap := TrafficSnapshot{
		Uptime:     time.Since(s.start),
		DNSQueries: s.dnsQueries.Load(),
		BytesUp:    s.bytesUp.Load(),
		BytesDown:  s.bytesDown.Load(),
		Domains:    []DomainStat{},
	}
	snap.Conns.HTTP = s.connsHTTP.Load()
	snap.Conns.HTTPS = s.connsHTTPS.Load()
	snap.Conns.Socks5 = s.connsSocks.Load()

	s.mu.Lock()
	defer s.mu.Unlock()
	for domain, d := range s.domains {
		if time.Since(d.lastSeen) > staleDomainAge {
			delete(s.domains, domain)
			continue
		}
		snap.Domains = append(snap.Domains, DomainStat{
			Domain:    domain,
			Conns:     d.conns,
			BytesUp:   d.bytesUp,
			BytesDown: d.bytesDown,
			LastSeen:  d.lastSeen,
		})
	}
	sort.Slice(snap.Domains, func(i, j int) bool {
		ti := snap.Domains[i].BytesUp + snap.Domains[i].BytesDown
		tj := snap.Domains[j].BytesUp + snap.Domains[j].BytesDown
		if ti == tj {
			return snap.Domains[i].Domain < snap.Domains[j].Domain
		}
		return ti > tj
	})
	if len(snap.Domains) > snapshotTopN {
		snap.Domains = snap.Domains[:snapshotTopN]
	}
	return snap
}

func (s *Stats) record(domain string, update func(*domainStat)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.domains[domain]
	if d == nil {
		if len(s.domains) >= maxDomainEntries {
			s.evictOldestLocked()
		}
		d = &domainStat{}
		s.domains[domain] = d
	}
	update(d)
	d.lastSeen = time.Now()
}

// evictOldestLocked drops the single least-recently-seen entry to keep the
// domain map bounded.
func (s *Stats) evictOldestLocked() {
	var oldestKey string
	var oldest time.Time
	for domain, d := range s.domains {
		if oldestKey == "" || d.lastSeen.Before(oldest) {
			oldestKey = domain
			oldest = d.lastSeen
		}
	}
	if oldestKey != "" {
		delete(s.domains, oldestKey)
	}
}

// countingConn wraps a client connection and feeds byte counts into Stats.
type countingConn struct {
	net.Conn
	stats  *Stats
	domain atomic.Value // string; empty until BindConn
}

func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.stats.bytesUp.Add(uint64(n))
		c.recordBytes(n, true)
	}
	return n, err
}

func (c *countingConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.stats.bytesDown.Add(uint64(n))
		c.recordBytes(n, false)
	}
	return n, err
}

func (c *countingConn) bind(stats *Stats, domain string) {
	c.stats = stats
	c.domain.Store(domain)
}

func (c *countingConn) recordBytes(n int, up bool) {
	domain, _ := c.domain.Load().(string)
	if domain == "" {
		return
	}
	c.stats.record(domain, func(d *domainStat) {
		if up {
			d.bytesUp += uint64(n)
		} else {
			d.bytesDown += uint64(n)
		}
	})
}

// normalizeDomain lowercases and strips the trailing dot of a domain or qname.
func normalizeDomain(domain string) string {
	domain = strings.TrimSpace(domain)
	domain = strings.TrimSuffix(domain, ".")
	return strings.ToLower(domain)
}
