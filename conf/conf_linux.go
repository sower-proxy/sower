// +build linux

package conf

import (
	"context"
	"flag"
	"fmt"
	"os/exec"
	"time"
)

func Init() {
	flag.StringVar(&Client.DNS.FlushCmd, "flush_dns", "", "flush dns command")
}

func execute(cmd string) error {
	ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "sh", "-c", Client.DNS.FlushCmd).CombinedOutput()
	if err != nil {
		return fmt.Errorf("cmd: %s, err: %s, output: %s", Client.DNS.FlushCmd, err, out)
	}
	return nil
}
