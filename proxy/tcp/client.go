package tcp

import "net"

type client struct {
}

func NewClient() *client {
	return &client{}
}

func (c *client) Dial(server string) (net.Conn, error) {
	return net.Dial("tcp", server)
}
