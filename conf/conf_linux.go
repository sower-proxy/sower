// +build linux

package conf

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

func Init() {
	flag.StringVar(&Conf.Router.FlushDNSCmd, "flush_dns", "", "flush dns command")
}

func execute(cmd string) error {
	ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "sh", "-c", Conf.Router.FlushDNSCmd).CombinedOutput()
	if err != nil {
		return fmt.Errorf("cmd: %s, err: %s, output: %s", Conf.Router.FlushDNSCmd, err, out)
	}
	return nil
}
