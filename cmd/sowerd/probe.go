package main

import (
	"bytes"
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

func httpRequestLine(buf []byte) (line []byte, complete bool) {
	i := bytes.IndexByte(buf, '\n')
	if i < 0 {
		return buf, false
	}
	line = buf[:i]
	if len(line) > 0 && line[len(line)-1] == '\r' {
		line = line[:len(line)-1]
	}
	return line, true
}

func httpRequestLineProbe(buf []byte) bool {
	line, _ := httpRequestLine(buf)
	method, rest, ok := bytes.Cut(line, []byte{' '})
	if !ok || !isExactHTTPMethod(method) {
		return false
	}
	target, version, ok := bytes.Cut(rest, []byte{' '})
	if !ok || len(target) == 0 {
		return false
	}
	version = bytes.TrimSuffix(version, []byte{'\r'})
	if bytes.IndexByte(version, ' ') >= 0 {
		return false
	}
	return string(version) == "HTTP/1.1" || string(version) == "HTTP/1.0"
}

func isExactHTTPMethod(m []byte) bool {
	switch string(m) {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "TRACE", "CONNECT":
		return true
	default:
		return false
	}
}

func isHTTPMethodPrefix(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	for _, method := range []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "TRACE", "CONNECT"} {
		if bytes.HasPrefix([]byte(method), b) {
			return true
		}
	}
	return false
}

func isHTTPVersionPrefix(b []byte) bool {
	b = bytes.TrimSuffix(b, []byte{'\r'})
	return bytes.HasPrefix([]byte("HTTP/1.1"), b) || bytes.HasPrefix([]byte("HTTP/1.0"), b)
}

func couldBeHTTPRequestLine(buf []byte) bool {
	line, complete := httpRequestLine(buf)
	if complete {
		return false
	}
	method, rest, hasSpace := bytes.Cut(line, []byte{' '})
	if !hasSpace {
		return isHTTPMethodPrefix(method)
	}
	if !isExactHTTPMethod(method) {
		return false
	}
	_, version, hasVersion := bytes.Cut(rest, []byte{' '})
	if !hasVersion {
		return true
	}
	if bytes.IndexByte(version, ' ') >= 0 {
		return false
	}
	return isHTTPVersionPrefix(version)
}

func extendHTTPRequestProbe(conn net.Conn, probe []byte) []byte {
	if httpRequestLineProbe(probe) || !couldBeHTTPRequestLine(probe) {
		return probe
	}
	buf := make([]byte, protocolProbeMaxBytes)
	n := copy(buf, probe)
	for n < protocolProbeMaxBytes {
		nr, err := conn.Read(buf[n:])
		if nr > 0 {
			n += nr
			if httpRequestLineProbe(buf[:n]) || !couldBeHTTPRequestLine(buf[:n]) {
				return buf[:n]
			}
			continue
		}
		if err != nil {
			return buf[:n]
		}
	}
	return buf[:n]
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
