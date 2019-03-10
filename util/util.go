package util

import (
	"errors"
	"net"
	"time"
)

// Iface is net interface address info
type Iface struct {
	net.HardwareAddr
	net.IP
}

func WithTimeout(fn func() error, timeout time.Duration) error {
	var okCh = make(chan struct{})
	var err error

	go func() {
		err = fn()
		close(okCh)
	}()

	select {
	case <-okCh:
		return err
	case <-time.After(timeout):
		return errors.New("timeout: " + timeout.String())
	}
}
