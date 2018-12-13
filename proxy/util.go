package proxy

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang/glog"
	"github.com/lucas-clemente/quic-go"
)

var dialConf = &quic.Config{
	HandshakeTimeout:   5 * time.Second,
	MaxIncomingStreams: 1024,
	KeepAlive:          true,
}

type streamConn struct {
	quic.Stream
	sess quic.Session
}

func (s *streamConn) LocalAddr() net.Addr {
	return s.sess.LocalAddr()
}

func (s *streamConn) RemoteAddr() net.Addr {
	return s.sess.RemoteAddr()
}

func relay(sess quic.Session, conn1, conn2 net.Conn) {
	wg := &sync.WaitGroup{}
	exitFlag := new(int32)
	wg.Add(2)
	go redirect(sess, conn1, conn2, wg, exitFlag)
	redirect(sess, conn2, conn1, wg, exitFlag)
	wg.Wait()
}

func redirect(sess quic.Session, conn1, conn2 net.Conn, wg *sync.WaitGroup, exitFlag *int32) {
	if _, err := io.Copy(conn2, conn1); err != nil && (atomic.LoadInt32(exitFlag) == 0) {
		glog.V(1).Infof("%s<>%s -> %s<>%s: %s", conn1.RemoteAddr(), conn1.LocalAddr(), conn2.LocalAddr(), conn2.RemoteAddr(), err)

		if strings.Contains(err.Error(), "PeerGoingAway") { //for internal package, hard code here
			sess.Close()
		}
	}

	// wakeup all conn goroutine
	atomic.AddInt32(exitFlag, 1)
	now := time.Now()
	conn1.SetDeadline(now)
	conn2.SetDeadline(now)
	wg.Done()
}

func mockTlsPem() *tls.Config {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		glog.Fatalln(err)
	}
	template := x509.Certificate{SerialNumber: big.NewInt(1)}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		glog.Fatalln(err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		glog.Fatalln(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{tlsCert}}
}
