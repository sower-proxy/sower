package util

import (
	"testing"
)

func TestPickInterface(t *testing.T) {
	t.Skip("skip for some enviroment not have net interface")

	got, err := PickInterface()
	if err != nil {
		t.Errorf("PickInterface() error = %v", err)
	} else {
		t.Logf("PickInterface() got: MAC: %s IP: %s", got.HardwareAddr, got.IP)
	}
}
