// +build debug
// go test -v -tags debug

package router_test

import (
	"crypto/tls"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/wweir/sower/router"
	"github.com/wweir/sower/transport"
)

var (
	proxyAddr string = "socks5://127.0.0.1:1080"
	password  []byte = nil
)

func TestPort_Ping(t *testing.T) {
	direct := func(addr string) (net.Conn, error) {
		return net.DialTimeout("tcp", addr, 5*time.Second)
	}
	proxy := func(addr string) (net.Conn, error) {
		conn, err := tls.Dial("tcp", proxyAddr, &tls.Config{})
		if err != nil {
			return nil, err
		}
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		p, err := strconv.Atoi(port)
		if err != nil {
			return nil, err
		}
		return transport.ToTrojanConn(conn, host, uint16(p), password)
	}

	type args struct {
		domain string
		dial   func(string) (net.Conn, error)
	}
	tests := []struct {
		name    string
		p       router.Port
		args    args
		wantErr bool
	}{
		{"", router.HTTP, args{"baidu.com", direct}, false},
		{"", router.HTTP, args{"baidu.com", proxy}, false},
		{"", router.HTTPS, args{"baidu.com", direct}, false},
		{"", router.HTTPS, args{"baidu.com", proxy}, false},
		{"", router.HTTP, args{"google.com", direct}, true},
		{"", router.HTTP, args{"google.com", proxy}, false},
		{"", router.HTTPS, args{"google.com", direct}, true},
		{"", router.HTTPS, args{"google.com", proxy}, false},
		{"", router.HTTP, args{"smtp.163.com", direct}, true},
		{"", router.HTTP, args{"smtp.163.com", proxy}, true},
		{"", router.HTTPS, args{"smtp.163.com", direct}, true},
		{"", router.HTTPS, args{"smtp.163.com", proxy}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.p.Ping(tt.args.domain, tt.args.dial); (err != nil) != tt.wantErr {
				t.Errorf("Port.Ping() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
