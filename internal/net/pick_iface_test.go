package net_test

import (
	"fmt"

	"github.com/wweir/sower/internal/net"
)

func Example_iface() {
	got, err := net.PickInternetInterface()
	if err != nil {
		panic(err)
	}
	fmt.Println(got)
}
