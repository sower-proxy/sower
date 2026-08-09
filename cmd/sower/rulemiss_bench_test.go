package main

import (
	"fmt"
	"testing"

	"github.com/sower-proxy/sower/router"
)

// BenchmarkRuleMissOnMiss measures the connection hot-path cost of the
// rule-miss tracker under parallel load — the per-connection lock + map
// update that detection/fallback traffic hits on every connection.
func BenchmarkRuleMissOnMiss(b *testing.B) {
	tracker := newRuleMissTracker()
	domains := make([]string, 1024)
	for i := range domains {
		domains[i] = fmt.Sprintf("host%d.sub.example.com", i)
	}
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			tracker.OnMiss(domains[i&1023])
			i++
		}
	})
}

// BenchmarkRuleHitOnHit measures the rule-hit hot path for the direct set,
// where a large share of connections match a rule.
func BenchmarkRuleHitOnHit(b *testing.B) {
	rs := router.NewRuleSet("**.example.com", "**.cn", "**.intranet")
	tracker := newRuleHitTracker(rs, maxRuleHitsWide)
	domains := make([]string, 1024)
	for i := range domains {
		domains[i] = fmt.Sprintf("host%d.example.com", i)
	}
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			tracker.OnHit(domains[i&1023])
			i++
		}
	})
}

// BenchmarkRuleMissTop measures the read path of the miss panel (admin
// query), which aggregates and sorts the tracked domains.
func BenchmarkRuleMissTop(b *testing.B) {
	tracker := newRuleMissTracker()
	for i := 0; i < 30000; i++ {
		tracker.OnMiss(fmt.Sprintf("host%d.sub.example.com", i))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tracker.Top(20)
	}
}
