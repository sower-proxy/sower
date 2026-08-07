package admin

import (
	"fmt"
	"net"
	rtmetrics "runtime/metrics"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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
	// clients holds the from-start cumulative per-client totals. It is
	// independent of the domain map so client totals survive domain eviction.
	clients map[string]*clientStat

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
		clients: make(map[string]*clientStat),
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
