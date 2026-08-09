//go:build windows

package main

import "errors"

// restartSupported reports whether the admin restart endpoint can work on
// this platform.
func restartSupported() bool { return false }

// restartCurrentProcess is unsupported on Windows: the admin restart
// endpoint reports the error and the process keeps running.
func restartCurrentProcess() error {
	return errors.New("process restart is unsupported on windows")
}
