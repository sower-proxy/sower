package suffixtree

import (
	"strings"
)

type Node struct {
	*node
	sep   string
	Count uint64
}
type node struct {
	secs     []string
	subNodes []*node
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
	return strings.TrimSuffix(item, n.sep)
}

func (n *Node) Add(item string) {
	n.Count++
	n.add(strings.Split(n.trim(item), n.sep))
}
func (n *node) add(secs []string) {
	length := len(secs)
	switch length {
	case 0:
	case 1:
		sec := secs[length-1]
		switch sec {
		case "", "*", "**":
			n.secs = append([]string{sec}, n.secs...)
			n.subNodes = append([]*node{nil}, n.subNodes...)
		default:
			n.secs = append(n.secs, sec)
			n.subNodes = append(n.subNodes, nil)
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
				idx = 0
				n.secs = append([]string{sec}, n.secs...)
				n.subNodes = append([]*node{{}}, n.subNodes...)
			default:
				idx = len(n.secs)
				n.secs = append(n.secs, sec)
				n.subNodes = append(n.subNodes, &node{})
			}

		} else if n.subNodes[idx] == nil {
			n.subNodes[idx] = &node{}
			n.subNodes[idx].add([]string{""})
		}

		n.subNodes[idx].add(secs[:length-1])
	}
}

func (n *Node) Match(item string) bool {
	if n == nil {
		return false
	}

	return n.matchSecs(strings.Split(n.trim(item), n.sep), false)
}

func (n *node) matchSecs(secs []string, fuzzNode bool) bool {
	length := len(secs)
	if length == 0 {
		if n == nil {
			return true
		}
		return n.index("") != -1 || n.index("**") != -1
	}

	if idx := n.index(secs[length-1]); idx >= 0 {
		if n.subNodes[idx].matchSecs(secs[:length-1], false) {
			return true
		}
	}
	if idx := n.index("*"); idx >= 0 {
		if n.subNodes[idx].matchSecs(secs[:length-1], true) {
			return true
		}
	}
	return n.index("**") >= 0
}

// index return the sec index in node, or -1 if not found
func (n *node) index(sec string) int {
	if n == nil {
		return -1
	}
	for s := range n.secs {
		if n.secs[s] == sec {
			return s
		}
	}
	return -1
}
