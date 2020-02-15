// +build darwin

package conf

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/wweir/utils/log"
)

const svcPath = "/Library/LaunchDaemons/cc.wweir.sower.plist"
const svcFile = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>cc.wweir.sower</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/sh</string>
    <string>-c</string>
    <string>%s %s</string>
  </array>
  <key>KeepAlive</key>
  <true/>
  <key>RunAtLoad</key>
  <true/>
</dict>
</plist>`

func Init() {
	flag.StringVar(&conf.file, "f", "", "config file, rewrite all other parameters if set")
	flag.StringVar(&Client.DNS.FlushCmd, "flush_dns", "pkill mDNSResponder || true", "flush dns command")
	flag.StringVar(&installCmd, "install", "", "install service with cmd, eg: '-f /etc/sower/sower.toml'")
}

func install() {
	execFile, err := filepath.Abs(os.Args[0])
	if err != nil {
		log.Fatalw("get binary path", "err", err)
	}

	if err := ioutil.WriteFile(svcPath, []byte(fmt.Sprintf(svcFile, execFile, installCmd)), 0644); err != nil {
		log.Fatalw("write service file", "err", err)
	}

	execute("launchctl unload " + svcPath)
	if err := execute("launchctl load -w " + svcPath); err != nil {
		log.Fatalw("install service", "err", err)
	}
}
func uninstall() {
	execute("launchctl unload " + svcPath)
	os.Remove(svcPath)
	os.RemoveAll("/etc/sower")
}
func execute(cmd string) error {
	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "sh", "-c", cmd).CombinedOutput()
	if err != nil {
		return fmt.Errorf("cmd: %s, err: %s, output: %s", cmd, err, out)
	}
	return nil
}
