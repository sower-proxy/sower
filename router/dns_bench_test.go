package router

import (
	"fmt"
	"testing"
	"time"
)

// BenchmarkDNSPrepareUpstreamState measures the per-query upstream state
// snapshot cost (mutex + address-slice copy).
func BenchmarkDNSPrepareUpstreamState(b *testing.B) {
	r, err := NewRouter(nil, "", "223.5.5.5", "", nil)
	if err != nil {
		b.Fatal(err)
	}
	r.dns.upstreamAddrs = []string{"1.1.1.1:53", "8.8.8.8:53", "223.5.5.5:53"}
	r.dns.upstreamIndex = 0

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		r.prepareUpstreamState(time.Now())
	}
}

// BenchmarkDNSRuleMatchPath measures the rule-classification cost of one
// DNS query: domain-list construction plus up to three RuleSet.Match calls.
func BenchmarkDNSRuleMatchPath(b *testing.B) {
	r, err := NewRouter(nil, "", "223.5.5.5", "", nil)
	if err != nil {
		b.Fatal(err)
	}
	rules := make([]string, 0, 10000)
	for i := 0; i < 10000; i++ {
		rules = append(rules, fmt.Sprintf("host%d.example.com", i))
	}
	r.BlockRule.Replace(rules...)
	r.DirectRule.Replace("direct.example.com")
	r.ProxyRule.Replace("proxy.example.com")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		domains := dnsRouteDomains("www.example.com", 1)
		_ = dnsRuleMatch(r.BlockRule, domains)
		if !dnsRuleMatch(r.DirectRule, domains) {
			_ = dnsRuleMatch(r.ProxyRule, domains)
		}
	}
}
