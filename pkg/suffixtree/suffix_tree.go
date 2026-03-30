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
	indexMap map[string]int
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
	return strings.ToLower(strings.TrimSuffix(item, n.sep))
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
		if idx := n.index(sec); idx >= 0 {
			if n.subNodes[idx] != nil {
				n.subNodes[idx].add([]string{""})
			}
			return
		}

		switch sec {
		case "", "*", "**":
			n.prepend(sec, nil)
		default:
			n.append(sec, nil)
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
			n.subNodes[idx].add([]string{""})
		}

		n.subNodes[idx].add(secs[:length-1])
	}
}

func (n *Node) Match(item string) bool {
	if n == nil {
		return false
	}

	return n.matchSecs(strings.Split(n.trim(item), n.sep))
}

func (n *node) matchSecs(secs []string) bool {
	length := len(secs)
	if length == 0 {
		if n == nil {
			return true
		}
		return n.index("") != -1 || n.index("**") != -1
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

// index return the sec index in node, or -1 if not found
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
			n.ensureIndexMap()
			n.indexMap[sec] = i
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
	n.indexMap[sec] = idx
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
	if n.indexMap != nil {
		return
	}

	n.indexMap = make(map[string]int, len(n.secs))
	for i := range n.secs {
		n.indexMap[n.secs[i]] = i
	}
}
