package dns

import "testing"

func TestGetDefaultDNSServer(t *testing.T) {
	t.Skip("for net env")
	tests := []struct {
		name string
		want string
	}{{
		"",
		"192.168.1.1",
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GetDefaultDNSServer(); got != tt.want {
				t.Errorf("GetDefaultDNSServer() = %v, want %v", got, tt.want)
			}
		})
	}
}
