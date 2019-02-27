package dns

import (
	"math/rand"
	"net"
	"runtime"

	"github.com/golang/glog"
	"go.universe.tf/netboot/dhcp4"
)

var xid = make([]byte, 4)

func GetDefaultDNSServer() string {
	pack := &dhcp4.Packet{
		Type:      dhcp4.MsgDiscover,
		Broadcast: true,
	}
	options := map[dhcp4.Option][]byte{
		dhcp4.OptRequestedOptions: []byte{byte(dhcp4.OptDNSServers)},
	}

	NewConn := dhcp4.NewConn
	if runtime.GOOS == "linux" {
		NewConn = dhcp4.NewSnooperConn
	}

	ifaces := mustGetInterfaces()
	for _, iface := range ifaces {
		conn, err := NewConn(iface.IP.String() + ":68")
		if err != nil { // maybe in use
			glog.Errorln(err)
			continue
		}
		defer conn.Close()

		rand.Read(xid)
		pack.TransactionID = xid
		pack.HardwareAddr = iface.Interface.HardwareAddr
		options[dhcp4.OptClientIdentifier] = iface.Interface.HardwareAddr
		pack.Options = dhcp4.Options(options)

		if err := conn.SendDHCP(pack, iface.Interface); err != nil {
			glog.Errorln(err)
			continue
		}

		pack, _, err = conn.RecvDHCP()
		if err != nil {
			glog.Errorln(err)
			continue
		}

		ips, err := pack.Options.IPs(dhcp4.OptDNSServers)
		if err != nil {
			glog.Errorln(err)
			continue
		}
		return ips[0].String() // if len(ips) == 0, err should not be wrong size
	}
	return ""
}

type netIface struct {
	*net.Interface
	net.Addr
	net.IP
}

func mustGetInterfaces() []*netIface {
	ifaces, err := net.Interfaces()
	if err != nil {
		glog.Fatalln(err)
	}

	v4Iface := make([]*netIface, 0, len(ifaces))
	for i := range ifaces {
		if len(ifaces[i].HardwareAddr) == 0 {
			continue
		}

		addrs, _ := ifaces[i].Addrs()
		for _, addr := range addrs {
			if ip := addr.(*net.IPNet).IP.To4(); ip != nil {
				v4Iface = append(v4Iface, &netIface{&ifaces[i], addr, ip})
				break
			}
		}
	}
	return v4Iface
}
