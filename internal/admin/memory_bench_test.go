package admin

import (
	"fmt"
	"runtime"
	"testing"
)

// TestStatsMemoryFootprint measures the heap retained by the domain table at
// realistic and worst-case cardinalities. Run with -count=1.
func TestStatsMemoryFootprint(t *testing.T) {
	if testing.Short() {
		t.Skip("memory footprint test; skipped in -short mode")
	}
	for _, domains := range []int{10_000, 50_000, 100_000} {
		s := newTestStats(t)
		for i := 0; i < domains; i++ {
			domain := fmt.Sprintf("host%d.example.com", i)
			s.record(domain, SourceHTTPS, "192.168.1.10", 1, 1000, 2000, func(d *domainStat, src *sourceStat, dc *clientStat) {
				d.conns++
				d.bytesUp += 1000
				d.bytesDown += 2000
				src.bytesUp += 1000
				src.bytesDown += 2000
				if dc != nil {
					dc.bytesUp += 1000
					dc.bytesDown += 2000
				}
			})
		}

		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		s.mu.Lock()
		retained := len(s.domains)
		s.mu.Unlock()
		t.Logf("%d records: retained=%d heap=%d KiB (%.0f B/domain)",
			domains, retained, m.HeapAlloc/1024, float64(m.HeapAlloc)/float64(max(retained, 1)))
		if retained != domains {
			t.Errorf("expected %d retained domains, got %d", domains, retained)
		}
	}
}
