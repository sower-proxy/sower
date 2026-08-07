package router

import (
	"strings"
	"sync"

	"github.com/sower-proxy/sower/pkg/suffixtree"
)

// RuleSet is a thread-safe rule container. It retains the raw rule list so
// rules can be listed and removed at runtime; the suffixtree is derived
// state and is rebuilt from the retained list whenever a rule is removed.
// Membership for Add/Remove uses the raw rule strings (set), so case and
// trailing-dot variants stay distinct entries in the configuration.
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

// Add appends rules that are not already present, using the raw rule string
// as the membership key. Duplicates are ignored.
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

// Replace swaps the entire rule list atomically, rebuilding the suffix
// tree. Literal duplicates in the input are dropped. It is used to rebuild
// a rule set from the boot baseline after a delta reset.
func (rs *RuleSet) Replace(rules ...string) {
	// Construct the full immutable candidate before taking the match lock.
	// The field swap below is the operation's linearization point.
	nextRules := make([]string, 0, len(rules))
	nextSet := make(map[string]struct{}, len(rules))
	nextTree := suffixtree.NewNodeFromRules()
	for _, rule := range rules {
		if _, ok := nextSet[rule]; ok {
			continue
		}
		nextSet[rule] = struct{}{}
		nextRules = append(nextRules, rule)
		nextTree.Add(rule)
	}
	nextTree.GC()

	rs.mu.Lock()
	rs.rules = nextRules
	rs.set = nextSet
	rs.tree = nextTree
	rs.mu.Unlock()
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

// MatchRule reports the first retained rule that matches item, mirroring the
// suffix semantics of Match: case-insensitive, trailing dot stripped, "*"
// matching one label and a trailing "**" any number. It is linear in the
// rule count and exists for the admin domain test; Match stays the fast path.
func (rs *RuleSet) MatchRule(item string) (string, bool) {
	if rs == nil {
		return "", false
	}

	rs.mu.RLock()
	defer rs.mu.RUnlock()
	for _, rule := range rs.rules {
		if matchRule(rule, item) {
			return rule, true
		}
	}
	return "", false
}

// matchRule reports whether one rule pattern matches item. A "**" in the
// last label position matches any remaining labels (including none); in the
// middle it behaves like "*" (one label), matching the suffix-tree builder.
func matchRule(rule, item string) bool {
	rule = strings.ToLower(strings.TrimSuffix(rule, "."))
	item = strings.ToLower(strings.TrimSuffix(item, "."))
	r := strings.Split(rule, ".")
	i := strings.Split(item, ".")
	ri, ii := len(r)-1, len(i)-1
	for ri >= 0 {
		switch r[ri] {
		case "**":
			if ri == 0 {
				return true // trailing "**" matches any remaining labels
			}
			fallthrough // mid-rule "**" behaves like "*"
		case "*":
			if ii < 0 {
				return false
			}
			ii--
		default:
			if ii < 0 || r[ri] != i[ii] {
				return false
			}
			ii--
		}
		ri--
	}
	return ii < 0
}

// List returns a copy of the retained rules.
func (rs *RuleSet) List() []string {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return append([]string(nil), rs.rules...)
}

// ListFiltered returns up to limit retained rules that contain q as a
// case-insensitive substring, starting at offset, plus the total number of
// matches. An empty q matches every rule. It avoids copying the full list
// when the caller only needs a page.
func (rs *RuleSet) ListFiltered(q string, offset, limit int) ([]string, uint64) {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	if q == "" {
		if offset >= len(rs.rules) {
			return []string{}, uint64(len(rs.rules))
		}
		end := offset + limit
		if end > len(rs.rules) {
			end = len(rs.rules)
		}
		return append([]string(nil), rs.rules[offset:end]...), uint64(len(rs.rules))
	}

	matched := make([]string, 0, min(limit, 64))
	total := uint64(0)
	for _, rule := range rs.rules {
		if !containsFold(rule, q) {
			continue
		}
		if total >= uint64(offset) && len(matched) < limit {
			matched = append(matched, rule)
		}
		total++
	}
	return matched, total
}

// containsFold reports whether s contains substr as a case-insensitive
// substring, without allocating per comparison.
func containsFold(s, substr string) bool {
	if substr == "" {
		return true
	}
	if len(substr) > len(s) {
		return false
	}
	for i := 0; i+len(substr) <= len(s); i++ {
		if strings.EqualFold(s[i:i+len(substr)], substr) {
			return true
		}
	}
	return false
}

// Count returns the number of retained rules.
func (rs *RuleSet) Count() uint64 {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return uint64(len(rs.rules))
}
