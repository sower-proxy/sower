package admin

import (
	"bytes"
	"fmt"
	"net"
	"slices"
	"sort"
	"sync"
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

func (c *fakeConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("192.0.2.1")}
}

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

	snap := s.Snapshot(DomainSortBytes, SourceAll, "")
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

	// Byte attribution is batched per connection: the pending batches are
	// drained on Close (or when a threshold/interval is crossed), so close
	// before asserting the per-domain totals.
	if err := wrapped.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	snap := s.Snapshot(DomainSortBytes, SourceAll, "")
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
	if len(s.Snapshot(DomainSortBytes, SourceAll, "").Domains) != 0 {
		t.Fatal("expected unwrapped conn to be ignored")
	}
}

// TestCountingConnBatchWindow: bytes within the flush window stay pending;
// Close drains them so attribution is exact in the end.
func TestCountingConnBatchWindow(t *testing.T) {
	s := newTestStats(t)
	wrapped := s.WrapConn(&fakeConn{readBuf: bytes.Repeat([]byte("x"), 64)}, "http")
	s.BindConn(wrapped, "example.com")

	buf := make([]byte, 1)
	// First I/O flushes immediately (nextFlush starts zero): attributed now.
	if _, err := wrapped.Read(buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	// Second I/O falls inside the flush window: kept pending.
	if _, err := wrapped.Read(buf); err != nil {
		t.Fatalf("read: %v", err)
	}

	snap := s.Snapshot(DomainSortBytes, SourceAll, "")
	if len(snap.Domains) != 1 || snap.Domains[0].BytesUp != 1 {
		t.Fatalf("expected 1 pending byte unflushed, got %+v", snap.Domains)
	}

	if err := wrapped.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	snap = s.Snapshot(DomainSortBytes, SourceAll, "")
	if len(snap.Domains) != 1 || snap.Domains[0].BytesUp != 2 {
		t.Fatalf("expected both bytes after close, got %+v", snap.Domains)
	}
}

// TestCountingConnBatchThreshold: sustained traffic flushes at the byte
// threshold without waiting for Close.
func TestCountingConnBatchThreshold(t *testing.T) {
	s := newTestStats(t)
	wrapped := s.WrapConn(&fakeConn{readBuf: bytes.Repeat([]byte("x"), 9*flushBytes)}, "https")
	s.BindConn(wrapped, "example.com")

	chunk := make([]byte, 4096)
	total := 0
	for total < 9*4096 {
		n, err := wrapped.Read(chunk)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		total += n
	}
	if total != 9*4096 {
		t.Fatalf("read %d bytes, want %d", total, 9*4096)
	}

	// 9×4KiB crosses the 32KiB threshold (first chunk flushes immediately,
	// the next eight accumulate to exactly the threshold), so the domain
	// total is visible without Close.
	snap := s.Snapshot(DomainSortBytes, SourceAll, "")
	if len(snap.Domains) != 1 || snap.Domains[0].BytesUp != uint64(total) {
		t.Fatalf("expected threshold flush of %d bytes, got %+v", total, snap.Domains)
	}
}

// TestCountingConnBatchConcurrent exercises the relay layout: Read and Write
// run on different goroutines, each direction's pending batch is owned by
// its goroutine, and Close drains both directions under concurrent I/O.
func TestCountingConnBatchConcurrent(t *testing.T) {
	s := newTestStats(t)
	wrapped := s.WrapConn(&fakeConn{readBuf: bytes.Repeat([]byte("y"), 64<<10)}, "socks5")
	s.BindConn(wrapped, "example.com")

	const iters = 8
	readChunk, writeChunk := make([]byte, 4096), make([]byte, 4096)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range iters {
			if _, err := wrapped.Read(readChunk); err != nil {
				t.Errorf("read: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for range iters {
			if _, err := wrapped.Write(writeChunk); err != nil {
				t.Errorf("write: %v", err)
				return
			}
		}
	}()
	wg.Wait()

	if err := wrapped.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	snap := s.Snapshot(DomainSortBytes, SourceAll, "")
	if len(snap.Domains) != 1 {
		t.Fatalf("expected 1 domain, got %v", snap.Domains)
	}
	d := snap.Domains[0]
	want := uint64(iters * len(readChunk))
	if d.BytesUp != want || d.BytesDown != want {
		t.Fatalf("expected %d up/%d down after concurrent relay, got %d/%d",
			want, want, d.BytesUp, d.BytesDown)
	}
}

// TestCountingConnCloseConcurrent races Close against active relay I/O: the
// closed latch must force a final drain of bytes that land after Close's
// own swap, so no byte is left permanently pending.
func TestCountingConnCloseConcurrent(t *testing.T) {
	s := newTestStats(t)
	wrapped := s.WrapConn(&fakeConn{readBuf: bytes.Repeat([]byte("z"), 1<<20)}, "http")
	s.BindConn(wrapped, "example.com")

	const iters = 500
	readChunk, writeChunk := make([]byte, 4096), make([]byte, 4096)
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			if _, err := wrapped.Read(readChunk); err != nil {
				t.Errorf("read: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < iters; i++ {
			if _, err := wrapped.Write(writeChunk); err != nil {
				t.Errorf("write: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		if err := wrapped.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	}()
	wg.Wait()

	// After Close, any byte that raced in must still be drained by the
	// closed-latch flush; totals therefore equal the writes that reported
	// success, regardless of interleaving.
	want := uint64(iters * len(readChunk))
	if got := s.bytesDown.Load(); got != want {
		t.Fatalf("expected %d total bytes down, got %d", want, got)
	}
}

func TestStatsRecordDNS(t *testing.T) {
	s := newTestStats(t)
	s.RecordDNS("Example.COM.", "")
	s.RecordDNS("example.com.", "")

	snap := s.Snapshot(DomainSortBytes, SourceAll, "")
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

	snap := s.Snapshot(DomainSortBytes, SourceAll, "")
	if len(snap.Domains) != 1 || snap.Domains[0].Domain != "fresh.example" {
		t.Fatalf("expected stale domain evicted, got %v", snap.Domains)
	}
}

func TestStatsDomainCapIsEnforced(t *testing.T) {
	s := newTestStats(t)
	now := time.Now()
	s.mu.Lock()
	for i := 0; i < maxTotalDomains; i++ {
		s.domains[fmt.Sprintf("host%d.example", i)] = &domainStat{lastSeen: now.Add(time.Duration(i) * time.Nanosecond)}
	}
	s.mu.Unlock()

	s.record("fresh.example", SourceHTTP, "", 1, 0, 0, func(d *domainStat, src *sourceStat, dc *clientStat) {})

	s.mu.Lock()
	n := len(s.domains)
	_, oldestPresent := s.domains["host0.example"]
	_, newestPresent := s.domains[fmt.Sprintf("host%d.example", maxTotalDomains-1)]
	_, freshPresent := s.domains["fresh.example"]
	s.mu.Unlock()
	want := maxTotalDomains - domainEvictionBatch + 1
	if n != want {
		t.Fatalf("expected %d domains after batch eviction, got %d", want, n)
	}
	if oldestPresent || !newestPresent || !freshPresent {
		t.Fatalf("unexpected retained domains: oldest=%v newest=%v fresh=%v", oldestPresent, newestPresent, freshPresent)
	}
}

func TestEvictOldestDomainsLocked(t *testing.T) {
	s := newTestStats(t)
	now := time.Now()
	s.mu.Lock()
	for i := 0; i < 4; i++ {
		s.domains[fmt.Sprintf("host%d.example", i)] = &domainStat{lastSeen: now.Add(time.Duration(i) * time.Second)}
	}
	s.evictOldestDomainsLocked(2)
	_, firstPresent := s.domains["host0.example"]
	_, secondPresent := s.domains["host1.example"]
	_, thirdPresent := s.domains["host2.example"]
	_, fourthPresent := s.domains["host3.example"]
	s.mu.Unlock()
	if firstPresent || secondPresent || !thirdPresent || !fourthPresent {
		t.Fatalf("unexpected eviction: first=%v second=%v third=%v fourth=%v", firstPresent, secondPresent, thirdPresent, fourthPresent)
	}
}

func TestStatsSnapshotSortsByTotalBytes(t *testing.T) {
	s := newTestStats(t)
	s.record("small.example", SourceHTTP, "", 0, 0, 0, func(d *domainStat, src *sourceStat, dc *clientStat) {
		d.bytesUp, d.bytesDown = 1, 1
		src.bytesUp, src.bytesDown = 1, 1
	})
	s.record("big.example", SourceHTTP, "", 0, 0, 0, func(d *domainStat, src *sourceStat, dc *clientStat) {
		d.bytesUp, d.bytesDown = 100, 100
		src.bytesUp, src.bytesDown = 100, 100
	})
	s.record("mid.example", SourceHTTP, "", 0, 0, 0, func(d *domainStat, src *sourceStat, dc *clientStat) {
		d.bytesUp, d.bytesDown = 10, 10
		src.bytesUp, src.bytesDown = 10, 10
	})

	snap := s.Snapshot(DomainSortBytes, SourceAll, "")
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
	snap := s.Snapshot(DomainSortBytes, SourceAll, "")
	// A fresh stats object must report a small number of seconds, not
	// nanoseconds (a time.Duration would serialize as ~1e9 per second).
	if snap.Uptime < 0 || snap.Uptime > 60 {
		t.Fatalf("expected uptime in seconds, got %d", snap.Uptime)
	}
}

func TestStatsActiveConnsTrackedAndIdempotentClose(t *testing.T) {
	s := newTestStats(t)
	wrapped := s.WrapConn(&fakeConn{}, "http")

	snap := s.Snapshot(DomainSortBytes, SourceAll, "")
	if snap.Active.HTTP != 1 || snap.Conns.HTTP != 1 {
		t.Fatalf("expected 1 active/1 total http conn, got %+v / %+v", snap.Active, snap.Conns)
	}

	if err := wrapped.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := wrapped.Close(); err != nil { // idempotent
		t.Fatalf("second close: %v", err)
	}
	snap = s.Snapshot(DomainSortBytes, SourceAll, "")
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

	snap := s.Snapshot(DomainSortBytes, SourceAll, "")
	if snap.Active.HTTP != 1 || snap.Active.HTTPS != 1 || snap.Active.Socks5 != 1 {
		t.Fatalf("unexpected active conns: %+v", snap.Active)
	}
	_ = http.Close()
	_ = https.Close()
	_ = socks.Close()
	snap = s.Snapshot(DomainSortBytes, SourceAll, "")
	if snap.Active.HTTP != 0 || snap.Active.HTTPS != 0 || snap.Active.Socks5 != 0 {
		t.Fatalf("expected all active conns released: %+v", snap.Active)
	}
}

func TestStatsRecordRoute(t *testing.T) {
	s := newTestStats(t)
	s.RecordRoute("block", "ads.example.com")
	s.RecordRoute("block", "ads.example.com")
	s.RecordRoute("block", "tracker.example.net")
	s.RecordRoute("direct", "")
	s.RecordRoute("proxy", "")
	s.RecordRoute("proxy", "")

	snap := s.Snapshot(DomainSortBytes, SourceAll, "")
	if snap.RuleHits.Block != 3 || snap.RuleHits.Direct != 1 || snap.RuleHits.Proxy != 2 {
		t.Fatalf("unexpected rule hits: %+v", snap.RuleHits)
	}
	if len(snap.Blocked) != 2 {
		t.Fatalf("expected 2 blocked domains, got %d", len(snap.Blocked))
	}
	if snap.Blocked[0].Domain != "ads.example.com" || snap.Blocked[0].Count != 2 {
		t.Fatalf("unexpected blocked ranking: %+v", snap.Blocked)
	}
	if snap.Blocked[1].Domain != "tracker.example.net" || snap.Blocked[1].Count != 1 {
		t.Fatalf("unexpected blocked ranking: %+v", snap.Blocked)
	}
}

func TestStatsBlockedCapIsEnforced(t *testing.T) {
	s := newTestStats(t)
	now := time.Now()
	s.mu.Lock()
	for i := 0; i < maxBlockedDomains; i++ {
		s.blocked[fmt.Sprintf("blocked-%d.example", i)] = &blockedStat{lastSeen: now.Add(time.Duration(i) * time.Nanosecond)}
	}
	s.mu.Unlock()

	s.RecordRoute("block", "fresh.example")

	s.mu.Lock()
	n := len(s.blocked)
	_, oldestPresent := s.blocked["blocked-0.example"]
	_, newestPresent := s.blocked[fmt.Sprintf("blocked-%d.example", maxBlockedDomains-1)]
	_, freshPresent := s.blocked["fresh.example"]
	s.mu.Unlock()
	want := maxBlockedDomains - blockedEvictionBatch + 1
	if n != want {
		t.Fatalf("expected %d blocked domains after batch eviction, got %d", want, n)
	}
	if oldestPresent || !newestPresent || !freshPresent {
		t.Fatalf("unexpected retained blocked domains: oldest=%v newest=%v fresh=%v", oldestPresent, newestPresent, freshPresent)
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
	s.RecordDNS("example.com", "")
	s.RecordRoute("proxy", "")
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

func TestStatsSnapshotDomainSortModes(t *testing.T) {
	s := newTestStats(t)
	now := time.Now()
	s.mu.Lock()
	s.domains["big.example"] = &domainStat{conns: 10, bytesUp: 1000, bytesDown: 0, lastSeen: now.Add(-time.Minute)}
	s.domains["hot.example"] = &domainStat{conns: 2, bytesUp: 100, bytesDown: 100, lastSeen: now}
	s.domains["busy.example"] = &domainStat{conns: 20, bytesUp: 10, bytesDown: 10, lastSeen: now.Add(-2 * time.Minute)}
	s.mu.Unlock()

	byBytes := s.Snapshot(DomainSortBytes, SourceAll, "")
	if byBytes.Domains[0].Domain != "big.example" {
		t.Fatalf("expected big.example first by bytes, got %q", byBytes.Domains[0].Domain)
	}

	byRecent := s.Snapshot(DomainSortRecent, SourceAll, "")
	if byRecent.Domains[0].Domain != "hot.example" {
		t.Fatalf("expected hot.example first by recency, got %q", byRecent.Domains[0].Domain)
	}

	byConns := s.Snapshot(DomainSortConns, SourceAll, "")
	if byConns.Domains[0].Domain != "busy.example" {
		t.Fatalf("expected busy.example first by conns, got %q", byConns.Domains[0].Domain)
	}

	fallback := s.Snapshot(DomainSort("bogus"), SourceAll, "")
	if fallback.Domains[0].Domain != "big.example" {
		t.Fatalf("expected invalid sort to fall back to bytes, got %q", fallback.Domains[0].Domain)
	}
}

func TestStatsSnapshotSourceFilter(t *testing.T) {
	s := newTestStats(t)
	now := time.Now()

	s.mu.Lock()
	s.domains["proxied.example"] = &domainStat{
		conns: 3, bytesUp: 300, bytesDown: 300, lastSeen: now,
		bySource: map[Source]*sourceStat{
			SourceHTTP:  {conns: 2, bytesUp: 200, bytesDown: 200, lastSeen: now},
			SourceHTTPS: {conns: 1, bytesUp: 100, bytesDown: 100, lastSeen: now},
		},
	}
	s.domains["queried.example"] = &domainStat{
		conns: 5, lastSeen: now,
		bySource: map[Source]*sourceStat{
			SourceDNS: {conns: 5, lastSeen: now},
		},
	}
	s.mu.Unlock()

	all := s.Snapshot(DomainSortBytes, SourceAll, "")
	if len(all.Domains) != 2 {
		t.Fatalf("expected 2 domains in all view, got %d", len(all.Domains))
	}

	dns := s.Snapshot(DomainSortBytes, SourceDNS, "")
	if len(dns.Domains) != 1 || dns.Domains[0].Domain != "queried.example" || dns.Domains[0].Conns != 5 {
		t.Fatalf("unexpected dns view: %+v", dns.Domains)
	}
	if dns.Domains[0].BytesUp != 0 || dns.Domains[0].BytesDown != 0 {
		t.Fatalf("dns view should carry no bytes, got %+v", dns.Domains[0])
	}

	httpView := s.Snapshot(DomainSortBytes, SourceHTTP, "")
	if len(httpView.Domains) != 1 || httpView.Domains[0].Domain != "proxied.example" || httpView.Domains[0].Conns != 2 {
		t.Fatalf("unexpected http view: %+v", httpView.Domains)
	}

	socks := s.Snapshot(DomainSortBytes, SourceSocks5, "")
	if len(socks.Domains) != 0 {
		t.Fatalf("expected no socks5 domains, got %+v", socks.Domains)
	}
}

func TestRecordDNSAttributedToDNSSource(t *testing.T) {
	s := newTestStats(t)
	s.RecordDNS("example.com.", "")

	dns := s.Snapshot(DomainSortBytes, SourceDNS, "")
	if len(dns.Domains) != 1 || dns.Domains[0].Domain != "example.com" || dns.Domains[0].Conns != 1 {
		t.Fatalf("unexpected dns view: %+v", dns.Domains)
	}

	httpView := s.Snapshot(DomainSortBytes, SourceHTTP, "")
	if len(httpView.Domains) != 0 {
		t.Fatalf("dns query must not appear in http view: %+v", httpView.Domains)
	}
}

func TestStatsSnapshotClientFilter(t *testing.T) {
	s := newTestStats(t)
	now := time.Now()

	s.mu.Lock()
	s.domains["a.example"] = &domainStat{
		conns: 4, bytesUp: 400, bytesDown: 400, lastSeen: now,
		byClient: map[string]*clientStat{
			"192.168.1.10": {conns: 3, bytesUp: 300, bytesDown: 300, lastSeen: now},
			"192.168.1.20": {conns: 1, bytesUp: 100, bytesDown: 100, lastSeen: now},
		},
	}
	s.domains["b.example"] = &domainStat{
		conns: 2, bytesUp: 200, bytesDown: 200, lastSeen: now,
		byClient: map[string]*clientStat{
			"192.168.1.10": {conns: 2, bytesUp: 200, bytesDown: 200, lastSeen: now},
		},
	}
	s.domains["c.example"] = &domainStat{
		conns: 7, bytesUp: 700, bytesDown: 700, lastSeen: now,
		byClient: map[string]*clientStat{
			"192.168.1.20": {conns: 7, bytesUp: 700, bytesDown: 700, lastSeen: now},
		},
	}
	s.mu.Unlock()

	// filtered by client: only that client's domains, with that client's values
	view := s.Snapshot(DomainSortBytes, SourceAll, "192.168.1.10")
	if len(view.Domains) != 2 {
		t.Fatalf("expected 2 domains for client, got %d", len(view.Domains))
	}
	if view.Domains[0].Domain != "a.example" || view.Domains[0].Conns != 3 {
		t.Fatalf("a.example should show client 10's conns (3), got %+v", view.Domains[0])
	}
	if view.Domains[1].Domain != "b.example" || view.Domains[1].Conns != 2 {
		t.Fatalf("b.example should show client 10's conns (2), got %+v", view.Domains[1])
	}

	// unknown client yields an empty list
	empty := s.Snapshot(DomainSortBytes, SourceAll, "203.0.113.9")
	if len(empty.Domains) != 0 {
		t.Fatalf("expected no domains for unknown client, got %+v", empty.Domains)
	}

	// the client chips are a navigation device: they must stay stable and
	// globally counted regardless of the active client filter, including
	// clients that share no domain with the selected one
	if len(view.Clients) != 2 {
		t.Fatalf("expected both clients in chips under filter, got %+v", view.Clients)
	}
	if view.Clients[0].IP != "192.168.1.20" || view.Clients[0].Conns != 8 {
		t.Fatalf("client .20 must keep its global conns (8) under filter, got %+v", view.Clients[0])
	}
	if len(empty.Clients) != 2 {
		t.Fatalf("chips must survive an unknown-client filter, got %+v", empty.Clients)
	}
}

func TestStatsSnapshotLastClientIP(t *testing.T) {
	s := newTestStats(t)
	now := time.Now()

	s.mu.Lock()
	s.domains["a.example"] = &domainStat{
		lastSeen: now,
		byClient: map[string]*clientStat{
			"192.168.1.10": {conns: 1, bytesUp: 100, bytesDown: 100, lastSeen: now.Add(-2 * time.Minute)},
			"192.168.1.20": {conns: 1, bytesUp: 100, bytesDown: 100, lastSeen: now.Add(-1 * time.Minute)},
		},
	}
	s.mu.Unlock()

	// unfiltered: the client with the most recent activity wins
	view := s.Snapshot(DomainSortBytes, SourceAll, "")
	if len(view.Domains) != 1 || view.Domains[0].LastClientIP != "192.168.1.20" {
		t.Fatalf("expected .20 as last client, got %+v", view.Domains)
	}

	// client-filtered: the filter itself is the last client of the view
	filtered := s.Snapshot(DomainSortBytes, SourceAll, "192.168.1.10")
	if len(filtered.Domains) != 1 || filtered.Domains[0].LastClientIP != "192.168.1.10" {
		t.Fatalf("expected filter client as last client, got %+v", filtered.Domains)
	}
}

func TestStatsSnapshotClientsAggregate(t *testing.T) {
	s := newTestStats(t)
	now := time.Now()

	s.mu.Lock()
	s.domains["a.example"] = &domainStat{
		lastSeen: now,
		byClient: map[string]*clientStat{
			"192.168.1.10": {conns: 3, bytesUp: 300, bytesDown: 300, lastSeen: now},
		},
	}
	s.domains["b.example"] = &domainStat{
		lastSeen: now,
		byClient: map[string]*clientStat{
			"192.168.1.10": {conns: 2, bytesUp: 200, bytesDown: 200, lastSeen: now},
			"192.168.1.20": {conns: 5, bytesUp: 50, bytesDown: 50, lastSeen: now},
		},
	}
	s.mu.Unlock()

	snap := s.Snapshot(DomainSortBytes, SourceAll, "")
	if len(snap.Clients) != 2 {
		t.Fatalf("expected 2 clients, got %+v", snap.Clients)
	}
	if snap.Clients[0].IP != "192.168.1.10" || snap.Clients[0].Conns != 5 || snap.Clients[0].BytesUp != 500 {
		t.Fatalf("unexpected top client: %+v", snap.Clients[0])
	}
	if snap.Clients[1].IP != "192.168.1.20" {
		t.Fatalf("unexpected second client: %+v", snap.Clients[1])
	}
}

func TestStatsTotalsSurviveStaleness(t *testing.T) {
	s := newTestStats(t)
	old := time.Now().Add(-2 * staleDomainAge)
	s.mu.Lock()
	s.domains["old.example"] = &domainStat{
		conns: 5, bytesUp: 500, bytesDown: 500, lastSeen: old,
		bySource: map[Source]*sourceStat{
			SourceHTTP: {conns: 5, bytesUp: 500, bytesDown: 500, lastSeen: old},
		},
	}
	s.mu.Unlock()

	// the window view hides the stale domain, the totals keep it
	snap := s.Snapshot(DomainSortBytes, SourceAll, "")
	if len(snap.Domains) != 0 {
		t.Fatalf("expected stale domain hidden from window, got %+v", snap.Domains)
	}
	tot := s.Totals()
	if len(tot.Domains) != 1 || tot.Domains[0].Domain != "old.example" || tot.Domains[0].Conns != 5 {
		t.Fatalf("expected stale domain kept in totals, got %+v", tot.Domains)
	}
}

func TestStatsTotalsClientsSurviveDomainEviction(t *testing.T) {
	s := newTestStats(t)
	s.record("evicted.example", SourceHTTP, "192.168.1.10", 2, 100, 200, func(d *domainStat, src *sourceStat, dc *clientStat) {
		d.conns += 2
		src.conns += 2
	})
	s.mu.Lock()
	delete(s.domains, "evicted.example") // simulate cap eviction
	s.mu.Unlock()

	tot := s.Totals()
	if len(tot.Domains) != 0 {
		t.Fatalf("expected no domains after eviction, got %+v", tot.Domains)
	}
	if len(tot.Clients) != 1 || tot.Clients[0].IP != "192.168.1.10" ||
		tot.Clients[0].Conns != 2 || tot.Clients[0].BytesUp != 100 || tot.Clients[0].BytesDown != 200 {
		t.Fatalf("expected client totals to survive domain eviction, got %+v", tot.Clients)
	}
}

func TestStatsClientCapIsEnforced(t *testing.T) {
	s := newTestStats(t)
	now := time.Now()
	s.mu.Lock()
	for i := 0; i < maxTotalClients; i++ {
		s.clients[fmt.Sprintf("192.168.%d.%d", i/256, i%256)] = &clientStat{lastSeen: now.Add(time.Duration(i) * time.Nanosecond)}
	}
	s.domains["existing.example"] = &domainStat{bySource: make(map[Source]*sourceStat), lastSeen: now}
	s.mu.Unlock()

	s.record("existing.example", SourceHTTP, "203.0.113.1", 1, 0, 0, func(d *domainStat, src *sourceStat, dc *clientStat) {})

	s.mu.Lock()
	n := len(s.clients)
	_, oldestPresent := s.clients["192.168.0.0"]
	_, newestPresent := s.clients[fmt.Sprintf("192.168.%d.%d", (maxTotalClients-1)/256, (maxTotalClients-1)%256)]
	_, freshPresent := s.clients["203.0.113.1"]
	s.mu.Unlock()
	want := maxTotalClients - clientEvictionBatch + 1
	if n != want {
		t.Fatalf("expected %d clients after batch eviction, got %d", want, n)
	}
	if oldestPresent || !newestPresent || !freshPresent {
		t.Fatalf("unexpected retained clients: oldest=%v newest=%v fresh=%v", oldestPresent, newestPresent, freshPresent)
	}
}

func TestStatsClientsPerDomainCap(t *testing.T) {
	s := newTestStats(t)
	for i := 0; i < maxClientsPerDomain+10; i++ {
		s.record("shared.example", SourceHTTP, fmt.Sprintf("192.0.2.%d", i), 1, 0, 0, func(d *domainStat, src *sourceStat, dc *clientStat) {})
	}
	s.mu.Lock()
	n := len(s.domains["shared.example"].byClient)
	s.mu.Unlock()
	if n > maxClientsPerDomain {
		t.Fatalf("expected per-domain client cap, got %d entries", n)
	}
}

func TestTopDomainsMatchesFullSort(t *testing.T) {
	now := time.Now()
	input := make([]DomainStat, 0, snapshotTopN+37)
	for i := 0; i < snapshotTopN+37; i++ {
		input = append(input, DomainStat{
			Domain:    fmt.Sprintf("host%03d.example", i),
			Conns:     uint64((i * 7) % 31),
			BytesUp:   uint64((i * 13) % 101),
			BytesDown: uint64((i * 17) % 97),
			LastSeen:  now.Add(time.Duration((i*19)%71) * time.Second),
		})
	}

	for _, mode := range []DomainSort{DomainSortBytes, DomainSortRecent, DomainSortConns} {
		want := slices.Clone(input)
		sortDomains(want, mode)
		want = want[:snapshotTopN]
		got := topDomains(slices.Clone(input), snapshotTopN, mode)
		if !slices.Equal(got, want) {
			t.Fatalf("top domains differ for %s:\n got %v\nwant %v", mode, got, want)
		}
	}

	tied := topDomains([]DomainStat{
		{Domain: "z.example", BytesUp: 1},
		{Domain: "a.example", BytesUp: 1},
	}, 2, DomainSortBytes)
	if tied[0].Domain != "a.example" || tied[1].Domain != "z.example" {
		t.Fatalf("expected domain tie break by name, got %v", tied)
	}
}

func TestTopClientsMatchesFullSort(t *testing.T) {
	agg := make(map[string]*ClientStat, clientsTopN+17)
	for i := 0; i < clientsTopN+17; i++ {
		ip := fmt.Sprintf("192.0.2.%03d", i)
		agg[ip] = &ClientStat{IP: ip, BytesUp: uint64((i * 11) % 113), BytesDown: uint64((i * 23) % 109)}
	}

	want := make([]ClientStat, 0, len(agg))
	for _, client := range agg {
		want = append(want, *client)
	}
	sort.Slice(want, func(i, j int) bool { return clientLess(want[i], want[j]) })
	want = want[:clientsTopN]
	got := topClients(agg)
	if !slices.Equal(got, want) {
		t.Fatalf("top clients differ:\n got %v\nwant %v", got, want)
	}

	tied := topClients(map[string]*ClientStat{
		"192.0.2.2": {IP: "192.0.2.2", BytesUp: 1},
		"192.0.2.1": {IP: "192.0.2.1", BytesUp: 1},
	})
	if tied[0].IP != "192.0.2.1" || tied[1].IP != "192.0.2.2" {
		t.Fatalf("expected IP tie break, got %v", tied)
	}
}
