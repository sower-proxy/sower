package transport

import (
	"net"

	"github.com/pkg/errors"
)

type Transport interface {
	Dial(server string) (net.Conn, error)
	Listen(port string) (<-chan net.Conn, error)
}

var transports = map[string]Transport{}

func ListTransports() []string {
	list := make([]string, 0, len(transports))
	for key := range transports {
		list = append(list, key)
	}
	return list
}

func GetTransport(netType string) (Transport, error) {
	tran, ok := transports[netType]
	if !ok {
		return nil, errors.New("invalid net type: " + netType)
	}
	return tran, nil
}
