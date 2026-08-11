package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/sower-proxy/sower/config"
	"github.com/sower-proxy/sower/internal/admin"
	"github.com/sower-proxy/sower/router"
)

const (
	sharedHeaderMaxBytes = 32 << 10
	sharedHeaderTimeout  = 5 * time.Second
)

var errHeaderTooLarge = errors.New("request header too large")

// sharedAdminHTTPAddr reports whether the admin console shares the DNS HTTP
// proxy listener on port 80. In shared mode the admin server and the HTTP
// proxy accept connections from the same IP:80 and are distinguished by the
// request head: origin-form requests whose Host equals the listener IP are
// admin traffic; CONNECT, absolute-form requests, and any other Host are
// proxy traffic.
func sharedAdminHTTPAddr(cfg config.SowerConfig) (string, bool) {
	if cfg.Admin.Disable || cfg.Admin.Addr == "" {
		return "", false
	}
	if cfg.DNS.Disable || strings.TrimSpace(cfg.DNS.Serve) == "" {
		return "", false
	}
	httpAddr := net.JoinHostPort(cfg.DNS.Serve, "80")
	if cfg.Admin.Addr != httpAddr {
		return "", false
	}
	return httpAddr, true
}

// classifyHTTPConn reads the request head from conn and decides whether it is
// an admin console request. The returned connection replays every byte read
// during classification, so the caller can hand it to either handler without
// losing data. A nil connection means the head was malformed, oversized, or
// timed out and the connection was closed.
func classifyHTTPConn(conn net.Conn, adminHost string) (net.Conn, bool) {
	_ = conn.SetReadDeadline(time.Now().Add(sharedHeaderTimeout))
	br := bufio.NewReaderSize(conn, sharedHeaderMaxBytes)
	head, err := readRequestHead(br)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		slog.Debug("classify http request head", "error", err)
		_ = conn.Close()
		return nil, false
	}

	// Replay every byte consumed by the classifier, including bytes already
	// buffered inside br beyond the head terminator.
	rest, _ := br.Peek(br.Buffered())
	prefix := make([]byte, 0, len(head)+len(rest))
	prefix = append(prefix, head...)
	prefix = append(prefix, rest...)
	replayed := &prefixConn{Conn: conn, prefix: bytes.NewReader(prefix)}

	return replayed, isAdminRequest(head, adminHost)
}

// readRequestHead reads through the end of the HTTP header block ("\r\n\r\n"),
// bounded by sharedHeaderMaxBytes. Bytes already buffered inside br are not
// consumed from the caller's perspective: the returned head plus br.Buffered()
// covers everything read from the underlying connection.
func readRequestHead(br *bufio.Reader) ([]byte, error) {
	var head []byte
	for {
		line, err := br.ReadSlice('\n')
		head = append(head, line...)
		if bytes.HasSuffix(head, []byte("\r\n\r\n")) {
			return head, nil
		}
		if err == bufio.ErrBufferFull {
			if len(head) >= sharedHeaderMaxBytes {
				return nil, errHeaderTooLarge
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		if len(head) >= sharedHeaderMaxBytes {
			return nil, errHeaderTooLarge
		}
	}
}

// isAdminRequest reports whether the request head targets the admin console:
// an origin-form request whose Host equals the admin listener IP. CONNECT and
// absolute-form requests are always proxy traffic.
func isAdminRequest(head []byte, adminHost string) bool {
	lineEnd := bytes.IndexByte(head, '\n')
	if lineEnd < 0 {
		return false
	}
	fields := strings.Fields(string(head[:lineEnd]))
	if len(fields) < 2 {
		return false
	}
	method, target := fields[0], fields[1]
	if method == "CONNECT" {
		return false
	}
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		return false
	}
	return hostOnly(requestHost(head)) == adminHost
}

// requestHost extracts the Host header value from a request head.
func requestHost(head []byte) string {
	idx := bytes.IndexByte(head, '\n')
	if idx < 0 {
		return ""
	}
	rest := head[idx+1:]
	for len(rest) > 0 {
		lineEnd := bytes.IndexByte(rest, '\n')
		var line []byte
		if lineEnd < 0 {
			line = rest
			rest = nil
		} else {
			line = rest[:lineEnd]
			rest = rest[lineEnd+1:]
		}
		line = bytes.TrimRight(line, "\r")
		if len(line) == 0 {
			break
		}
		colon := bytes.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(string(line[:colon])), "host") {
			return strings.TrimSpace(string(line[colon+1:]))
		}
	}
	return ""
}

// prefixConn replays a fixed prefix before reading from the underlying
// connection.
type prefixConn struct {
	net.Conn
	prefix *bytes.Reader
}

func (c *prefixConn) Read(p []byte) (int, error) {
	if c.prefix.Len() > 0 {
		return c.prefix.Read(p)
	}
	return c.Conn.Read(p)
}

// chanListener is a channel-backed net.Listener that hands accepted
// connections to a single consumer (the admin HTTP server).
type chanListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
	addr   net.Addr
}

func newChanListener(addr net.Addr) *chanListener {
	return &chanListener{
		conns:  make(chan net.Conn),
		closed: make(chan struct{}),
		addr:   addr,
	}
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *chanListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *chanListener) Addr() net.Addr { return l.addr }

// dispatch hands a classified admin connection to the admin server, closing
// it if the listener is already shut down.
func (l *chanListener) dispatch(conn net.Conn) {
	select {
	case l.conns <- conn:
	case <-l.closed:
		_ = conn.Close()
	}
}

// ServeSharedHTTP serves the admin console and the HTTP proxy from one
// listener, classifying each connection by its request head. adminHost is the
// normalized listener IP that identifies admin traffic.
func ServeSharedHTTP(ctx context.Context, ln net.Listener, r *router.Router, stats *admin.Stats, srv *admin.Server, adminHost string, errCh chan<- error) error {
	adminLn := newChanListener(ln.Addr())
	go func() {
		if err := srv.Serve(adminLn); err != nil && !errors.Is(err, net.ErrClosed) {
			reportServeError(errCh, "admin", err)
		}
	}()
	defer adminLn.Close()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if shouldRetryAccept(ctx, "http", err, stats) {
				continue
			}
			return wrapAcceptErr(ctx, "http", err)
		}
		go func(conn net.Conn) {
			replayed, isAdmin := classifyHTTPConn(conn, adminHost)
			if replayed == nil {
				return
			}
			if isAdmin {
				adminLn.dispatch(replayed)
				return
			}
			handleHTTPConn(replayed, r, stats)
		}(conn)
	}
}

var _ io.Reader = (*prefixConn)(nil)
