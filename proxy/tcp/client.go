package tcp

import "net"

type client struct {
}

func NewClient() *client {
	return &client{}
}

func (c *client) Dial(server string) (net.Conn, error) {
	conn, err := net.Dial("tcp", server)
	if err != nil {
		return nil, err
	}

	conn.(*net.TCPConn).SetKeepAlive(true)
	return conn, nil
}
