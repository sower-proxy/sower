// +build !windows

package conf

import (
	"flag"
	"os"
	"path/filepath"
)

func initArgs() {
	flag.StringVar(&Conf.ConfigFile, "f", filepath.Dir(os.Args[0])+"/sower.toml", "config file location")
	flag.StringVar(&Conf.NetType, "n", "TCP", "proxy net type (QUIC|KCP|TCP)")
	flag.StringVar(&Conf.Cipher, "C", "AES_128_GCM", "cipher type (AES_128_GCM|AES_192_GCM|AES_256_GCM|CHACHA20_IETF_POLY1305|XCHACHA20_IETF_POLY1305)")
	flag.StringVar(&Conf.Password, "p", "12345678", "password")
	flag.StringVar(&Conf.ServerPort, "P", "5533", "server mode listen port")
	flag.StringVar(&Conf.ServerAddr, "s", "", "server IP (run in client mode if set)")
	flag.StringVar(&Conf.HTTPProxy, "H", "", "http proxy listen addr")
	flag.StringVar(&Conf.DNSServer, "d", "114.114.114.114", "client dns server")
	flag.StringVar(&Conf.ClientIP, "c", "127.0.0.1", "client dns service redirect IP")

	if !flag.Parsed() {
		flag.Set("logtostderr", "true")
		flag.Parse()
	}
}
