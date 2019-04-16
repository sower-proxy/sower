package util

import (
	"runtime"
	"testing"
)

func TestPickInterface(t *testing.T) {
	switch runtime.GOOS {
	case "windows":
	case "darwin":
	default:
		t.Skip("skip for some enviroment not have net interface")
		return
	}

	got, err := PickInterface()
	if err != nil {
		t.Errorf("PickInterface() error = %v", err)
	} else {
		t.Logf("PickInterface() got: MAC: %s IP: %s", got.HardwareAddr, got.IP)
	}
}
