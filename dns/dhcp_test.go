package dns

import "testing"

func TestGetDefaultDNSServer(t *testing.T) {
	t.Skip("skip for some enviroment not support dhcp and permission")

	if got, err := GetDefaultDNSServer(); err != nil {
		t.Errorf("GetDefaultDNSServer() return error: %s", err)
	} else {
		t.Logf("GetDefaultDNSServer() return IP: %v", got)
	}
}
