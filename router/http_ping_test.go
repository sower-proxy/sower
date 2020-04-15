// +build debug
// go test -v -tags debug

package router_test

import (
	"net"
	"testing"
	"time"

	"github.com/wweir/sower/router"
	"github.com/wweir/sower/transport"
)

func TestPort_Ping(t *testing.T) {
	type args struct {
		domain  string
		timeout time.Duration
	}
	tests := []struct {
		name    string
		p       router.Port
		args    args
		wantErr bool
	}{
		{
			"baidu_80",
			router.HTTP,
			args{"baidu.com", time.Second},
			false,
		},
		{
			"baidu_443",
			router.HTTPS,
			args{"baidu.com", time.Second},
			false,
		},
		{
			"google_80",
			router.HTTP,
			args{"google.com", time.Second},
			true,
		},
		{
			"google_443",
			router.HTTPS,
			args{"google.com", time.Second},
			true,
		},
		{
			"mail_80",
			router.HTTP,
			args{"smtp.163.com", time.Second},
			true,
		},
		{
			"mail_443",
			router.HTTPS,
			args{"smtp.163.com", time.Second},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.p.Ping(tt.args.domain, tt.args.timeout); (err != nil) != tt.wantErr {
				t.Errorf("Port.Ping() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

var (
	proxyAddr string = ""
	password  []byte = []byte("")
)

func TestPort_PingWithConn(t *testing.T) {
	conn, err := transport.Dial(":443", func(string) (string, []byte) {
		return proxyAddr, password
	})
	if err != nil {
		t.Errorf("dial remote %s", err)
	}

	type args struct {
		domain  string
		conn    net.Conn
		timeout time.Duration
	}
	tests := []struct {
		name    string
		p       router.Port
		args    args
		wantErr bool
	}{
		{
			"google_80",
			router.HTTP,
			args{"google.com", conn, 3 * time.Second},
			false,
		},
		{
			"google_443",
			router.HTTPS,
			args{"baidu.com", conn, 3 * time.Second},
			false,
		},
		{
			"mail_80",
			router.HTTP,
			args{"smtp.163.com", conn, 3 * time.Second},
			true,
		},
		{
			"mail_443",
			router.HTTPS,
			args{"smtp.163.com", conn, 3 * time.Second},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.p.PingWithConn(tt.args.domain, tt.args.conn, tt.args.timeout); (err != nil) != tt.wantErr {
				t.Errorf("Port.PingWithConn() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
