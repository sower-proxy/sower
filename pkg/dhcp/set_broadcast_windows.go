//go:build windows

package dhcp

import "golang.org/x/sys/windows"

// setBroadcast enables SO_BROADCAST; Winsock requires it for broadcasts.
func setBroadcast(fd int) error {
	return windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_BROADCAST, 1)
}
