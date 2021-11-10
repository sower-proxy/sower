package transport

import (
	"net"
	"strings"
	"testing"

	"github.com/wweir/deferlog/log"
	"github.com/wweir/sower/pkg/teeconn"
	"github.com/wweir/sower/transport/sower"
	"github.com/wweir/sower/transport/trojan"
)

func init() {
	log.Logger = log.Logger.With().Caller().Logger()
}

func testPipe(tran Transport) (net.Addr, error) {
	r, w := net.Pipe()
	defer r.Close()

	go func(w net.Conn) {
		defer w.Close()
		tran.Wrap(w, "sower", 443)
	}(w)

	return tran.Unwrap(teeconn.New(r))
}

func Test_Transports(t *testing.T) {
	if addr, err := testPipe(newSower()); err != nil || strings.TrimSpace(addr.String()) != "sower:443" {
		t.Errorf("test sower, unexpected address: %s, err: %s", addr, err)
	}

	if addr, err := testPipe(newTrojan()); err != nil || strings.TrimSpace(addr.String()) != "sower:443" {
		t.Errorf("test trojan, unexpected address: %s, err: %s", addr, err)
	}
}

func newSower() *sower.Sower {
	return sower.New("123")
}

func newTrojan() *trojan.Trojan {
	return trojan.New("123")
}
