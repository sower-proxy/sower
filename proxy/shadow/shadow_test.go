package shadow

import (
	"net"
	"testing"
)

func TestShadow(t *testing.T) {
	c1, c2 := net.Pipe()

	go func() {
		conn := Shadow(c1, "AES_128_GCM", "12345678")
		conn.Write([]byte{1, 2})
	}()

	conn := Shadow(c2, "AES_128_GCM", "12345678")
	buf := make([]byte, 3)
	n, _ := conn.Read(buf)
	if n!=2|| buf[0] != 1 || buf[1] != 2  {
		t.Error(buf)
	}
}
