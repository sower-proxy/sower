package dns

import (
	"runtime"
	"testing"
)

func TestGetDefaultDNSServer(t *testing.T) {
	switch runtime.GOOS {
	case "windows":
	case "darwin":
	default:
		t.Skip("skip for some enviroment not support dhcp and permission set")
		return
	}

	if got, err := GetDefaultDNSServer(); err != nil {
		t.Errorf("GetDefaultDNSServer() return error: %s", err)
	} else {
		t.Logf("GetDefaultDNSServer() return IP: %v", got)
	}
}
