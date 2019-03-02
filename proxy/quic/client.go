package quic

import (
	"crypto/tls"
	"net"
	"time"

	quic "github.com/lucas-clemente/quic-go"
	"github.com/pkg/errors"
	"github.com/wweir/sower/util"
)

type client struct {
	conf *quic.Config
	sess quic.Session
}

func NewClient() *client {
	return &client{
		conf: &quic.Config{
			HandshakeTimeout: time.Second,
			KeepAlive:        true,
			IdleTimeout:      time.Minute,
		},
	}
}

func (c *client) Dial(server string) (net.Conn, error) {
	if c.sess == nil {
		if sess, err := quic.DialAddr(server, &tls.Config{InsecureSkipVerify: true}, c.conf); err != nil {
			return nil, errors.Wrap(err, "session")
		} else {
			go func() {
				<-sess.Context().Done()
				sess.Close()
				c.sess = nil
			}()
			c.sess = sess
		}
	}

	var stream quic.Stream
	if err := util.WithTimeout(func() (err error) {
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
