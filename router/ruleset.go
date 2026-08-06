package router

import (
	"sync"

	"github.com/sower-proxy/sower/pkg/suffixtree"
)

// RuleSet is a thread-safe rule container. It retains the raw rule list so
// rules can be listed and removed at runtime; the suffixtree is derived
// state and is rebuilt from the retained list whenever a rule is removed.
type RuleSet struct {
	mu    sync.RWMutex
	rules []string
	set   map[string]struct{}
	tree  *suffixtree.Node
}

// NewRuleSet returns a RuleSet initialized with the given rules.
func NewRuleSet(rules ...string) *RuleSet {
	rs := &RuleSet{set: make(map[string]struct{}, len(rules))}
	rs.Add(rules...)
	rs.Compact()
	return rs
}

// Add appends rules that are not already present. Duplicates are ignored.
func (rs *RuleSet) Add(rules ...string) {
	if len(rules) == 0 {
		return
	}

	rs.mu.Lock()
	defer rs.mu.Unlock()
	for _, rule := range rules {
		if _, ok := rs.set[rule]; ok {
			continue
		}
		rs.set[rule] = struct{}{}
		rs.rules = append(rs.rules, rule)
		if rs.tree == nil {
			rs.tree = suffixtree.NewNodeFromRules()
		}
		rs.tree.Add(rule)
	}
}

// Compact runs garbage collection on the underlying tree. Call it after a
// batch of adds to avoid per-add compaction overhead on large rule files.
func (rs *RuleSet) Compact() {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.tree != nil {
		rs.tree.GC()
	}
}

// Remove deletes the first occurrence of the rule and rebuilds the tree from
// the retained list. It reports whether the rule was present.
func (rs *RuleSet) Remove(rule string) bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if _, ok := rs.set[rule]; !ok {
		return false
	}

	delete(rs.set, rule)
	for i := range rs.rules {
		if rs.rules[i] == rule {
			rs.rules = append(rs.rules[:i], rs.rules[i+1:]...)
			break
		}
	}
	rs.tree = suffixtree.NewNodeFromRules(rs.rules...)
	return true
}

// Match reports whether any rule matches the item.
func (rs *RuleSet) Match(item string) bool {
	if rs == nil {
		return false
	}

	rs.mu.RLock()
	defer rs.mu.RUnlock()
	if rs.tree == nil {
		return false
	}
	return rs.tree.Match(item)
}

// List returns a copy of the retained rules.
func (rs *RuleSet) List() []string {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return append([]string(nil), rs.rules...)
}

// Count returns the number of retained rules.
func (rs *RuleSet) Count() uint64 {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return uint64(len(rs.rules))
}
