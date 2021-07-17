package transport

import (
	"net"
	"strings"
	"testing"

	"github.com/rs/zerolog/log"
	"github.com/wweir/sower/pkg/teeconn"
	"github.com/wweir/sower/transport/sower"
	"github.com/wweir/sower/transport/trojan"
)

func init() {
	log.Logger = log.Logger.With().Caller().Logger()
}

func testPipe(tran Transport) net.Addr {
	r, w := net.Pipe()
	defer r.Close()

	go func(w net.Conn) {
		defer w.Close()
		tran.Wrap(w, "sower", 443)
	}(w)

	return tran.Unwrap(teeconn.New(r))
}

func Test_Transports(t *testing.T) {
	if addr := testPipe(newSower()); addr == nil || strings.TrimSpace(addr.String()) != "sower:443" {
		t.Errorf("test sower, unexpected address: %s", addr)
	}

	if addr := testPipe(newTrojan()); addr == nil || strings.TrimSpace(addr.String()) != "sower:443" {
		t.Errorf("test trojan, unexpected address: %s", addr)
	}
}

func newSower() *sower.Sower {
	return sower.New("123")
}

func newTrojan() *trojan.Trojan {
	return trojan.New("123")
}
