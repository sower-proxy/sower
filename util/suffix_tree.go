package util

import (
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
	return n
}

func (n *Node) String() string {
	return n.string("", "     ")
}
func (n *node) string(prefix, indent string) (out string) {
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
		subNode := &node{secs: []string{""}, subNodes: []*node{{}}}
		switch sec {
		case "", "*", "**":
			n.secs = append([]string{sec}, n.secs...)
			n.subNodes = append([]*node{subNode}, n.subNodes...)
		default:
			n.secs = append(n.secs, sec)
			n.subNodes = append(n.subNodes, subNode)
		}
	default:
		sec := secs[length-1]
		if sec == "**" {
			sec = "*"
		}

		subNode, ok := n.find(sec)
		if !ok {
			subNode = &node{}
			switch sec {
			case "", "*", "**":
				n.secs = append([]string{sec}, n.secs...)
				n.subNodes = append([]*node{subNode}, n.subNodes...)
			default:
				n.secs = append(n.secs, sec)
				n.subNodes = append(n.subNodes, subNode)
			}
		}
		subNode.add(secs[:length-1])
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
		if len(n.secs) == 0 {
			return true
		}
		if _, ok := n.find(""); ok {
			return true
		}
		if _, ok := n.find("**"); ok {
			return true
		}
		if _, ok := n.find("*"); ok {
			return !fuzzNode
		}
		return false
	}

	if n, ok := n.find(secs[length-1]); ok {
		if n.matchSecs(secs[:length-1], false) {
			return true
		}
	}
	if n, ok := n.find("*"); ok {
		if n.matchSecs(secs[:length-1], true) {
			return true
		}
	}
	if _, ok := n.find("**"); ok {
		return true
	}

	return false
}
func (n *node) find(sec string) (*node, bool) {
	if n == nil {
		return nil, false
	}
	for s := range n.secs {
		if n.secs[s] == sec {
			return n.subNodes[s], true
		}
	}
	return nil, false
}
