package main

import (
	"fmt"
	"net"
	"time"

	transportSower "github.com/sower-proxy/sower/transport/sower"
)

const (
	protocolProbeTimeout  = time.Second
	protocolProbeMaxBytes = 512
	// protocolHeaderTimeout bounds reading the transport header after a
	// probe match; the probe window itself is too short for a slow client.
	protocolHeaderTimeout = 5 * time.Second
	// tlsHandshakeTimeout bounds the explicit TLS handshake before probing;
	// ACME issuance on first contact can take tens of seconds.
	tlsHandshakeTimeout = 60 * time.Second
)

type probeVerdict int

const (
	probeNoMatch probeVerdict = iota
	probeNeedMore
	probeMatch
)

type proxyProtocolHandler interface {
	Name() string
	Probe(buf []byte) probeVerdict
	Unwrap(conn net.Conn) (net.Addr, error)
}

type sowerProtocolHandler struct {
	transport *transportSower.Sower
}

func newSowerProtocolHandler(transport *transportSower.Sower) sowerProtocolHandler {
	return sowerProtocolHandler{transport: transport}
}

func (h sowerProtocolHandler) Name() string { return "sower" }

func (h sowerProtocolHandler) Probe(buf []byte) probeVerdict {
	if len(buf) == 0 {
		return probeNeedMore
	}
	if buf[0] != 0x80 {
		return probeNoMatch
	}
	return probeMatch
}

func (h sowerProtocolHandler) Unwrap(conn net.Conn) (net.Addr, error) {
	return h.transport.Unwrap(conn)
}

func readProtocolProbe(conn net.Conn) ([]byte, error) {
	buf := make([]byte, protocolProbeMaxBytes)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			return buf[:n], nil
		}
		if err != nil {
			return nil, fmt.Errorf("read probe bytes: %w", err)
		}
		// n == 0 && err == nil is legal (though rare) on TCP; retry instead
		// of tearing down the connection.
	}
}
