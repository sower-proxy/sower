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

const svcPath = "/Library/LaunchDaemons/sower.plist"
const svcFile = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>sower</string>
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

var ConfigDir = ""

func _init() {
	if _, err := os.Stat(execDir + "/sower.toml"); err == nil {
		ConfigDir = execDir
	} else {
		dir, _ := os.UserConfigDir()
		ConfigDir = filepath.Join("/", dir, "sower")
	}

	if _, err := os.Stat(ConfigDir + "/sower.toml"); err != nil {
		flag.StringVar(&Conf.file, "f", "", "config file, rewrite all other parameters if set")
	} else {
		flag.StringVar(&Conf.file, "f", ConfigDir+"/sower.toml", "config file, rewrite all other parameters if set")
	}

	flag.StringVar(&installCmd, "install", "", "install service with cmd, eg: '-f \""+ConfigDir+"/sower.toml\"'")
}

func runAsService() {}

func install() {
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
	ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "sh", "-c", cmd).CombinedOutput()
	if err != nil {
		return fmt.Errorf("cmd: %s, err: %s, output: %s", cmd, err, out)
	}
	return nil
}
