package tcp

import (
	"net"
	"time"
)

type client struct {
	DialTimeout time.Duration
}

func NewClient() *client {
	return &client{
		DialTimeout: 5 * time.Second,
	}
}

func (c *client) Dial(server string) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", server, c.DialTimeout)
	if err != nil {
		return nil, err
	}

	conn.(*net.TCPConn).SetKeepAlive(true)
	return conn, nil
}
