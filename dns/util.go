package dns

import (
	"github.com/golang/glog"
	"github.com/wweir/sower/conf"
)

var rule *Node

func init() {
	rule = NewNodeFromRule(conf.Conf.BlockList...)
	glog.V(2).Infof("block rule:\n%s", rule)

	conf.OnRefreash = append(conf.OnRefreash, func() error {
		rule = NewNodeFromRule(conf.Conf.BlockList...)
		glog.V(2).Infof("block rule:\n%s", rule)
		return nil
	})
}
