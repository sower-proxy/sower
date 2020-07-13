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

	"github.com/wweir/util-go/log"
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

var (
	ConfigDir     = ""
	installCmd    = ""
	uninstallFlag = false
)

func beforeInitFlag() {
	if _, err := os.Stat(execDir + "/sower.toml"); err == nil {
		ConfigDir = execDir
	} else {
		dir, _ := os.UserConfigDir()
		ConfigDir = filepath.Join("/", dir, "sower")
	}

	if _, err := os.Stat(ConfigDir + "/sower.toml"); err != nil {
		flag.StringVar(&conf.file, "f", "", "config file, rewrite all other parameters if set")
	} else {
		flag.StringVar(&conf.file, "f", ConfigDir+"/sower.toml", "config file, rewrite all other parameters if set")
	}

	flag.StringVar(&installCmd, "install", "", "install service with cmd, eg: '-f \""+ConfigDir+"/sower.toml\"'")
	flag.BoolVar(&uninstallFlag, "uninstall", false, "uninstall service")
}

func afterInitFlag() {
	switch {
	case installCmd != "":
		install()
	case uninstallFlag:
		uninstall()
	default:
		return
	}
	os.Exit(0)
}

func install() {
	if err := ioutil.WriteFile(svcPath, []byte(fmt.Sprintf(svcFile, execFile, installCmd)), 0644); err != nil {
		log.Fatalw("write service file", "err", err)
	}

	execute("launchctl unload " + svcPath)
	if err := execute("launchctl load -wF " + svcPath); err != nil {
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
