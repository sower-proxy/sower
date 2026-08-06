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

func TestStatsWrapConnCountsBytesBeforeBind(t *testing.T) {
	s := NewStats()
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
	s := NewStats()
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
	s := NewStats()
	s.BindConn(&fakeConn{}, "example.com")
	if len(s.Snapshot().Domains) != 0 {
		t.Fatal("expected unwrapped conn to be ignored")
	}
}

func TestStatsRecordDNS(t *testing.T) {
	s := NewStats()
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
	s := NewStats()
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
	s := NewStats()
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
	s := NewStats()
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
	s := NewStats()
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
