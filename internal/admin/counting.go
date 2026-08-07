package admin

import (
	"net"
	"sync/atomic"
	"time"
)

// countingConn wraps a client connection and feeds byte counts into Stats.
type countingConn struct {
	net.Conn
	stats    *Stats
	kind     string
	clientIP string
	created  time.Time
	domain   atomic.Value // string; empty until BindConn
	closed   atomic.Bool

	// Per-direction pending byte batches. Read and Write run on different
	// relay goroutines, so each direction is owned by one goroutine and the
	// atomics below are uncontended; Close swaps them safely under I/O.
	pendingUp     atomic.Uint64
	pendingDown   atomic.Uint64
	nextFlushUp   atomic.Int64 // unix nanos, upload flush due
	nextFlushDown atomic.Int64 // unix nanos, download flush due
}

// flushBytes and flushInterval bound how long per-domain byte attribution
// may lag: throughput connections flush every flushBytes, slow trickles at
// least every flushInterval, and Close always drains the remainder. The
// global BytesUp/BytesDown totals stay real-time via direct atomics; only
// per-domain attribution and the OTel byte counter are batched.
const (
	flushBytes    = 32 << 10
	flushInterval = 500 * time.Millisecond
)

func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.stats.bytesUp.Add(uint64(n))
		c.addBytes(true, uint64(n))
	}
	return n, err
}

func (c *countingConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.stats.bytesDown.Add(uint64(n))
		c.addBytes(false, uint64(n))
	}
	return n, err
}

// Close releases the active-connection slot exactly once and records the
// connection duration. The underlying conn is closed on every call, matching
// net.Conn semantics.
func (c *countingConn) Close() error {
	if c.closed.CompareAndSwap(false, true) {
		switch c.kind {
		case "http":
			c.stats.activeHTTP.Add(^uint64(0))
		case "https":
			c.stats.activeHTTPS.Add(^uint64(0))
		case "socks5":
			c.stats.activeSocks.Add(^uint64(0))
		}
		c.stats.metrics.ConnClosed(c.kind)
		c.stats.metrics.RecordConnDuration(time.Since(c.created))
		c.flush(true, c.pendingUp.Swap(0))
		c.flush(false, c.pendingDown.Swap(0))
	}
	return c.Conn.Close()
}

func (c *countingConn) bind(domain string) {
	c.domain.Store(domain)
}

// addBytes accumulates payload bytes per direction and flushes the batch
// when it reaches flushBytes or flushInterval has elapsed. The first I/O on
// a fresh connection flushes immediately (nextFlush starts zero), so short
// connections attribute their bytes right away.
func (c *countingConn) addBytes(up bool, n uint64) {
	var pending *atomic.Uint64
	var due *atomic.Int64
	if up {
		pending, due = &c.pendingUp, &c.nextFlushUp
	} else {
		pending, due = &c.pendingDown, &c.nextFlushDown
	}

	acc := pending.Add(n)
	now := time.Now().UnixNano()
	if acc < flushBytes && now < due.Load() && !c.closed.Load() {
		return
	}

	due.Store(now + int64(flushInterval))
	c.flush(up, pending.Swap(0))
}

// flush attributes one direction's pending bytes to the OTel byte counter
// and, when the domain is known, to the bound domain. The OTel counter is
// recorded unconditionally: protocol bytes read before BindConn (e.g. TLS
// ClientHello) were counted there in the pre-batching implementation too.
// Bytes read before BindConn are dropped from per-domain attribution.
func (c *countingConn) flush(up bool, n uint64) {
	if n == 0 {
		return
	}
	c.stats.metrics.RecordBytes(up, int(n))

	domain, _ := c.domain.Load().(string)
	if domain == "" {
		return
	}
	source := Source(c.kind)
	var upBytes, downBytes uint64
	if up {
		upBytes = n
	} else {
		downBytes = n
	}
	c.stats.record(domain, source, c.clientIP, 0, upBytes, downBytes, func(d *domainStat, src *sourceStat, dc *clientStat) {
		if up {
			d.bytesUp += n
			src.bytesUp += n
			if dc != nil {
				dc.bytesUp += n
			}
		} else {
			d.bytesDown += n
			src.bytesDown += n
			if dc != nil {
				dc.bytesDown += n
			}
		}
	})
}
