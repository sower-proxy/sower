// +build windows

package util

import (
	"bytes"
	"errors"
	"net"
	"os"
	"syscall"
	"unsafe"
)

// PickInterface pick the first active net interface
func PickInterface() (*Iface, error) {
	list, err := getAdapterList()
	if err != nil {
		return nil, err
	}
	for {
		if list.DhcpEnabled == 1 && list.LeaseObtained != 0 {
			ip := bytes.TrimRight(list.IpAddressList.IpAddress.String[:], string([]byte{0}))
			return &Iface{net.HardwareAddr(list.Address[:list.AddressLength]), net.ParseIP(string(ip))}, nil
		}

		if list.Next != nil {
			list = list.Next
		} else {
			return nil, errors.New("no valid interface")
		}
	}
}

func getAdapterList() (*syscall.IpAdapterInfo, error) {
	b := make([]byte, 1000)
	l := uint32(len(b))
	a := (*syscall.IpAdapterInfo)(unsafe.Pointer(&b[0]))
	// TODO(mikio): GetAdaptersInfo returns IP_ADAPTER_INFO that
	// contains IPv4 address list only. We should use another API
	// for fetching IPv6 stuff from the kernel.
	err := syscall.GetAdaptersInfo(a, &l)
	if err == syscall.ERROR_BUFFER_OVERFLOW {
		b = make([]byte, l)
		a = (*syscall.IpAdapterInfo)(unsafe.Pointer(&b[0]))
		err = syscall.GetAdaptersInfo(a, &l)
	}
	if err != nil {
		return nil, os.NewSyscallError("GetAdaptersInfo", err)
	}
	return a, nil
}
