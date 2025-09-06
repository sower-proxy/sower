package dhcp_test

import (
	"fmt"

	"github.com/sower-proxy/sower/pkg/dhcp"
)

func Example_dns() {
	got, err := dhcp.GetDNSServer()
	if err != nil {
		panic(err)
	}
	fmt.Println(got)
}
