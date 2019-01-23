package util

import (
	"io"
	"net"
	"strings"
	"time"
)

// HTTPPing try connect to a http server with domain though the http addr

func HTTPPing(tcpAddr, domain string, timeout time.Duration) <-chan error {
	errCh := make(chan error)
	go func() {
		errCh <- httpPing(tcpAddr, domain, timeout)
	}()
	return errCh
}

func httpPing(tcpAddr, domain string, timeout time.Duration) error {
	conn, err := net.DialTimeout("tcp", tcpAddr, timeout)
	if err != nil {
		return err
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(timeout))
	if _, err = conn.Write(
		[]byte("TRACE / HTTP/1.1\r\nHost: " + domain + "\r\n\r\n")); err != nil {
		return err
	}

	// err -> nil:		read something succ
	// err -> io.EOF:	no such domain or connection refused
	// err -> timeout:	tcp package has been dropped
	_, err = conn.Read(make([]byte, 1))
	if err == io.EOF {
		idx := strings.Index(tcpAddr, ":")
		if idx >= 0 && tcpAddr[:idx] == domain {
			return nil
		}
	}
	return err
}
