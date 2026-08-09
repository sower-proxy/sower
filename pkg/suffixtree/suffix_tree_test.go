package suffixtree_test

import (
	"testing"

	"github.com/sower-proxy/sower/pkg/suffixtree"
)

func TestNode_MatchRule(t *testing.T) {
	tests := []struct {
		name  string
		rules []string
		arg   string
		want  string
		ok    bool
	}{{
		"exact",
		[]string{"a.wweir.cc", "b.wweir.cc"},
		"a.wweir.cc", "a.wweir.cc", true,
	}, {
		"parent vs child picks most specific",
		[]string{"wweir.cc", "a.wweir.cc"},
		"a.wweir.cc", "a.wweir.cc", true,
	}, {
		"parent matches itself",
		[]string{"wweir.cc", "a.wweir.cc"},
		"wweir.cc", "wweir.cc", true,
	}, {
		"wildcard beats parent",
		[]string{"wweir.cc", "*.wweir.cc"},
		"b.wweir.cc", "*.wweir.cc", true,
	}, {
		"exact beats wildcard",
		[]string{"*.wweir.cc", "a.wweir.cc"},
		"a.wweir.cc", "a.wweir.cc", true,
	}, {
		"double star suffix",
		[]string{"**.doubleclick.net"},
		"ads.doubleclick.net", "**.doubleclick.net", true,
	}, {
		"double star matches bare domain",
		[]string{"**.github.com"},
		"github.com", "**.github.com", true,
	}, {
		"deep double star",
		[]string{"a.b.foo.com", "**.foo.com"},
		"z.foo.com", "**.foo.com", true,
	}, {
		"deep exact beats double star",
		[]string{"**.foo.com", "a.b.foo.com"},
		"a.b.foo.com", "a.b.foo.com", true,
	}, {
		"terminal node is also parent",
		[]string{"b.com", "a.b.com"},
		"b.com", "b.com", true,
	}, {
		"terminal node is also parent - deeper",
		[]string{"b.com", "a.b.com"},
		"a.b.com", "a.b.com", true,
	}, {
		"terminal node is also parent - no subdomain",
		[]string{"b.com", "a.b.com"},
		"x.b.com", "", false,
	}, {
		"sibling split",
		[]string{"a.b.com", "a.com"},
		"a.com", "a.com", true,
	}, {
		"sibling split - deeper",
		[]string{"a.b.com", "a.com"},
		"a.b.com", "a.b.com", true,
	}, {
		"no match",
		[]string{"a.wweir.cc"},
		"b.wweir.cc", "", false,
	}, {
		"normalize",
		[]string{"GitHub.Com", "*.Example.Com"},
		"A.Example.Com", "*.Example.Com", true,
	}, {
		"empty rule set",
		nil,
		"a.wweir.cc", "", false,
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := suffixtree.NewNodeFromRules(tt.rules...)
			got, ok := node.MatchRule(tt.arg)
			if ok != tt.ok || (ok && got != tt.want) {
				t.Errorf("Node.MatchRule(%s) = (%q, %v), want (%q, %v)", tt.arg, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestNode_Match(t *testing.T) {
	type test struct {
		arg  string
		want bool
	}
	tests := []struct {
		name  string
		node  *suffixtree.Node
		tests []test
	}{{
		"simple",
		suffixtree.NewNodeFromRules("a.wweir.cc", "b.wweir.cc"),
		[]test{
			{"a.wweir.cc", true},
			{"b.wweir.cc", true},
		},
	}, {
		"parent",
		suffixtree.NewNodeFromRules("wweir.cc", "a.wweir.cc"),
		[]test{
			{"wweir.cc", true},
			{"a.wweir.cc", true},
			{"b.wweir.cc", false},
		},
	}, {
		"fuzz1",
		suffixtree.NewNodeFromRules("wweir.cc", "a.wweir.cc", "*.wweir.cc"),
		[]test{
			{"wweir.cc", true},
			{"a.wweir.cc", true},
			{"b.wweir.cc", true},
			{"a.b.wweir.cc", false},
		},
	}, {
		"fuzz2",
		suffixtree.NewNodeFromRules("a.*.cc", "c.wweir.*"),
		[]test{
			{"wweir.cc", false},
			{"a.wweir.cc", true},
			{"b.wweir.cc", false},
			{"c.wweir.cc", true},
		},
	}, {
		"fuzz3",
		suffixtree.NewNodeFromRules("*.*.cc", "iamp.*.*"),
		[]test{
			{"wweir.cc", false},
			{"a.wweir.cc", true},
			{"b.wweir.cc", true},
			{"iamp.wweir.cc", true},
		},
	}, {
		"fuzz4",
		suffixtree.NewNodeFromRules("**.cc", "a.**.com", "**.wweir.*"),
		[]test{
			{"wweir.cc", true},
			{"a.wweir.cc", true},
			{"a.b.wweir.cc", true},
			{"a.fuzz.com", true},
			{"b.fuzz.com", false},
			{"www.wweir.com", true},
		},
	}, {
		"github",
		suffixtree.NewNodeFromRules("**.github.com"),
		[]test{
			{"github.com", true},
		},
	}, {
		"normalize domain",
		suffixtree.NewNodeFromRules("GitHub.Com", "*.Example.Com", "**.Foo.Com"),
		[]test{
			{"github.com", true},
			{"GitHub.com", true},
			{"github.com.", true},
			{"a.example.com", true},
			{"A.Example.Com", true},
			{"foo.com", true},
			{"a.b.foo.com", true},
		},
	}, {
		"parent after child",
		suffixtree.NewNodeFromRules("a.wweir.cc", "wweir.cc"),
		[]test{
			{"wweir.cc", true},
			{"a.wweir.cc", true},
			{"b.wweir.cc", false},
		},
	}, {
		"wildcard after child",
		suffixtree.NewNodeFromRules("a.wweir.cc", "*.wweir.cc"),
		[]test{
			{"a.wweir.cc", true},
			{"b.wweir.cc", true},
			{"a.b.wweir.cc", false},
		},
	}, {
		"deep wildcard after child",
		suffixtree.NewNodeFromRules("a.b.foo.com", "**.foo.com"),
		[]test{
			{"foo.com", true},
			{"a.b.foo.com", true},
			{"z.foo.com", true},
		},
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, test := range tt.tests {
				if got := tt.node.Match(test.arg); got != test.want {
					t.Errorf("Node.Match(%s) = %v, want %v", test.arg, got, test.want)
				}
			}
		})
	}
}
