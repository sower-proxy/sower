package router

import (
	"fmt"
	"runtime"
	"testing"
)

// keepGlobal pins the trees across GC; without it the compiler may treat
// them as dead after the Match checks.
var keepGlobal struct {
	block  *RuleSet
	direct *RuleSet
	proxy  *RuleSet
}

// TestRulesetMemoryFootprint builds rule shapes matching the production
// ad/china/gfw lists (synthetic, no external files) and reports the retained
// heap. Skipped under -short.
func TestRulesetMemoryFootprint(t *testing.T) {
	if testing.Short() {
		t.Skip("memory footprint test; skipped in -short mode")
	}
	t.Cleanup(func() {
		keepGlobal = struct {
			block  *RuleSet
			direct *RuleSet
			proxy  *RuleSet
		}{}
	})

	// adlist-like: many exact domains under a few second-level domains.
	block := make([]string, 0, 160000)
	for i := 0; i < 160000; i++ {
		block = append(block, fmt.Sprintf("ads%d.doubleclick.net", i))
	}
	// chinalist-like: many exact domains under many second-level domains.
	direct := make([]string, 0, 110000)
	for i := 0; i < 110000; i++ {
		direct = append(direct, fmt.Sprintf("host%d.example%d.com", i, i%2000))
	}
	// gfwlist-like: wildcard-heavy.
	proxy := make([]string, 0, 5000)
	for i := 0; i < 5000; i++ {
		proxy = append(proxy, fmt.Sprintf("**.site%d.com", i%100))
	}
	t.Logf("rules: block=%d direct=%d proxy=%d", len(block), len(direct), len(proxy))

	keepGlobal.block = NewRuleSet(block...)
	keepGlobal.direct = NewRuleSet(direct...)
	keepGlobal.proxy = NewRuleSet(proxy...)

	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	before := m.HeapAlloc

	// Keep references live across GC.
	if !keepGlobal.block.Match("ads1.doubleclick.net") || !keepGlobal.direct.Match("host1.example1.com") || !keepGlobal.proxy.Match("a.b.site1.com") {
		t.Fatal("unexpected match result")
	}
	runtime.GC()
	runtime.ReadMemStats(&m)
	t.Logf("retained after GC: %d KiB (%.1f MB)", m.HeapAlloc/1024, float64(m.HeapAlloc)/1024/1024)
	t.Logf("heap before/after: %d / %d", before, m.HeapAlloc)
}
