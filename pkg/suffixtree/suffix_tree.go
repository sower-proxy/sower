package suffixtree

import (
	"strings"
)

type Node struct {
	*node
	sep string
	// Deprecated: unused. Retained to avoid breaking the package API; new
	// code should not depend on it.
	Count uint64
}
type node struct {
	secs     []string
	subNodes []*node
	indexMap map[string]int
	// rule is the full rule text when this node terminates a rule (a
	// single-label rule, a "" marker child, or a "**" wildcard child);
	// empty for intermediate nodes. It backs MatchRule's fast path.
	rule string
}

func NewNodeFromRules(rules ...string) *Node {
	n := &Node{&node{}, ".", 0}
	for i := range rules {
		n.Add(rules[i])
	}

	n.GC()
	return n
}

func (n *node) GC() {
	if n == nil {
		return
	}

	n.secs = GCSlice(n.secs)
	n.subNodes = GCSlice(n.subNodes)
	for i := range n.subNodes {
		n.subNodes[i].GC()
	}
}

func (n *Node) String() string {
	return n.string("", "     ")
}
func (n *node) string(prefix, indent string) (out string) {
	if n == nil {
		return
	}
	for key, val := range n.subNodes {
		out += prefix + n.secs[key] + "\n" + val.string(prefix+indent, indent)
	}
	return
}

func (n *Node) trim(item string) string {
	// Trim every trailing separator so a rule with repeated dots (dirty
	// remote rule files) cannot build an unreachable empty-label subtree.
	return strings.ToLower(strings.TrimRight(item, n.sep))
}

func (n *Node) Add(item string) {
	n.Count++
	n.add(strings.Split(n.trim(item), n.sep), item)
}
func (n *node) add(secs []string, rule string) {
	length := len(secs)
	switch length {
	case 0:
	case 1:
		sec := secs[length-1]
		if idx := n.index(sec); idx >= 0 {
			if n.subNodes[idx] != nil {
				n.subNodes[idx].add([]string{""}, rule)
			}
			return
		}

		switch sec {
		case "", "*", "**":
			n.prepend(sec, &node{rule: rule})
		default:
			n.append(sec, &node{rule: rule})
		}
	default:
		sec := secs[length-1]
		if sec == "**" { // ** is only allowed in the last sec
			sec = "*"
		}

		idx := n.index(sec)
		if idx == -1 {
			switch sec {
			case "", "*", "**":
				idx = n.prepend(sec, &node{})
			default:
				idx = n.append(sec, &node{})
			}

		} else if n.subNodes[idx] == nil {
			n.subNodes[idx] = &node{}
			n.subNodes[idx].add([]string{""}, rule)
		}

		n.subNodes[idx].add(secs[:length-1], rule)
	}
}

func (n *Node) Match(item string) bool {
	if n == nil {
		return false
	}

	return n.matchSecs(strings.Split(n.trim(item), n.sep))
}

// MatchRule reports one rule that matches item, or "", false when none
// does. Unlike a linear scan it resolves the match in tree depth: exact
// labels win over "*", which wins over a trailing "**", so the reported
// rule is the most specific one matching item rather than the first in
// insertion order. Match stays the bool fast path; MatchRule is for the
// rare paths that need the matched rule text.
func (n *Node) MatchRule(item string) (string, bool) {
	if n == nil {
		return "", false
	}

	return n.matchRuleSecs(strings.Split(n.trim(item), n.sep))
}

func (n *node) matchRuleSecs(secs []string) (string, bool) {
	length := len(secs)
	if length == 0 {
		if n == nil {
			// Unreachable from MatchRule (nil root is rejected there); keep
			// the semantics explicit so a nil node never reports a match.
			return "", false
		}
		if n.rule != "" {
			return n.rule, true
		}
		if idx := n.index(""); idx >= 0 {
			if r := n.subNodes[idx].rule; r != "" {
				return r, true
			}
		}
		if idx := n.index("**"); idx >= 0 {
			if r := n.subNodes[idx].rule; r != "" {
				return r, true
			}
		}
		return "", false
	}

	if idx := n.index(secs[length-1]); idx >= 0 {
		if r, ok := n.subNodes[idx].matchRuleSecs(secs[:length-1]); ok {
			return r, true
		}
	}
	if idx := n.index("*"); idx >= 0 {
		if r, ok := n.subNodes[idx].matchRuleSecs(secs[:length-1]); ok {
			return r, true
		}
	}
	if idx := n.index("**"); idx >= 0 {
		return n.subNodes[idx].rule, true
	}
	return "", false
}

func (n *node) matchSecs(secs []string) bool {
	length := len(secs)
	if length == 0 {
		if n == nil {
			return true
		}
		return n.rule != "" || n.index("") != -1 || n.index("**") != -1
	}

	if idx := n.index(secs[length-1]); idx >= 0 {
		if n.subNodes[idx].matchSecs(secs[:length-1]) {
			return true
		}
	}
	if idx := n.index("*"); idx >= 0 {
		if n.subNodes[idx].matchSecs(secs[:length-1]) {
			return true
		}
	}
	return n.index("**") >= 0
}

// indexMapThreshold: below this many children a node keeps a plain slice
// scan and no map, saving ~50+ bytes of map header and entries per small
// node; suffix trees are dominated by shallow fan-out nodes.
const indexMapThreshold = 8

// index return the sec index in node, or -1 if not found. Read-only: Match
// runs concurrently on a shared tree, so no map is built on this path.
func (n *node) index(sec string) int {
	if n == nil {
		return -1
	}

	if n.indexMap != nil {
		if idx, ok := n.indexMap[sec]; ok {
			return idx
		}
		return -1
	}

	for i := range n.secs {
		if n.secs[i] == sec {
			return i
		}
	}

	return -1
}

func (n *node) append(sec string, child *node) int {
	n.secs = append(n.secs, sec)
	n.subNodes = append(n.subNodes, child)
	n.ensureIndexMap()
	idx := len(n.secs) - 1
	if n.indexMap != nil {
		n.indexMap[sec] = idx
	}
	return idx
}

func (n *node) prepend(sec string, child *node) int {
	n.secs = append([]string{sec}, n.secs...)
	n.subNodes = append([]*node{child}, n.subNodes...)
	n.indexMap = nil
	n.ensureIndexMap()
	return 0
}

func (n *node) ensureIndexMap() {
	if n.indexMap != nil || len(n.secs) < indexMapThreshold {
		return
	}

	n.indexMap = make(map[string]int, len(n.secs))
	for i := range n.secs {
		n.indexMap[n.secs[i]] = i
	}
}
