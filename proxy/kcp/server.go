package kcp

import (
	"net"

	"github.com/golang/glog"
	"github.com/pkg/errors"
	kcp "github.com/xtaci/kcp-go"
)

type server struct {
	Password    []byte
	DataShard   int
	ParityShard int
	DSCP        int
	SockBuf     int
}

func NewServer(password string) *server {
	return &server{
		Password:    fillPassword(password),
		DataShard:   10,
		ParityShard: 3,
		DSCP:        0,
		SockBuf:     4194304,
	}
}

func (s *server) Listen(port string) (<-chan net.Conn, error) {
	block, _ := kcp.NewAESBlockCrypt(s.Password)
	ln, err := kcp.ListenWithOptions(port, block, s.DataShard, s.ParityShard)
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
				glog.Errorln(err)
				continue
			}

			connCh <- conn
		}
	}()

	return connCh, nil
}
