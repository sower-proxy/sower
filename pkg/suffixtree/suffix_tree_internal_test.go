package suffixtree

import (
	"fmt"
	"sync"
	"testing"
)

// TestIndexMapThreshold: nodes with few children stay mapless (slice scan),
// nodes at or above the threshold carry a map.
func TestIndexMapThreshold(t *testing.T) {
	rules := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		rules = append(rules, fmt.Sprintf("host%d.wweir.cc", i))
	}
	n := NewNodeFromRules(rules...)

	// The "cc" node has one child ("wweir"), the "wweir" node has 20 children.
	cc := n.node.subNodes[n.index("cc")]
	if cc == nil || cc.indexMap != nil {
		t.Fatalf("expected single-child node without indexMap, got %+v", cc)
	}
	wweir := cc.subNodes[cc.index("wweir")]
	if wweir == nil || wweir.indexMap == nil {
		t.Fatalf("expected 20-child node to carry indexMap, got %+v", wweir)
	}
	if len(wweir.indexMap) != 20 {
		t.Fatalf("expected 20 indexMap entries, got %d", len(wweir.indexMap))
	}
}

// TestConcurrentMatchNoRace: Match runs concurrently on a shared tree; the
// index lookup must be read-only (no lazy map building on the query path).
func TestConcurrentMatchNoRace(t *testing.T) {
	rules := make([]string, 0, 10000)
	for i := 0; i < 10000; i++ {
		rules = append(rules, fmt.Sprintf("host%d.example.com", i))
	}
	rules = append(rules, "**.example.com", "*.wweir.cc")
	n := NewNodeFromRules(rules...)

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				n.Match(fmt.Sprintf("host%d.example.com", i%10000))
				n.Match("a.b.example.com")
				n.Match("x.wweir.cc")
			}
		}()
	}
	wg.Wait()
}

// TestIndexMapMemoryFootprint walks a realistic rule shape and reports how
// many nodes keep a map (>=8 children) versus the mapless small nodes that
// previously carried a ~48B+ map header each.
func TestIndexMapMemoryFootprint(t *testing.T) {
	// Many distinct second-level domains create lots of 1-2 child nodes.
	rules := make([]string, 0, 30000)
	for i := 0; i < 10000; i++ {
		rules = append(rules, fmt.Sprintf("ads%d.foo%d.com", i, i%50))
		rules = append(rules, fmt.Sprintf("track%d.bar%d.net", i, i%50))
		rules = append(rules, fmt.Sprintf("*.cdn%d.org", i%100))
	}
	n := NewNodeFromRules(rules...)

	var nodes, mapped, small int
	var walk func(*node)
	walk = func(cur *node) {
		if cur == nil {
			return
		}
		nodes++
		if len(cur.secs) >= indexMapThreshold {
			mapped++
		} else if len(cur.secs) > 0 {
			small++
		}
		for _, sub := range cur.subNodes {
			walk(sub)
		}
	}
	walk(n.node)

	// Each mapless small node saves a map header (~48B) plus per-entry
	// overhead (~16B); the slice scan costs nothing extra in memory.
	saved := small * 48
	t.Logf("nodes=%d mapped=%d small=%d estimated savings≈%d KiB",
		nodes, mapped, small, saved/1024)
	if small == 0 {
		t.Fatal("expected mapless small nodes in this shape")
	}
}

// BenchmarkSuffixTreeBuildAlloc reports the retained per-rule cost of a
// large rule set (map header savings included).
func BenchmarkSuffixTreeBuildAlloc(b *testing.B) {
	rules := make([]string, 0, 100000)
	for i := 0; i < 100000; i++ {
		rules = append(rules, fmt.Sprintf("host%d.sub.example.com", i))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		NewNodeFromRules(rules...)
	}
}
