package quic

import (
	"crypto/tls"
	"net"
	"time"

	quic "github.com/lucas-clemente/quic-go"
	"github.com/pkg/errors"
)

type client struct {
	conf *quic.Config
	sess quic.Session
}

func NewClient() *client {
	return &client{
		// conf: &quic.Config{
		// 	HandshakeTimeout:   5 * time.Second,
		// 	MaxIncomingStreams: 1024,
		// 	KeepAlive:          true,
		// },
	}
}

func (c *client) Dial(server string) (net.Conn, error) {
	if c.sess == nil {
		if sess, err := quic.DialAddr(server, &tls.Config{InsecureSkipVerify: true}, c.conf); err != nil {
			return nil, errors.Wrap(err, "session")
		} else {
			c.sess = sess
		}
	}

	var stream quic.Stream
	if err := WithTimeout(func() (err error) {
		if stream, err = c.sess.OpenStream(); err != nil {
			c.sess = nil
		}
		return
	}, time.Second); err != nil {
		return nil, errors.Wrap(err, "stream")
	}

	return &streamConn{
		Stream: stream,
		sess:   c.sess,
	}, nil
}
