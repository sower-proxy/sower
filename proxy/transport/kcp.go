package transport

import (
	"net"

	"github.com/golang/glog"
	"github.com/pkg/errors"
	kcp "github.com/xtaci/kcp-go"
)

type kcpTran struct {
	client
	server
}
type client struct {
	DataShard    int
	ParityShard  int
	DSCP         int
	SockBuf      int
	AckNodelay   bool
	NoDelay      int
	Interval     int
	Resend       int
	NoCongestion int
	SndWnd       int
	RcvWnd       int
	MTU          int
}
type server struct {
	DataShard   int
	ParityShard int
	DSCP        int
	SockBuf     int
}

func init() {
	transports["KCP"] = &kcpTran{
		client: client{
			DataShard:    10,
			ParityShard:  3,
			DSCP:         0,
			SockBuf:      4194304,
			NoDelay:      0,
			Interval:     50,
			Resend:       0,
			NoCongestion: 0,
			SndWnd:       0,
			RcvWnd:       0,
			MTU:          1350,
		},
		server: server{
			DataShard:   10,
			ParityShard: 3,
			DSCP:        0,
			SockBuf:     4194304,
		},
	}
}

func (c *client) Dial(server string) (net.Conn, error) {
	conn, err := kcp.DialWithOptions(server, nil, c.DataShard, c.ParityShard)
	if err != nil {
		return nil, errors.Wrap(err, "dial")
	}

	conn.SetStreamMode(true)
	conn.SetWriteDelay(false)
	conn.SetNoDelay(c.NoDelay, c.Interval, c.Resend, c.NoCongestion)
	conn.SetWindowSize(c.SndWnd, c.RcvWnd)
	conn.SetMtu(c.MTU)
	conn.SetACKNoDelay(c.AckNodelay)

	if err := conn.SetDSCP(c.DSCP); err != nil {
		return nil, errors.Wrap(err, "SetDSCP")
	}
	if err := conn.SetReadBuffer(c.SockBuf); err != nil {
		return nil, errors.Wrap(err, "SetReadBuffer")
	}
	if err := conn.SetWriteBuffer(c.SockBuf); err != nil {
		return nil, errors.Wrap(err, "SetWriteBuffer")
	}

	return conn, nil
}

func (s *server) Listen(port string) (<-chan net.Conn, error) {
	ln, err := kcp.ListenWithOptions(port, nil, s.DataShard, s.ParityShard)
	if err != nil {
		return nil, err
	}

	if err := ln.SetDSCP(s.DSCP); err != nil {
		return nil, errors.Wrap(err, "SetDSCP")
	}
	if err := ln.SetReadBuffer(s.SockBuf); err != nil {
		return nil, errors.Wrap(err, "SetReadBuffer")
	}
	if err := ln.SetWriteBuffer(s.SockBuf); err != nil {
		return nil, errors.Wrap(err, "SetWriteBuffer")
	}

	connCh := make(chan net.Conn)
	go func() {
		for {
			conn, err := ln.AcceptKCP()
			if err != nil {
				glog.Fatalln("KCP listen:", err)
			}

			connCh <- conn
		}
	}()

	return connCh, nil
}
