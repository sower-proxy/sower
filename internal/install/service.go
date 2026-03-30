package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sower-proxy/sower/config"
)

type ConfirmFunc func(label string) bool

type installOptions struct {
	confirm           ConfirmFunc
	geteuid           func() int
	executable        func() (string, error)
	evalSymlinks      func(string) (string, error)
	serviceIsActive   func() bool
	systemctl         func(args ...string) error
	targetBinaryPath  string
	servicePath       string
	configPath        string
	defaultFakeSite   string
	defaultConfigTOML string
}

func InstallService(confirm ConfirmFunc) error {
	return installService(installOptions{
		confirm:           confirm,
		geteuid:           os.Geteuid,
		executable:        os.Executable,
		evalSymlinks:      filepath.EvalSymlinks,
		serviceIsActive:   serviceIsActive,
		systemctl:         systemctl,
		targetBinaryPath:  "/usr/local/bin/sowerd",
		servicePath:       "/etc/systemd/system/sowerd.service",
		configPath:        "/etc/sower/sowerd.toml",
		defaultFakeSite:   "/var/www",
		defaultConfigTOML: config.ExampleSowerdConfigTOML,
	})
}

func installService(opts installOptions) error {
	if opts.geteuid() != 0 {
		return fmt.Errorf("installing systemd service requires root privileges, try running with sudo")
	}

	execPath, err := opts.executable()
	if err != nil {
		return fmt.Errorf("get executable path: %w", err)
	}
	execPath, err = opts.evalSymlinks(execPath)
	if err != nil {
		return fmt.Errorf("resolve executable symlink: %w", err)
	}

	isUpdate := opts.serviceIsActive()
	if execPath != opts.targetBinaryPath {
		label := fmt.Sprintf("Copy binary to %s?", opts.targetBinaryPath)
		if isUpdate {
			label = fmt.Sprintf("Update binary at %s?", opts.targetBinaryPath)
		}
		if opts.confirm != nil && opts.confirm(label) {
			if err := copyFile(execPath, opts.targetBinaryPath, 0o755); err != nil {
				return err
			}
			if isUpdate {
				_ = opts.systemctl("stop", "sowerd")
			}
			fmt.Printf("  OK binary copied to %s\n", opts.targetBinaryPath)
			execPath = opts.targetBinaryPath
		}
	}

	if err := writeServiceFile(opts.servicePath, execPath, opts.configPath); err != nil {
		return err
	}
	fmt.Printf("  OK service file installed: %s\n", opts.servicePath)

	if err := ensureDefaultFiles(opts.configPath, opts.defaultFakeSite, opts.defaultConfigTOML); err != nil {
		return err
	}

	if err := opts.systemctl("daemon-reload"); err != nil {
		return err
	}
	fmt.Println("  OK systemd daemon reloaded")

	if isUpdate {
		return finishUpdate(opts.confirm, opts.systemctl)
	}
	return finishInstall(opts.confirm, opts.systemctl)
}

func finishInstall(confirm ConfirmFunc, systemctlFn func(args ...string) error) error {
	if confirm == nil || !confirm("Start and enable sowerd service now?") {
		fmt.Println()
		fmt.Println("sowerd service installed. Next steps:")
		fmt.Println("  1. Edit /etc/sower/sowerd.toml")
		fmt.Println("  2. sudo systemctl start sowerd")
		fmt.Println("  3. sudo systemctl enable sowerd")
		return nil
	}

	if err := systemctlFn("enable", "sowerd"); err != nil {
		return err
	}
	if err := systemctlFn("start", "sowerd"); err != nil {
		return err
	}
	fmt.Println("  OK service enabled and started")
	return nil
}

func finishUpdate(confirm ConfirmFunc, systemctlFn func(args ...string) error) error {
	if confirm == nil || !confirm("Restart sowerd service now?") {
		fmt.Println()
		fmt.Println("sowerd service updated. To apply:")
		fmt.Println("  sudo systemctl restart sowerd")
		return nil
	}

	if err := systemctlFn("restart", "sowerd"); err != nil {
		return err
	}
	fmt.Println("  OK service restarted")
	return nil
}

func serviceIsActive() bool {
	cmd := exec.Command("systemctl", "is-active", "--quiet", "sowerd")
	return cmd.Run() == nil
}

func systemctl(args ...string) error {
	cmd := exec.Command("systemctl", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl %s: %w - %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read binary %s: %w", src, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create binary directory: %w", err)
	}
	if err := os.WriteFile(dst, data, mode); err != nil {
		return fmt.Errorf("write binary to %s: %w", dst, err)
	}
	return nil
}

func writeServiceFile(servicePath, execPath, configPath string) error {
	if err := os.MkdirAll(filepath.Dir(servicePath), 0o755); err != nil {
		return fmt.Errorf("create service directory: %w", err)
	}

	serviceContent := fmt.Sprintf(`[Unit]
Description=sower server service
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s -c %s
Restart=always
RestartSec=5s

[Install]
WantedBy=multi-user.target
`, execPath, configPath)

	if err := os.WriteFile(servicePath, []byte(serviceContent), 0o644); err != nil {
		return fmt.Errorf("write service file: %w", err)
	}
	return nil
}

func ensureDefaultFiles(configPath, fakeSiteDir, defaultConfigTOML string) error {
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	if err := os.MkdirAll(fakeSiteDir, 0o755); err != nil {
		return fmt.Errorf("create fake site directory: %w", err)
	}

	if _, err := os.Stat(configPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat default config path: %w", err)
	}

	if err := os.WriteFile(configPath, []byte(defaultConfigTOML), 0o644); err != nil {
		return fmt.Errorf("write default config: %w", err)
	}
	fmt.Printf("  OK default config written to %s\n", configPath)
	return nil
}
