package admin

import (
	"fmt"
	"net"
	rtmetrics "runtime/metrics"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/sower-proxy/sower/internal/metrics"
)

const (
	// maxTotalDomains bounds the cumulative domain map. Domains are never
	// dropped for staleness (unlike the old window-only table); the map only
	// evicts its least-recently-seen entry once full, so from-start totals
	// survive while memory stays bounded.
	maxTotalDomains = 100000
	// maxTotalClients bounds the cumulative per-client table.
	maxTotalClients = 4096
	// maxClientsPerDomain guards one domain's per-client breakdown against a
	// pathological number of source IPs; extra clients are not attributed.
	maxClientsPerDomain = 64
	// maxBlockedDomains bounds the per-domain block counter map. Blocked
	// domains are typically few (bounded by the rule set), but a wildcard
	// rule can match an unbounded domain space, so the map evicts its
	// least-recently-seen entry once full.
	maxBlockedDomains   = 10000
	staleDomainAge      = time.Hour
	snapshotTopN        = 100
	clientsTopN         = 50
	domainEvictionBatch = 1024
	clientEvictionBatch = 64

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

	// maxErrorEvents bounds the in-memory error event ring exposed to the
	// console; the ring drops its oldest entry once full.
	maxErrorEvents = 64
	// maxErrorDetailLen truncates the per-event detail so a malformed host
	// cannot bloat the ring or the SSE payload.
	maxErrorDetailLen = 120
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
	// Errors aggregates from-start proxy-side failures by kind. Blocked
	// connections are deliberate routing decisions and are never counted
	// here; only dial, DNS, and accept failures are.
	Errors struct {
		Dial   uint64 `json:"dial"`
		DNS    uint64 `json:"dns"`
		Accept uint64 `json:"accept"`
	} `json:"errors"`
	// Events is the recent error event ring, newest first, capped at
	// maxErrorEvents entries. It carries the same kinds as Errors.
	Events []ErrorEvent `json:"events"`
	// Blocked is the from-start per-domain block counter, ordered by count.
	// It is independent of the domain map: blocked connections never carry
	// traffic, so they would otherwise be invisible to the traffic view.
	Blocked []BlockedStat `json:"blocked"`
	System  struct {
		Goroutines uint64 `json:"goroutines"`
		HeapAlloc  uint64 `json:"heapAlloc"`
	} `json:"system"`
	BytesUp   uint64       `json:"bytesUp"`
	BytesDown uint64       `json:"bytesDown"`
	Domains   []DomainStat `json:"domains"`
	Clients   []ClientStat `json:"clients"`
}

// BlockedStat aggregates block decisions for a single domain.
type BlockedStat struct {
	Domain   string    `json:"domain"`
	Count    uint64    `json:"count"`
	LastSeen time.Time `json:"lastSeen"`
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

// ErrorEvent is one recorded proxy-side failure, exposed to the console so
// service-level problems (unreachable upstream, DNS breakdown, accept
// pressure) are visible without grepping the system log.
type ErrorEvent struct {
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"`
	Detail string    `json:"detail"`
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
	dialFailed  atomic.Uint64
	dnsFailed   atomic.Uint64
	acceptFailed atomic.Uint64

	errMu     sync.Mutex
	errEvents []ErrorEvent

	mu      sync.Mutex
	domains map[string]*domainStat
	// clients holds the from-start cumulative per-client totals. It is
	// independent of the domain map so client totals survive domain eviction.
	clients map[string]*clientStat
	// blocked holds the from-start per-domain block counts, fed by the router
	// observer. It is independent of the domain map: blocked connections
	// never carry traffic, so they would otherwise be invisible.
	blocked map[string]*blockedStat

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

// blockedStat is the per-domain block counter.
type blockedStat struct {
	count    uint64
	lastSeen time.Time
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
		clients: make(map[string]*clientStat),
		blocked: make(map[string]*blockedStat),
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
	s.record(domain, SourceDNS, clientIP, 1, 0, 0, func(d *domainStat, src *sourceStat, dc *clientStat) {
		d.conns++
		src.conns++
		if dc != nil {
			dc.conns++
		}
	})
}

// recentErrorEvents returns the error event ring, newest first. The caller
// must not mutate the returned slice.
func (s *Stats) recentErrorEvents() []ErrorEvent {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	out := make([]ErrorEvent, len(s.errEvents))
	for i, ev := range s.errEvents {
		out[len(s.errEvents)-1-i] = ev
	}
	return out
}

// RecordProxyError counts one proxy-side failure of the given kind (dial,
// dns, or accept) and appends it to the console event ring. Blocked
// connections must not call this: a block is a routing decision, not a
// failure. detail is a short human-readable context (host, DNS server); it
// is truncated and must never carry query strings or credentials.
func (s *Stats) RecordProxyError(kind string, detail string) {
	switch kind {
	case "dial":
		s.dialFailed.Add(1)
	case "dns":
		s.dnsFailed.Add(1)
	case "accept":
		s.acceptFailed.Add(1)
	default:
		return
	}
	s.metrics.RecordProxyError(kind)

	ev := ErrorEvent{At: time.Now(), Kind: kind, Detail: cleanErrorDetail(detail)}
	s.errMu.Lock()
	s.errEvents = append(s.errEvents, ev)
	if len(s.errEvents) > maxErrorEvents {
		s.errEvents = s.errEvents[len(s.errEvents)-maxErrorEvents:]
	}
	s.errMu.Unlock()
}

// cleanErrorDetail bounds an error detail to the byte budget without
// splitting a UTF-8 rune (a host can be an IDN in Chinese), and collapses
// line breaks so a multiline error cannot break the console alert layout.
func cleanErrorDetail(detail string) string {
	detail = strings.ReplaceAll(detail, "\n", " ")
	if len(detail) > maxErrorDetailLen {
		detail = detail[:maxErrorDetailLen]
		for !utf8.ValidString(detail) {
			detail = detail[:len(detail)-1]
		}
	}
	return detail
}

// RecordRoute counts one connection routing decision. It is called exactly
// once per connection by the router observer. Block decisions additionally
// attribute the domain to the per-domain block counter; the domain may be
// empty for non-block routes or when it cannot be determined.
func (s *Stats) RecordRoute(route, domain string) {
	switch route {
	case "block":
		s.ruleBlock.Add(1)
		if domain != "" {
			s.recordBlock(domain)
		}
	case "direct":
		s.ruleDirect.Add(1)
	case "proxy":
		s.ruleProxy.Add(1)
	}
	s.metrics.RecordRoute(route)
}

// recordBlock attributes one block decision to a domain, evicting a batch
// of least-recently-seen entries when the map is full.
func (s *Stats) recordBlock(domain string) {
	domain = normalizeDomain(domain)
	if domain == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.blocked[domain]
	if b == nil {
		if len(s.blocked) >= maxBlockedDomains {
			s.evictOldestBlockedLocked(blockedEvictionBatch)
		}
		b = &blockedStat{}
		s.blocked[domain] = b
	}
	b.count++
	b.lastSeen = time.Now()
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
	s.record(domain, source, cc.clientIP, 1, 0, 0, func(d *domainStat, src *sourceStat, dc *clientStat) {
		d.conns++
		src.conns++
		if dc != nil {
			dc.conns++
		}
	})
}

// record attributes one event to a domain, its source, and optionally its
// client IP, with the given counter deltas. The client IP may be empty when it
// cannot be determined; the per-client stat is then nil and callers must
// guard. The deltas also feed the independent cumulative client table, so
// client totals are exact regardless of domain eviction.
func (s *Stats) record(domain string, source Source, clientIP string, conns, up, down uint64, update func(*domainStat, *sourceStat, *clientStat)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.domains[domain]
	if d == nil {
		if len(s.domains) >= maxTotalDomains {
			s.evictOldestDomainsLocked(domainEvictionBatch)
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
		if dc == nil && len(d.byClient) < maxClientsPerDomain {
			dc = &clientStat{}
			d.byClient[clientIP] = dc
		}
	}
	update(d, src, dc)
	now := time.Now()
	d.lastSeen = now
	src.lastSeen = now
	if dc != nil {
		dc.lastSeen = now
	}

	if clientIP != "" {
		cc := s.clients[clientIP]
		if cc == nil {
			if len(s.clients) >= maxTotalClients {
				s.evictOldestClientsLocked(clientEvictionBatch)
			}
			cc = &clientStat{}
			s.clients[clientIP] = cc
		}
		cc.conns += conns
		cc.bytesUp += up
		cc.bytesDown += down
		cc.lastSeen = now
	}
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
