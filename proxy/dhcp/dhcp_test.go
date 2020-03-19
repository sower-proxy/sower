package dhcp_test

import (
	"fmt"

	"github.com/wweir/sower/proxy/dhcp"
)

func Example_dns() {
	got, err := dhcp.GetDefaultDNSServer()
	if err != nil {
		panic(err)
	}
	fmt.Println(got)
}
