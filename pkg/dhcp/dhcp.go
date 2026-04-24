package dhcp

import (
	"crypto/rand"
	"fmt"
	"net"
	"runtime"
	"time"

	"errors"
	"github.com/krolaw/dhcp4"
	"github.com/libp2p/go-reuseport"
)

var broadcastAddr, _ = net.ResolveUDPAddr("udp", "255.255.255.255:67")

func GetDNSServer() ([]string, error) {
	iface, err := PickInternetInterface()
	if err != nil {
		return nil, fmt.Errorf("pick interface: %w", err)
	}

	xid := make([]byte, 4)
	if _, err := rand.Read(xid); err != nil {
		return nil, fmt.Errorf("generate xid: %w", err)
	}
	pack := dhcp4.RequestPacket(dhcp4.Discover, iface.HardwareAddr, net.IPv4(0, 0, 0, 0), xid, true, []dhcp4.Option{
		{Code: dhcp4.OptionRequestedIPAddress, Value: []byte(iface.IP.To4())},
		{Code: dhcp4.End},
	})

	var conn net.PacketConn
	if runtime.GOOS == "windows" {
		if conn, err = reuseport.ListenPacket("udp4", iface.IP.String()+":68"); err != nil {
			return nil, fmt.Errorf("listen dhcp: %w", err)
		}
	} else {
		if conn, err = reuseport.ListenPacket("udp4", "0.0.0.0:68"); err != nil {
			return nil, fmt.Errorf("listen dhcp: %w", err)
		}
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))

	if _, err := conn.WriteTo([]byte(pack), broadcastAddr); err != nil {
		return nil, fmt.Errorf("write broadcast: %w", err)
	}

	buf := make([]byte, 1500 /*MTU*/)
	n, _, err := conn.ReadFrom(buf)
	if err != nil {
		return nil, fmt.Errorf("read dhcp offer: %w", err)
	}

	pack = dhcp4.Packet(buf[:n])
	dnsBytes := pack.ParseOptions()[dhcp4.OptionDomainNameServer]
	if len(dnsBytes) < 4 || len(dnsBytes)%4 != 0 {
		return nil, errors.New("invalid DNS setting in upstream network device")
	}

	ips := []string{}
	for i := 0; i < len(dnsBytes); i += 4 {
		ips = append(ips, net.IP(dnsBytes[i:i+4]).String())
	}

	return ips, nil
}
