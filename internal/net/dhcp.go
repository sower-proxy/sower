package net

import (
	"math/rand"
	"net"
	"runtime"
	"time"

	"github.com/krolaw/dhcp4"
	"github.com/libp2p/go-reuseport"
	"github.com/pkg/errors"
)

var xid = make([]byte, 4)
var broadcastAddr, _ = net.ResolveUDPAddr("udp", "255.255.255.255:67")

func GetDefaultDNSServer() (string, error) {
	iface, err := PickInternetInterface()
	if err != nil {
		return "", errors.Wrap(err, "pick interface")
	}

	rand.Read(xid)
	pack := dhcp4.RequestPacket(dhcp4.Discover, iface.HardwareAddr, net.IPv4(0, 0, 0, 0), xid, true, []dhcp4.Option{
		{Code: dhcp4.OptionRequestedIPAddress, Value: []byte(iface.IP.To4())},
		{Code: dhcp4.End},
	})

	var conn net.PacketConn
	if runtime.GOOS == "windows" {
		if conn, err = reuseport.ListenPacket("udp4", iface.IP.String()+":68"); err != nil {
			return "", errors.Wrap(err, "listen dhcp")
		}
	} else {
		if conn, err = reuseport.ListenPacket("udp4", "0.0.0.0:68"); err != nil {
			return "", errors.Wrap(err, "listen dhcp")
		}
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))

	if _, err := conn.WriteTo([]byte(pack), broadcastAddr); err != nil {
		return "", errors.Wrap(err, "write broadcast")
	}

	buf := make([]byte, 1500 /*MTU*/)
	n, _, err := conn.ReadFrom(buf)
	if err != nil {
		return "", errors.Wrap(err, "read dhcp offer")
	}

	pack = dhcp4.Packet(buf[:n])
	dnsBytes := pack.ParseOptions()[dhcp4.OptionDomainNameServer]
	if len(dnsBytes) < 4 {
		return "", errors.New("no DNS setting in upstream network device")
	}

	return net.IPv4(dnsBytes[0], dnsBytes[1], dnsBytes[2], dnsBytes[3]).String(), nil
}
