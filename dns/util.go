package dns

import (
	"github.com/golang/glog"
	"github.com/wweir/sower/conf"
)

var (
	blockList   *Node
	suggestList *Node
	writeList   = NewNode(".")
)

func init() {
	//first init
	blockList = loadRules("block", conf.Conf.BlockList)
	suggestList = loadRules("suggest", conf.Conf.BlockList)

	conf.OnRefreash = append(conf.OnRefreash, func() error {
		blockList = loadRules("block", conf.Conf.BlockList)
		suggestList = loadRules("suggest", conf.Conf.Suggestions)
		return nil
	})
}

func loadRules(name string, list []string) *Node {
	rule := NewNodeFromRules(".", list...)
	glog.V(2).Infof("load %s rule:\n%s", name, rule)
	return rule
}
