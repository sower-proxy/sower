package suffixtree

import (
	"runtime"
	"strings"
)

type Node struct {
	*node
	sep string
}
type node struct {
	secs     []string
	subNodes []*node
}

func NewNodeFromRules(rules ...string) *Node {
	n := &Node{&node{}, "."}
	for i := range rules {
		n.Add(rules[i])
	}

	n.node = n.node.lite()
	runtime.GC()
	return n
}

func (n *node) lite() *node {
	if n == nil {
		return nil
	}

	lite := &node{
		secs:     make([]string, 0, len(n.secs)),
		subNodes: make([]*node, 0, len(n.subNodes)),
	}
	lite.secs = append(lite.secs, n.secs...)
	for i := range n.subNodes {
		lite.subNodes = append(lite.subNodes, n.subNodes[i].lite())
	}
	return lite
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
		return n.index("") != -1
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
