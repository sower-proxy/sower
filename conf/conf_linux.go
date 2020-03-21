// +build linux

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

const svcPath = "/etc/systemd/system/sower.service"
const svcFile = `[Unit]
Description=Sower client service
After=network.target

[Install]
WantedBy=multi-user.target

[Service]
Type=simple
User=root
WorkingDirectory=/tmp
ExecStart=%s %s
RestartSec=3
Restart=on-failure`

var ConfigDir = ""

func Init() {
	if _, err := os.Stat(execDir + "/sower.toml"); err == nil {
		ConfigDir = execDir

	} else if stat, err := os.Stat("/etc/sower"); err == nil && stat.IsDir() {
		ConfigDir = "/etc/sower"

	} else {
		dir, _ := os.UserConfigDir()
		ConfigDir = filepath.Join("/", dir, "sower")
	}

	if _, err := os.Stat(ConfigDir + "/sower.toml"); err != nil {
		flag.StringVar(&Conf.file, "f", "", "config file, rewrite all other parameters if set")
	} else {
		flag.StringVar(&Conf.file, "f", ConfigDir+"/sower.toml", "config file, rewrite all other parameters if set")
	}

	flag.StringVar(&installCmd, "install", "", "install service with cmd, eg: '-f "+ConfigDir+"/sower.toml'")
}

func install() {
	execFile, err := filepath.Abs(os.Args[0])
	if err != nil {
		log.Fatalw("get binary path", "err", err)
	}

	if err := ioutil.WriteFile(svcPath, []byte(fmt.Sprintf(svcFile, execFile, installCmd)), 0644); err != nil {
		log.Fatalw("write service file", "err", err)
	}
	if err := execute("systemctl daemon-reload"); err != nil {
		log.Fatalw("install service", "err", err)
	}
	if err := execute("systemctl enable sower"); err != nil {
		log.Fatalw("install service", "err", err)
	}
	if err := execute("systemctl start sower"); err != nil {
		log.Fatalw("install service", "err", err)
	}
}

func uninstall() {
	execute("systemctl stop sower")
	execute("systemctl disable sower")
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
