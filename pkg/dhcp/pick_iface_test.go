package dhcp_test

import (
	"fmt"

	"github.com/sower-proxy/sower/pkg/dhcp"
)

func Example_iface() {
	got, err := dhcp.PickInternetInterface()
	if err != nil {
		panic(err)
	}
	fmt.Println(got)
}
