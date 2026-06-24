package transport

import (
	"net"
	"testing"

	"github.com/sower-proxy/conns/reread"
	"github.com/sower-proxy/sower/transport/socks5"
	"github.com/sower-proxy/sower/transport/sower"
	"github.com/sower-proxy/sower/transport/trojan"
)

func testPipe(tran Transport) (net.Addr, error) {
	r, w := net.Pipe()
	defer r.Close()

	go func(w net.Conn) {
		defer w.Close()
		tran.Wrap(w, "sower", 443)
	}(w)

	return tran.Unwrap(reread.New(r))
}

// testPipeDirect is like testPipe but passes the raw conn to Unwrap
// (no reread wrapper), needed for transports where Unwrap writes back.
func testPipeDirect(tran Transport) (net.Addr, error) {
	r, w := net.Pipe()
	defer r.Close()

	go func(w net.Conn) {
		defer w.Close()
		tran.Wrap(w, "sower", 443)
	}(w)

	return tran.Unwrap(r)
}

func Test_Transports(t *testing.T) {
	if addr, err := testPipe(newSower()); err != nil || addr.String() != "sower:443" {
		t.Errorf("test sower, unexpected address: %s, err: %s", addr, err)
	}

	if addr, err := testPipe(newTrojan()); err != nil || addr.String() != "sower:443" {
		t.Errorf("test trojan, unexpected address: %s, err: %s", addr, err)
	}

	if addr, err := testPipeDirect(socks5.New()); err != nil || addr.String() != "sower:443" {
		t.Errorf("test socks5, unexpected address: %s, err: %s", addr, err)
	}
}

func newSower() *sower.Sower {
	return sower.New("123")
}

func newTrojan() *trojan.Trojan {
	return trojan.New("123")
}
