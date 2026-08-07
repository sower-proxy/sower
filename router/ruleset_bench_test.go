package router

import (
	"fmt"
	"testing"
)

func BenchmarkRuleSetMatch(b *testing.B) {
	rules := make([]string, 0, 10000)
	for i := 0; i < 10000; i++ {
		rules = append(rules, fmt.Sprintf("host%d.example.com", i))
	}
	rs := NewRuleSet(rules...)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		rs.Match("www.example.com")
	}
}

func BenchmarkRuleSetMatchHit(b *testing.B) {
	rules := make([]string, 0, 10000)
	for i := 0; i < 10000; i++ {
		rules = append(rules, fmt.Sprintf("host%d.example.com", i))
	}
	rs := NewRuleSet(rules...)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		rs.Match("host5000.example.com")
	}
}
