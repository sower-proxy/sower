package install

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestInstallServiceRequiresRoot(t *testing.T) {
	t.Parallel()

	err := installService(installOptions{
		geteuid: func() int { return 1000 },
	})
	if err == nil || !strings.Contains(err.Error(), "root privileges") {
		t.Fatalf("installService() error = %v, want root privileges error", err)
	}
}

func TestInstallServiceFreshInstall(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	execPath := filepath.Join(tmpDir, "sowerd")
	if err := os.WriteFile(execPath, []byte("binary"), 0o755); err != nil {
		t.Fatalf("write executable: %v", err)
	}

	targetBinaryPath := filepath.Join(tmpDir, "usr", "local", "bin", "sowerd")
	servicePath := filepath.Join(tmpDir, "etc", "systemd", "system", "sowerd.service")
	configPath := filepath.Join(tmpDir, "etc", "sower", "sowerd.toml")
	fakeSiteDir := filepath.Join(tmpDir, "var", "www")

	var prompts []string
	var systemctlCalls []string
	confirmAnswers := map[string]bool{
		"Copy binary to " + targetBinaryPath + "?": true,
		"Start and enable sowerd service now?":     true,
	}

	err := installService(installOptions{
		confirm: func(label string) bool {
			prompts = append(prompts, label)
			return confirmAnswers[label]
		},
		geteuid:         func() int { return 0 },
		executable:      func() (string, error) { return execPath, nil },
		evalSymlinks:    func(path string) (string, error) { return path, nil },
		serviceIsActive: func() bool { return false },
		systemctl: func(args ...string) error {
			systemctlCalls = append(systemctlCalls, strings.Join(args, " "))
			return nil
		},
		targetBinaryPath:  targetBinaryPath,
		servicePath:       servicePath,
		configPath:        configPath,
		defaultFakeSite:   fakeSiteDir,
		defaultConfigTOML: `password = "change_me"` + "\n" + `fakeSite = "/var/www"` + "\n",
	})
	if err != nil {
		t.Fatalf("installService() error = %v", err)
	}

	gotBinary, err := os.ReadFile(targetBinaryPath)
	if err != nil {
		t.Fatalf("read target binary: %v", err)
	}
	if string(gotBinary) != "binary" {
		t.Fatalf("target binary = %q, want %q", string(gotBinary), "binary")
	}

	serviceContent, err := os.ReadFile(servicePath)
	if err != nil {
		t.Fatalf("read service file: %v", err)
	}
	serviceText := string(serviceContent)
	if !strings.Contains(serviceText, "ExecStart="+targetBinaryPath+" -c "+configPath) {
		t.Fatalf("service file missing ExecStart, content = %s", serviceText)
	}

	configContent, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}
	if string(configContent) == "" {
		t.Fatal("config file is empty")
	}

	if info, err := os.Stat(fakeSiteDir); err != nil || !info.IsDir() {
		t.Fatalf("fake site dir stat err = %v, isDir = %v", err, err == nil && info.IsDir())
	}

	wantPrompts := []string{
		"Copy binary to " + targetBinaryPath + "?",
		"Start and enable sowerd service now?",
	}
	if !reflect.DeepEqual(prompts, wantPrompts) {
		t.Fatalf("prompts = %#v, want %#v", prompts, wantPrompts)
	}

	wantSystemctlCalls := []string{
		"daemon-reload",
		"enable sowerd",
		"start sowerd",
	}
	if !reflect.DeepEqual(systemctlCalls, wantSystemctlCalls) {
		t.Fatalf("systemctl calls = %#v, want %#v", systemctlCalls, wantSystemctlCalls)
	}
}

func TestInstallServiceUpdateWithoutRestart(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	targetBinaryPath := filepath.Join(tmpDir, "usr", "local", "bin", "sowerd")
	if err := os.MkdirAll(filepath.Dir(targetBinaryPath), 0o755); err != nil {
		t.Fatalf("mkdir target binary dir: %v", err)
	}
	if err := os.WriteFile(targetBinaryPath, []byte("binary"), 0o755); err != nil {
		t.Fatalf("write target binary: %v", err)
	}

	servicePath := filepath.Join(tmpDir, "etc", "systemd", "system", "sowerd.service")
	configPath := filepath.Join(tmpDir, "etc", "sower", "sowerd.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("password = \"existing\"\n"), 0o644); err != nil {
		t.Fatalf("write existing config: %v", err)
	}

	var prompts []string
	var systemctlCalls []string

	err := installService(installOptions{
		confirm: func(label string) bool {
			prompts = append(prompts, label)
			return false
		},
		geteuid:         func() int { return 0 },
		executable:      func() (string, error) { return targetBinaryPath, nil },
		evalSymlinks:    func(path string) (string, error) { return path, nil },
		serviceIsActive: func() bool { return true },
		systemctl: func(args ...string) error {
			systemctlCalls = append(systemctlCalls, strings.Join(args, " "))
			return nil
		},
		targetBinaryPath:  targetBinaryPath,
		servicePath:       servicePath,
		configPath:        configPath,
		defaultFakeSite:   filepath.Join(tmpDir, "var", "www"),
		defaultConfigTOML: "password = \"change_me\"\n",
	})
	if err != nil {
		t.Fatalf("installService() error = %v", err)
	}

	configContent, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}
	if string(configContent) != "password = \"existing\"\n" {
		t.Fatalf("config content = %q, want existing config to be preserved", string(configContent))
	}

	wantPrompts := []string{"Restart sowerd service now?"}
	if !reflect.DeepEqual(prompts, wantPrompts) {
		t.Fatalf("prompts = %#v, want %#v", prompts, wantPrompts)
	}

	wantSystemctlCalls := []string{"daemon-reload"}
	if !reflect.DeepEqual(systemctlCalls, wantSystemctlCalls) {
		t.Fatalf("systemctl calls = %#v, want %#v", systemctlCalls, wantSystemctlCalls)
	}
}
