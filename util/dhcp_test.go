package util

import "testing"

func TestGetDefaultDNSServer(t *testing.T) {
	t.Skip("skip for some enviroment not support dhcp")

	tests := []struct {
		name string
	}{{
		"",
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetDefaultDNSServer(); got == "" {
				t.Errorf("GetDefaultDNSServer() return empty")
			}
		})
	}
}
