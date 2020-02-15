package http

import (
	"bufio"
	"bytes"
	"io/ioutil"
	"net"
	"net/http"
	"testing"
)

func TestParseAddr1(t *testing.T) {
	c1, c2 := net.Pipe()

	go func() {
		c1 = NewTgtConn(c1, nil, TGT_HTTP, "", 0)
		req, _ := http.NewRequest("GET", "http://wweir.cc", bytes.NewReader([]byte{1, 2, 3}))
		req.Write(c1)
	}()

	c2, host, port, err := ParseAddr(c2, nil)

	if err != nil || host != "wweir.cc" || port != 80 {
		t.Error(err, host, port)
	}

	req, err := http.ReadRequest(bufio.NewReader(c2))
	if err != nil {
		t.Error(err)
	}

	data, err := ioutil.ReadAll(req.Body)
	if err != nil || len(data) != 3 || data[0] != 1 {
		t.Error(err, data)
	}
}

func TestParseAddr2(t *testing.T) {
	c1, c2 := net.Pipe()

	go func() {
		c1 = NewTgtConn(c1, nil, TGT_HTTPS, "", 443)
		c1.Write(HTTPS.PingMsg("wweir.cc"))
	}()

	_, host, port, err := ParseAddr(c2, nil)

	if err != nil || host != "wweir.cc" || port != 443 {
		t.Error(err, host, port)
	}
}

func TestParseAddr3(t *testing.T) {
	c1, c2 := net.Pipe()

	go func() {
		c1 = NewTgtConn(c1, nil, TGT_OTHER, "wweir.cc", 1080)
		c1.Write(HTTPS.PingMsg("wweir.cc"))
	}()

	_, host, port, err := ParseAddr(c2, nil)

	if err != nil || host != "wweir.cc" || port != 1080 {
		t.Error(err, host, port)
	}
}
