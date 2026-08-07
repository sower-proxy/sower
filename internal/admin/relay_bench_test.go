package admin

import (
	"net"
	"testing"
)

// discardConn accepts and discards data without copying, isolating the
// accounting overhead from fake-conn buffer growth.
type discardConn struct {
	net.Conn
}

func (discardConn) Read(p []byte) (int, error)  { return len(p), nil }
func (discardConn) Write(p []byte) (int, error) { return len(p), nil }
func (discardConn) Close() error                { return nil }
func (discardConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345}
}
func (discardConn) LocalAddr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080} }

// BenchmarkCountingConnWrite measures the per-write accounting cost of the
// relay path: atomic counters, OTel byte recording, and per-domain
// attribution under the stats mutex.
func BenchmarkCountingConnWrite(b *testing.B) {
	s := newBenchmarkStats(b, 100)
	conn := s.WrapConn(discardConn{}, "https")
	s.BindConn(conn, "example.com")
	chunk := make([]byte, 4096)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := conn.Write(chunk); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCountingConnWriteChained measures one Write plus one Read, the
// full duplex relay unit of work.
func BenchmarkCountingConnWriteChained(b *testing.B) {
	s := newBenchmarkStats(b, 100)
	conn := s.WrapConn(discardConn{}, "http")
	s.BindConn(conn, "example.com")
	chunk := make([]byte, 4096)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := conn.Write(chunk); err != nil {
			b.Fatal(err)
		}
		if _, err := conn.Read(chunk); err != nil {
			b.Fatal(err)
		}
	}
}
