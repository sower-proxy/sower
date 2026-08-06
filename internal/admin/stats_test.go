package admin

import (
	"fmt"
	"net"
	"testing"
	"time"
)

type fakeConn struct {
	net.Conn
	readBuf  []byte
	writeBuf []byte
}

func (c *fakeConn) Read(p []byte) (int, error) {
	n := copy(p, c.readBuf)
	c.readBuf = c.readBuf[n:]
	return n, nil
}

func (c *fakeConn) Write(p []byte) (int, error) {
	c.writeBuf = append(c.writeBuf, p...)
	return len(p), nil
}

func (c *fakeConn) Close() error { return nil }

func TestStatsWrapConnCountsBytesBeforeBind(t *testing.T) {
	s := newTestStats(t)
	conn := &fakeConn{readBuf: []byte("GET / HTTP/1.1\r\nHost: example.com\r\n\r\n")}
	wrapped := s.WrapConn(conn, "http")

	// protocol parsing reads header bytes before the domain is known
	header := make([]byte, 5)
	if _, err := wrapped.Read(header); err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := wrapped.Write([]byte("HTTP/1.1 200 OK\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	snap := s.Snapshot()
	if snap.Conns.HTTP != 1 {
		t.Fatalf("expected 1 http conn, got %d", snap.Conns.HTTP)
	}
	if snap.BytesUp != 5 {
		t.Fatalf("expected 5 bytes up, got %d", snap.BytesUp)
	}
	if snap.BytesDown != 17 {
		t.Fatalf("expected 17 bytes down, got %d", snap.BytesDown)
	}
	if len(snap.Domains) != 0 {
		t.Fatalf("expected no domain before bind, got %v", snap.Domains)
	}
}

func TestStatsBindConnAttributesBytesToDomain(t *testing.T) {
	s := newTestStats(t)
	conn := &fakeConn{readBuf: []byte("request body")}
	wrapped := s.WrapConn(conn, "https")

	pre := make([]byte, 4) // header bytes read before the domain is known
	if _, err := wrapped.Read(pre); err != nil {
		t.Fatalf("read: %v", err)
	}
	s.BindConn(wrapped, "example.com.")
	payload := make([]byte, 8)
	if _, err := wrapped.Read(payload); err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, err := wrapped.Write([]byte("response")); err != nil {
		t.Fatalf("write: %v", err)
	}

	snap := s.Snapshot()
	if len(snap.Domains) != 1 {
		t.Fatalf("expected 1 domain, got %v", snap.Domains)
	}
	d := snap.Domains[0]
	if d.Domain != "example.com" {
		t.Fatalf("unexpected domain %q", d.Domain)
	}
	if d.Conns != 1 {
		t.Fatalf("expected 1 domain conn, got %d", d.Conns)
	}
	if d.BytesUp != 8 {
		t.Fatalf("expected 8 domain bytes up, got %d", d.BytesUp)
	}
	if d.BytesDown != 8 {
		t.Fatalf("expected 8 domain bytes down, got %d", d.BytesDown)
	}
	if snap.BytesUp != 12 {
		t.Fatalf("expected 12 aggregate bytes up, got %d", snap.BytesUp)
	}
}

func TestStatsBindConnIgnoresUnwrappedConn(t *testing.T) {
	s := newTestStats(t)
	s.BindConn(&fakeConn{}, "example.com")
	if len(s.Snapshot().Domains) != 0 {
		t.Fatal("expected unwrapped conn to be ignored")
	}
}

func TestStatsRecordDNS(t *testing.T) {
	s := newTestStats(t)
	s.RecordDNS("Example.COM.")
	s.RecordDNS("example.com.")

	snap := s.Snapshot()
	if snap.DNSQueries != 2 {
		t.Fatalf("expected 2 dns queries, got %d", snap.DNSQueries)
	}
	if len(snap.Domains) != 1 {
		t.Fatalf("expected dns domains to be deduplicated, got %v", snap.Domains)
	}
	if snap.Domains[0].Conns != 2 {
		t.Fatalf("expected 2 dns conns, got %d", snap.Domains[0].Conns)
	}
}

func TestStatsSnapshotEvictsStaleDomains(t *testing.T) {
	s := newTestStats(t)
	s.mu.Lock()
	s.domains["stale.example"] = &domainStat{lastSeen: time.Now().Add(-2 * staleDomainAge)}
	s.domains["fresh.example"] = &domainStat{lastSeen: time.Now()}
	s.mu.Unlock()

	snap := s.Snapshot()
	if len(snap.Domains) != 1 || snap.Domains[0].Domain != "fresh.example" {
		t.Fatalf("expected stale domain evicted, got %v", snap.Domains)
	}
}

func TestStatsDomainCapIsEnforced(t *testing.T) {
	s := newTestStats(t)
	for i := 0; i < maxDomainEntries+50; i++ {
		s.record(fmt.Sprintf("host%d.example", i), func(d *domainStat) {})
	}
	s.mu.Lock()
	n := len(s.domains)
	s.mu.Unlock()
	if n > maxDomainEntries {
		t.Fatalf("expected domain cap enforced, got %d entries", n)
	}
	if n == 0 {
		t.Fatal("expected some domains to survive")
	}
}

func TestEvictOldestLocked(t *testing.T) {
	s := newTestStats(t)
	s.mu.Lock()
	s.domains["old.example"] = &domainStat{lastSeen: time.Now().Add(-time.Hour)}
	s.domains["new.example"] = &domainStat{lastSeen: time.Now()}
	s.evictOldestLocked()
	_, oldOK := s.domains["old.example"]
	_, newOK := s.domains["new.example"]
	s.mu.Unlock()
	if oldOK || !newOK {
		t.Fatalf("expected oldest evicted, old=%v new=%v", oldOK, newOK)
	}
}

func TestStatsSnapshotSortsByTotalBytes(t *testing.T) {
	s := newTestStats(t)
	s.record("small.example", func(d *domainStat) { d.bytesUp, d.bytesDown = 1, 1 })
	s.record("big.example", func(d *domainStat) { d.bytesUp, d.bytesDown = 100, 100 })
	s.record("mid.example", func(d *domainStat) { d.bytesUp, d.bytesDown = 10, 10 })

	snap := s.Snapshot()
	if len(snap.Domains) != 3 {
		t.Fatalf("unexpected domains: %v", snap.Domains)
	}
	if snap.Domains[0].Domain != "big.example" || snap.Domains[2].Domain != "small.example" {
		t.Fatalf("unexpected sort order: %v", snap.Domains)
	}
}

func newTestStats(t *testing.T) *Stats {
	t.Helper()
	s, err := NewStats()
	if err != nil {
		t.Fatalf("new stats: %v", err)
	}
	return s
}

func TestStatsUptimeIsSeconds(t *testing.T) {
	s := newTestStats(t)
	snap := s.Snapshot()
	// A fresh stats object must report a small number of seconds, not
	// nanoseconds (a time.Duration would serialize as ~1e9 per second).
	if snap.Uptime < 0 || snap.Uptime > 60 {
		t.Fatalf("expected uptime in seconds, got %d", snap.Uptime)
	}
}

func TestStatsActiveConnsTrackedAndIdempotentClose(t *testing.T) {
	s := newTestStats(t)
	wrapped := s.WrapConn(&fakeConn{}, "http")

	snap := s.Snapshot()
	if snap.Active.HTTP != 1 || snap.Conns.HTTP != 1 {
		t.Fatalf("expected 1 active/1 total http conn, got %+v / %+v", snap.Active, snap.Conns)
	}

	if err := wrapped.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := wrapped.Close(); err != nil { // idempotent
		t.Fatalf("second close: %v", err)
	}
	snap = s.Snapshot()
	if snap.Active.HTTP != 0 {
		t.Fatalf("expected 0 active after close, got %d", snap.Active.HTTP)
	}
	if snap.Conns.HTTP != 1 {
		t.Fatalf("expected cumulative count to stay 1, got %d", snap.Conns.HTTP)
	}
}

func TestStatsActiveConnsPerProtocol(t *testing.T) {
	s := newTestStats(t)
	http := s.WrapConn(&fakeConn{}, "http")
	https := s.WrapConn(&fakeConn{}, "https")
	socks := s.WrapConn(&fakeConn{}, "socks5")

	snap := s.Snapshot()
	if snap.Active.HTTP != 1 || snap.Active.HTTPS != 1 || snap.Active.Socks5 != 1 {
		t.Fatalf("unexpected active conns: %+v", snap.Active)
	}
	_ = http.Close()
	_ = https.Close()
	_ = socks.Close()
	snap = s.Snapshot()
	if snap.Active.HTTP != 0 || snap.Active.HTTPS != 0 || snap.Active.Socks5 != 0 {
		t.Fatalf("expected all active conns released: %+v", snap.Active)
	}
}

func TestStatsRecordRoute(t *testing.T) {
	s := newTestStats(t)
	s.RecordRoute("block")
	s.RecordRoute("direct")
	s.RecordRoute("proxy")
	s.RecordRoute("proxy")

	snap := s.Snapshot()
	if snap.RuleHits.Block != 1 || snap.RuleHits.Direct != 1 || snap.RuleHits.Proxy != 2 {
		t.Fatalf("unexpected rule hits: %+v", snap.RuleHits)
	}
}

func TestStatsRatesFromHistory(t *testing.T) {
	s := newTestStats(t)
	now := time.Now()
	s.histMu.Lock()
	s.lastSample = sampleCounters{at: now.Add(-10 * time.Second)}
	s.history = []HistorySample{
		{At: now.Add(-10 * time.Second), BytesUp: 1000, BytesDown: 2000, DNS: 10, Conns: 5},
		{At: now.Add(-5 * time.Second), BytesUp: 2000, BytesDown: 4000, DNS: 20, Conns: 10},
	}
	s.histMu.Unlock()

	up, down, dns, conns := s.rates()
	if up < 290 || up > 310 {
		t.Fatalf("expected ~300 bytes/s up, got %v", up)
	}
	if down < 590 || down > 610 {
		t.Fatalf("expected ~600 bytes/s down, got %v", down)
	}
	if dns < 2.9 || dns > 3.1 {
		t.Fatalf("expected ~3 dns/s, got %v", dns)
	}
	if conns < 1.4 || conns > 1.6 {
		t.Fatalf("expected ~1.5 conns/s, got %v", conns)
	}
}

func TestStatsHistoryRingBounded(t *testing.T) {
	s := newTestStats(t)
	s.histMu.Lock()
	s.lastSample = sampleCounters{at: time.Now().Add(-2 * time.Second)}
	s.history = make([]HistorySample, historyCapacity)
	for i := range s.history {
		s.history[i] = HistorySample{At: time.Now().Add(-time.Duration(historyCapacity-i) * time.Second)}
	}
	s.histMu.Unlock()

	s.sample() // gap 2s > minGap: appends and trims to capacity
	if got := len(s.History().Samples); got != historyCapacity {
		t.Fatalf("expected ring capped at %d, got %d", historyCapacity, got)
	}
}

func TestStatsSampleClampsLongGap(t *testing.T) {
	s := newTestStats(t)
	s.RecordDNS("example.com")
	s.RecordRoute("proxy")
	s.histMu.Lock()
	s.lastSample = sampleCounters{at: time.Now().Add(-10 * time.Minute)}
	s.histMu.Unlock()

	s.sample()
	h := s.History().Samples
	if len(h) != 1 {
		t.Fatalf("expected 1 sample, got %d", len(h))
	}
	if h[0].DNS != 0 || h[0].Proxy != 0 {
		t.Fatalf("expected clamped zero deltas, got %+v", h[0])
	}
}
