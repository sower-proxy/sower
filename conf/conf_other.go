// +build !windows

package conf

import (
	"context"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/wweir/sower/dns"
	"github.com/wweir/sower/proxy/shadow"
	"github.com/wweir/sower/proxy/transport"
)

func initArgs() {
	flag.StringVar(&Conf.ConfigFile, "f", filepath.Dir(os.Args[0])+"/sower.toml", "config file location")
	flag.StringVar(&Conf.NetType, "n", "TCP", "net type (socks5 client only): "+strings.Join(transport.ListTransports(), ","))
	flag.StringVar(&Conf.Cipher, "C", "AES_128_GCM", "cipher type: "+strings.Join(shadow.ListCiphers(), ","))
	flag.StringVar(&Conf.Password, "p", "12345678", "password")
	flag.StringVar(&Conf.ServerPort, "P", "5533", "server mode listen port")
	flag.StringVar(&Conf.ServerAddr, "s", "", "server IP (run in CLIENT MODE if set)")
	flag.StringVar(&Conf.HTTPProxy, "H", "", "http proxy listen addr")
	flag.StringVar(&Conf.DNSServer, "d", "114.114.114.114", "client dns server")
	flag.StringVar(&Conf.ClientIP, "c", "127.0.0.1", "client dns service redirect IP")
	flag.StringVar(&Conf.SuggestLevel, "S", "SPEEDUP", "suggest level setting: "+strings.Join(dns.ListSuggestLevels(), ","))
	flag.BoolVar(&Conf.VersionOnly, "V", false, "print sower version")

	if !flag.Parsed() {
		flag.Set("logtostderr", "true")
		flag.Parse()
	}
}

func execute(cmd string) error {
	ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "sh", "-c", Conf.ClearDNSCache).CombinedOutput()
	return errors.Wrapf(err, "cmd: %s, output: %s, error", Conf.ClearDNSCache, out)
}
