package dns

import (
	"strings"
)

type Node struct {
	sep  string
	Node map[string]*Node
}

func NewNode(sep string) *Node {
	return &Node{sep: sep, Node: map[string]*Node{}}
}
func NewNodeFromRules(sep string, rules ...string) *Node {
	node := NewNode(sep)
	for i := range rules {
		node.Add(rules[i])
	}
	return node
}

func (n *Node) String() string {
	return n.string("")
}
func (n *Node) string(prefix string) (out string) {
	for key, val := range n.Node {
		out += prefix + key + "\n" + val.string(prefix+"    ")
	}
	return
}
func (n *Node) trim(item string) string {
	return strings.TrimSuffix(item, n.sep)
}

func (n *Node) Add(item string) {
	n.add(strings.Split(n.trim(item), n.sep))
}
func (n *Node) add(secs []string) {
	length := len(secs)
	switch length {
	case 0:
		return
	case 1:
		n.Node[secs[length-1]] = NewNode(n.sep)
	default:
		subNode, ok := n.Node[secs[length-1]]
		if !ok {
			subNode = NewNode(n.sep)
			n.Node[secs[length-1]] = subNode
		}
		subNode.add(secs[:length-1])
	}
}

func (n *Node) Match(item string) bool {
	return n.matchSecs(strings.Split(n.trim(item), n.sep))
}

func (n *Node) matchSecs(secs []string) bool {
	length := len(secs)
	if length == 0 {
		switch len(n.Node) {
		case 0:
			return true
		case 1:
			_, ok := n.Node["*"]
			return ok
		default:
			return false
		}
	}

	if n, ok := n.Node[secs[length-1]]; ok {
		return n.matchSecs(secs[:length-1])
	}

	_, ok := n.Node["*"]
	return ok
}
