//go:build !windows

package dhcp

import "golang.org/x/sys/unix"

// setBroadcast enables SO_BROADCAST so the DHCP discover broadcast to
// 255.255.255.255 is delivered on stacks that require the explicit option.
func setBroadcast(fd int) error {
	return unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_BROADCAST, 1)
}
