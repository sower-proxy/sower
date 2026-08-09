//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

// restartSupported reports whether the admin restart endpoint can work on
// this platform.
func restartSupported() bool { return true }

// restartCurrentProcess replaces the current process image in place, so the
// systemd unit keeps the same PID and Restart=on-failure is not triggered.
// All listeners must be closed before calling this: listener sockets carry
// CLOEXEC and would be closed by the exec anyway, but closing them first
// guarantees the addresses are released (no in-flight connections, no
// lingering TIME_WAIT on the listener) before the replacement binds them.
func restartCurrentProcess() error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("get executable: %w", err)
	}
	if err := syscall.Exec(executable, os.Args, os.Environ()); err != nil {
		return fmt.Errorf("exec %s: %w", executable, err)
	}
	return nil
}
