package util

import (
	"strings"
	"sync"
)

type Node struct {
	node
	sep string
	*sync.RWMutex
}
type node struct {
	node map[string]*node
}

func NewNodeFromRules(rules ...string) *Node {
	n := &Node{node{node: map[string]*node{}}, ".", &sync.RWMutex{}}
	for i := range rules {
		n.Add(rules[i])
	}
	return n
}

func (n *Node) String() string {
	n.RLock()
	defer n.RUnlock()
	return n.string("", "     ")
}
func (n *node) string(prefix, indent string) (out string) {
	for key, val := range n.node {
		out += prefix + key + "\n" + val.string(prefix+indent, indent)
	}
	return
}

func (n *Node) trim(item string) string {
	return strings.TrimSuffix(item, n.sep)
}

func (n *Node) Add(item string) {
	n.Lock()
	defer n.Unlock()
	n.add(strings.Split(n.trim(item), n.sep))
}
func (n *node) add(secs []string) {
	length := len(secs)
	switch length {
	case 0:
		return
	case 1:
		n.node[secs[length-1]] = &node{node: map[string]*node{"": {}}}
	default:
		sec := secs[length-1]
		if sec == "**" {
			sec = "*"
		}

		subNode, ok := n.node[sec]
		if !ok {
			subNode = &node{node: map[string]*node{}}
			n.node[sec] = subNode
		}
		subNode.add(secs[:length-1])
	}
}

func (n *Node) Match(item string) bool {
	if n == nil {
		return false
	}

	n.RLock()
	defer n.RUnlock()
	return n.matchSecs(strings.Split(n.trim(item), n.sep), false)
}

func (n *node) matchSecs(secs []string, fuzzNode bool) bool {
	length := len(secs)
	if length == 0 {
		if _, ok := n.node[""]; ok {
			return true
		}
		if _, ok := n.node["**"]; ok {
			return true
		}
		if _, ok := n.node["*"]; ok {
			return !fuzzNode
		}
		return false
	}

	if n, ok := n.node[secs[length-1]]; ok {
		if n.matchSecs(secs[:length-1], false) {
			return true
		}
	}
	if n, ok := n.node["*"]; ok {
		if n.matchSecs(secs[:length-1], true) {
			return true
		}
	}
	if _, ok := n.node["**"]; ok {
		return true
	}

	return false
}
