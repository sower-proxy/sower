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
	n, err := conn.Read(buf)
	if n > 0 {
		return buf[:n], nil
	}
	if err != nil {
		return nil, fmt.Errorf("read probe bytes: %w", err)
	}
	return nil, fmt.Errorf("read probe bytes: empty read")
}
