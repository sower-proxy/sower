package admin

import (
	"fmt"
	"net"
	rtmetrics "runtime/metrics"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sower-proxy/sower/internal/metrics"
)

const (
	maxDomainEntries = 10000
	staleDomainAge   = time.Hour
	snapshotTopN     = 100
	clientsTopN      = 50

	// historyCapacity keeps one hour of samples at the 5s poll cadence.
	historyCapacity = 720
	// historyMinGap is the minimum time between two history samples; rapid
	// duplicate polls do not advance the ring.
	historyMinGap = time.Second
	// historyMaxGap clamps the deltas of a sample taken after a long absence
	// (e.g. the console was closed) to zero, so charts do not show a spike
	// that aggregates an entire gap into one interval.
	historyMaxGap = 30 * time.Second
	// rateWindow is the sliding window used for per-second rates.
	rateWindow = 60 * time.Second
)

// DomainSort selects the ordering of the domain stats in a snapshot.
type DomainSort string

const (
	DomainSortBytes  DomainSort = "bytes"  // total traffic, high to low
	DomainSortRecent DomainSort = "recent" // most recently seen first
	DomainSortConns  DomainSort = "conns"  // most connections first
)

func (s DomainSort) valid() bool {
	switch s {
	case DomainSortBytes, DomainSortRecent, DomainSortConns:
		return true
	default:
		return false
	}
}

// Source identifies the entry point a connection or DNS query came through.
type Source string

const (
	SourceAll    Source = "all"
	SourceHTTP   Source = "http"
	SourceHTTPS  Source = "https"
	SourceSocks5 Source = "socks5"
	SourceDNS    Source = "dns"
)

func (s Source) valid() bool {
	switch s {
	case SourceAll, SourceHTTP, SourceHTTPS, SourceSocks5, SourceDNS:
		return true
	default:
		return false
	}
}

// DomainStat aggregates traffic for a single domain. Conns counts both proxy
// connections and DNS queries involving the domain. LastClientIP is the most
// recent client IP for the displayed view: the filtered client itself when a
// client filter is active, otherwise the latest client across all sources.
type DomainStat struct {
	Domain       string    `json:"domain"`
	Conns        uint64    `json:"conns"`
	BytesUp      uint64    `json:"bytesUp"`
	BytesDown    uint64    `json:"bytesDown"`
	LastSeen     time.Time `json:"lastSeen"`
	LastClientIP string    `json:"lastClientIP"`
}

// TrafficSnapshot is an immutable point-in-time view of traffic stats.
type TrafficSnapshot struct {
	// Uptime is seconds since the process started. It is an int64 on purpose:
	// a time.Duration would serialize as nanoseconds and the frontend renders
	// seconds.
	Uptime     int64  `json:"uptime"`
	DNSQueries uint64 `json:"dnsQueries"`
	Conns      struct {
		HTTP   uint64 `json:"http"`
		HTTPS  uint64 `json:"https"`
		Socks5 uint64 `json:"socks5"`
	} `json:"conns"`
	Active struct {
		HTTP   uint64 `json:"http"`
		HTTPS  uint64 `json:"https"`
		Socks5 uint64 `json:"socks5"`
	} `json:"active"`
	Rates struct {
		BytesUpPerSec   float64 `json:"bytesUpPerSec"`
		BytesDownPerSec float64 `json:"bytesDownPerSec"`
		DNSPerSec       float64 `json:"dnsPerSec"`
		ConnsPerSec     float64 `json:"connsPerSec"`
	} `json:"rates"`
	RuleHits struct {
		Block  uint64 `json:"block"`
		Direct uint64 `json:"direct"`
		Proxy  uint64 `json:"proxy"`
	} `json:"ruleHits"`
	System struct {
		Goroutines uint64 `json:"goroutines"`
		HeapAlloc  uint64 `json:"heapAlloc"`
	} `json:"system"`
	BytesUp   uint64       `json:"bytesUp"`
	BytesDown uint64       `json:"bytesDown"`
	Domains   []DomainStat `json:"domains"`
	Clients   []ClientStat `json:"clients"`
}

// ClientStat aggregates traffic for a single client IP. Conns counts both
// proxy connections and DNS queries originating from the IP.
type ClientStat struct {
	IP        string    `json:"ip"`
	Conns     uint64    `json:"conns"`
	BytesUp   uint64    `json:"bytesUp"`
	BytesDown uint64    `json:"bytesDown"`
	LastSeen  time.Time `json:"lastSeen"`
}

// HistorySample is one point in the in-process history ring. Byte, query,
// connection and rule-hit fields are deltas since the previous sample; the
// Active fields are instantaneous connection counts. History lives in process
// memory and is lost on restart.
type HistorySample struct {
	At          time.Time `json:"at"`
	BytesUp     uint64    `json:"bytesUp"`
	BytesDown   uint64    `json:"bytesDown"`
	DNS         uint64    `json:"dns"`
	Conns       uint64    `json:"conns"`
	Active      uint64    `json:"active"`
	ActiveHTTP  uint64    `json:"activeHttp"`
	ActiveHTTPS uint64    `json:"activeHttps"`
	ActiveSocks uint64    `json:"activeSocks"`
	Block       uint64    `json:"block"`
	Direct      uint64    `json:"direct"`
	Proxy       uint64    `json:"proxy"`
}

// History is the bounded in-process time series served by /api/history.
type History struct {
	Samples []HistorySample `json:"samples"`
}

// Stats records proxied payload bytes and request/connection counters. It is
// not packet-level network accounting. Byte direction is relative to the
// client: reads from the client are uploads, writes to the client downloads.
// Aggregate counters are mirrored to OpenTelemetry instruments
// (internal/metrics) for the standardized /metrics export; the domain map
// stays here because per-domain attribution is high-cardinality and not
// OTel-appropriate.
type Stats struct {
	start   time.Time
	metrics *metrics.Metrics

	dnsQueries  atomic.Uint64
	connsHTTP   atomic.Uint64
	connsHTTPS  atomic.Uint64
	connsSocks  atomic.Uint64
	activeHTTP  atomic.Uint64
	activeHTTPS atomic.Uint64
	activeSocks atomic.Uint64
	bytesUp     atomic.Uint64
	bytesDown   atomic.Uint64
	ruleBlock   atomic.Uint64
	ruleDirect  atomic.Uint64
	ruleProxy   atomic.Uint64

	mu      sync.Mutex
	domains map[string]*domainStat

	histMu     sync.Mutex
	history    []HistorySample
	lastSample sampleCounters
}

// sampleCounters is the cumulative counter state at one sampling point.
type sampleCounters struct {
	at        time.Time
	bytesUp   uint64
	bytesDown uint64
	dns       uint64
	conns     uint64
	block     uint64
	direct    uint64
	proxy     uint64
}

type domainStat struct {
	conns     uint64
	bytesUp   uint64
	bytesDown uint64
	lastSeen  time.Time
	bySource  map[Source]*sourceStat
	byClient  map[string]*clientStat // client IP -> per-domain stats
}

// sourceStat is the per-source breakdown of a domain's traffic. A domain can
// be reached through several entry points (HTTP/HTTPS/SOCKS5 proxies and DNS
// queries), so each is tracked separately for the source-filtered views.
type sourceStat struct {
	conns     uint64
	bytesUp   uint64
	bytesDown uint64
	lastSeen  time.Time
}

// clientStat is the per-client-IP breakdown of a domain's traffic.
type clientStat struct {
	conns     uint64
	bytesUp   uint64
	bytesDown uint64
	lastSeen  time.Time
}

// NewStats creates the stats registry and its OpenTelemetry instruments.
func NewStats() (*Stats, error) {
	m, err := metrics.New()
	if err != nil {
		return nil, fmt.Errorf("init metrics: %w", err)
	}
	return &Stats{
		start:   time.Now(),
		metrics: m,
		domains: make(map[string]*domainStat),
	}, nil
}

// Metrics exposes the OpenTelemetry export surface (Prometheus /metrics).
func (s *Stats) Metrics() *metrics.Metrics { return s.metrics }

// RecordDNS counts a DNS query for the given qname from the given client IP.
func (s *Stats) RecordDNS(qname string, clientIP string) {
	s.dnsQueries.Add(1)
	s.metrics.RecordDNS()
	domain := normalizeDomain(qname)
	if domain == "" {
		return
	}
	s.record(domain, SourceDNS, clientIP, func(d *domainStat, src *sourceStat, dc *clientStat) {
		d.conns++
		src.conns++
		if dc != nil {
			dc.conns++
		}
	})
}

// RecordRoute counts one connection routing decision. It is called exactly
// once per connection by the router observer.
func (s *Stats) RecordRoute(route string) {
	switch route {
	case "block":
		s.ruleBlock.Add(1)
	case "direct":
		s.ruleDirect.Add(1)
	case "proxy":
		s.ruleProxy.Add(1)
	}
	s.metrics.RecordRoute(route)
}

// WrapConn wraps the client connection before protocol parsing so header and
// handshake bytes are counted. The per-kind connection counter is bumped
// immediately; per-domain attribution happens later via BindConn.
func (s *Stats) WrapConn(conn net.Conn, kind string) net.Conn {
	switch kind {
	case "http":
		s.connsHTTP.Add(1)
		s.activeHTTP.Add(1)
	case "https":
		s.connsHTTPS.Add(1)
		s.activeHTTPS.Add(1)
	case "socks5":
		s.connsSocks.Add(1)
		s.activeSocks.Add(1)
	}
	s.metrics.ConnOpened(kind)
	return &countingConn{
		Conn:     conn,
		stats:    s,
		kind:     kind,
		clientIP: ClientIPOf(conn.RemoteAddr()),
		created:  time.Now(),
	}
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
	cc.bind(domain)
	source := Source(cc.kind)
	s.record(domain, source, cc.clientIP, func(d *domainStat, src *sourceStat, dc *clientStat) {
		d.conns++
		src.conns++
		if dc != nil {
			dc.conns++
		}
	})
}

// Snapshot returns an immutable view of the current stats, advancing the
// history ring and evicting stale domains along the way. sort selects the
// domain ordering, source restricts domains to one entry point, and client
// restricts them to one client IP; invalid values fall back to
// DomainSortBytes and SourceAll, and an empty client disables the filter.
// Clients aggregates per-client counters over the source-filtered view and
// is intentionally unaffected by the client filter, so the console's client
// chips stay stable while one is selected.
func (s *Stats) Snapshot(sort DomainSort, source Source, client string) TrafficSnapshot {
	s.sample()

	snap := TrafficSnapshot{
		Uptime:     int64(time.Since(s.start).Seconds()),
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
	snap.Rates.BytesUpPerSec, snap.Rates.BytesDownPerSec, snap.Rates.DNSPerSec, snap.Rates.ConnsPerSec = s.rates()
	snap.System.Goroutines = uint64(goroutineCount())
	snap.System.HeapAlloc = heapAlloc()

	s.mu.Lock()
	defer s.mu.Unlock()
	clientAgg := make(map[string]*ClientStat)
	for domain, d := range s.domains {
		if time.Since(d.lastSeen) > staleDomainAge {
			delete(s.domains, domain)
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
	sortDomains(snap.Domains, sort)
	if len(snap.Domains) > snapshotTopN {
		snap.Domains = snap.Domains[:snapshotTopN]
	}
	snap.Clients = topClients(clientAgg)
	return snap
}

// topClients orders per-IP aggregates by total bytes and caps the list.
func topClients(agg map[string]*ClientStat) []ClientStat {
	out := make([]ClientStat, 0, len(agg))
	for _, c := range agg {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		ti := out[i].BytesUp + out[i].BytesDown
		tj := out[j].BytesUp + out[j].BytesDown
		if ti == tj {
			return out[i].IP < out[j].IP
		}
		return ti > tj
	})
	if len(out) > clientsTopN {
		out = out[:clientsTopN]
	}
	return out
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

// record attributes one event to a domain, its source, and optionally its
// client IP. The client IP may be empty when it cannot be determined; the
// per-client stat is then nil and callers must guard.
func (s *Stats) record(domain string, source Source, clientIP string, update func(*domainStat, *sourceStat, *clientStat)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.domains[domain]
	if d == nil {
		if len(s.domains) >= maxDomainEntries {
			s.evictOldestLocked()
		}
		d = &domainStat{bySource: make(map[Source]*sourceStat)}
		s.domains[domain] = d
	}
	src := d.bySource[source]
	if src == nil {
		src = &sourceStat{}
		d.bySource[source] = src
	}
	var dc *clientStat
	if clientIP != "" {
		if d.byClient == nil {
			d.byClient = make(map[string]*clientStat)
		}
		dc = d.byClient[clientIP]
		if dc == nil {
			dc = &clientStat{}
			d.byClient[clientIP] = dc
		}
	}
	update(d, src, dc)
	d.lastSeen = time.Now()
	src.lastSeen = time.Now()
	if dc != nil {
		dc.lastSeen = time.Now()
	}
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
	stats    *Stats
	kind     string
	clientIP string
	created  time.Time
	domain   atomic.Value // string; empty until BindConn
	closed   atomic.Bool
}

func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.stats.bytesUp.Add(uint64(n))
		c.stats.metrics.RecordBytes(true, n)
		c.recordBytes(n, true)
	}
	return n, err
}

func (c *countingConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.stats.bytesDown.Add(uint64(n))
		c.stats.metrics.RecordBytes(false, n)
		c.recordBytes(n, false)
	}
	return n, err
}

// Close releases the active-connection slot exactly once and records the
// connection duration. The underlying conn is closed on every call, matching
// net.Conn semantics.
func (c *countingConn) Close() error {
	if c.closed.CompareAndSwap(false, true) {
		switch c.kind {
		case "http":
			c.stats.activeHTTP.Add(^uint64(0))
		case "https":
			c.stats.activeHTTPS.Add(^uint64(0))
		case "socks5":
			c.stats.activeSocks.Add(^uint64(0))
		}
		c.stats.metrics.ConnClosed(c.kind)
		c.stats.metrics.RecordConnDuration(time.Since(c.created))
	}
	return c.Conn.Close()
}

func (c *countingConn) bind(domain string) {
	c.domain.Store(domain)
}

func (c *countingConn) recordBytes(n int, up bool) {
	domain, _ := c.domain.Load().(string)
	if domain == "" {
		return
	}
	source := Source(c.kind)
	c.stats.record(domain, source, c.clientIP, func(d *domainStat, src *sourceStat, dc *clientStat) {
		if up {
			d.bytesUp += uint64(n)
			src.bytesUp += uint64(n)
			if dc != nil {
				dc.bytesUp += uint64(n)
			}
		} else {
			d.bytesDown += uint64(n)
			src.bytesDown += uint64(n)
			if dc != nil {
				dc.bytesDown += uint64(n)
			}
		}
	})
}

// normalizeDomain lowercases and strips the trailing dot of a domain or qname.
func normalizeDomain(domain string) string {
	domain = strings.TrimSpace(domain)
	domain = strings.TrimSuffix(domain, ".")
	return strings.ToLower(domain)
}

// clientIPOf extracts the host part of a network address, falling back to the
// full address when it has no port.
func ClientIPOf(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	if host, _, err := net.SplitHostPort(addr.String()); err == nil {
		return host
	}
	return addr.String()
}

// goroutineCount reports the current number of goroutines.
func goroutineCount() uint64 {
	var samples = []rtmetrics.Sample{{Name: "/sched/goroutines:goroutines"}}
	rtmetrics.Read(samples)
	return samples[0].Value.Uint64()
}

// heapAlloc reads the live heap size without stopping the world.
func heapAlloc() uint64 {
	var samples = []rtmetrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	rtmetrics.Read(samples)
	return samples[0].Value.Uint64()
}
