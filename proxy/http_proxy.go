package proxy

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/wweir/sower/transport"
	"github.com/wweir/utils/log"
)

func StartHTTPProxy(httpProxyAddr, serverAddr string, password []byte, shouldProxy func(string) bool) {
	srv := &http.Server{
		Addr: httpProxyAddr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodConnect {
				httpsProxy(w, r, serverAddr, password, shouldProxy)
			} else {
				httpProxy(w, r, serverAddr, password, shouldProxy)
			}
		}),
		// Disable HTTP/2.
		TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){},
		IdleTimeout:  90 * time.Second,
	}

	log.Infow("start sower http proxy", "http_proxy", httpProxyAddr)
	go log.Fatalw("serve http proxy", "addr", httpProxyAddr, "err", srv.ListenAndServe())
}

func httpProxy(w http.ResponseWriter, r *http.Request,
	serverAddr string, password []byte, shouldProxy func(string) bool) {

	target, host := withDefaultPort(r.Host, "80")

	roundTripper := &http.Transport{}
	if shouldProxy(host) {
		roundTripper.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return transport.Dial(serverAddr, target, password)
		}
	}

	resp, err := roundTripper.RoundTrip(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()

	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func httpsProxy(w http.ResponseWriter, r *http.Request,
	serverAddr string, password []byte, shouldProxy func(string) bool) {

	target, host := withDefaultPort(r.Host, "443")

	conn, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	conn.(*net.TCPConn).SetKeepAlive(true)

	if _, err := conn.Write([]byte(r.Proto + " 200 Connection established\r\n\r\n")); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		conn.Close()
		return
	}

	var rc net.Conn
	if shouldProxy(host) {
		rc, err = transport.Dial(serverAddr, target, password)
	} else {
		rc, err = net.Dial("tcp", target)
	}
	if err != nil {
		conn.Write([]byte("sower dial " + serverAddr + " fail: " + err.Error()))
		conn.Close()
		return
	}
	defer rc.Close()

	relay(conn, rc)
}
