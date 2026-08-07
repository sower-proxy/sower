package admin

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var (
	benchmarkSnapshot TrafficSnapshot
	benchmarkTotals   Totals
)

func newBenchmarkStats(b *testing.B, domains int) *Stats {
	b.Helper()
	s, err := NewStats()
	if err != nil {
		b.Fatalf("new stats: %v", err)
	}
	b.Cleanup(func() {
		ctx, cancel := context.WithTimeout(b.Context(), time.Second)
		defer cancel()
		if err := s.Metrics().Shutdown(ctx); err != nil {
			b.Errorf("shutdown metrics: %v", err)
		}
	})

	now := time.Now()
	func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for i := 0; i < domains; i++ {
			domain := fmt.Sprintf("host%d.example", i)
			s.domains[domain] = &domainStat{
				conns:     uint64(i + 1),
				bytesUp:   uint64(i + 1),
				bytesDown: uint64(i + 1),
				lastSeen:  now,
				bySource: map[Source]*sourceStat{
					SourceHTTP: {conns: 1, bytesUp: 1, bytesDown: 1, lastSeen: now},
				},
			}
		}
	}()
	return s
}

func BenchmarkStatsSnapshot(b *testing.B) {
	for _, domains := range []int{1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("domains=%d", domains), func(b *testing.B) {
			s := newBenchmarkStats(b, domains)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				benchmarkSnapshot = s.Snapshot(DomainSortBytes, SourceAll, "")
			}
		})
	}
}

func BenchmarkStatsTotals(b *testing.B) {
	for _, domains := range []int{1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("domains=%d", domains), func(b *testing.B) {
			s := newBenchmarkStats(b, domains)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				benchmarkTotals = s.Totals()
			}
		})
	}
}

func BenchmarkStatsRecordConcurrentWithSnapshots(b *testing.B) {
	s := newBenchmarkStats(b, 10_000)
	stop := make(chan struct{})
	var snapshots sync.WaitGroup
	snapshots.Add(1)
	go func() {
		defer snapshots.Done()
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				benchmarkSnapshot = s.Snapshot(DomainSortBytes, SourceAll, "")
			}
		}
	}()
	b.Cleanup(func() {
		close(stop)
		snapshots.Wait()
	})

	var next atomic.Uint64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			i := next.Add(1) % 10_000
			s.record(fmt.Sprintf("host%d.example", i), SourceHTTP, "", 0, 1, 1,
				func(d *domainStat, src *sourceStat, dc *clientStat) {
					d.bytesUp++
					d.bytesDown++
					src.bytesUp++
					src.bytesDown++
				})
		}
	})
}

func BenchmarkStatsInsertAtDomainCapacity(b *testing.B) {
	s := newBenchmarkStats(b, maxTotalDomains)
	var next atomic.Uint64
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		i := next.Add(1)
		s.record(fmt.Sprintf("new%d.example", i), SourceHTTP, "", 1, 0, 0,
			func(d *domainStat, src *sourceStat, dc *clientStat) {
				d.conns++
				src.conns++
			})
	}
}
