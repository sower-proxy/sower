package net_test

import (
	"fmt"

	"github.com/wweir/sower/internal/net"
)

func Example_dns() {
	got, err := net.GetDefaultDNSServer()
	if err != nil {
		panic(err)
	}
	fmt.Println(got)
}
