package proxy

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httputil"
	"time"

	"github.com/wweir/sower/transport"
	"github.com/wweir/sower/util"
	"github.com/wweir/utils/log"
)

// StartHTTPProxy start http reverse proxy.
// The httputil.ReverseProxy do not supply enough support for https request.
func StartHTTPProxy(httpProxyAddr, serverAddr string, password []byte,
	shouldProxy func(string) bool) {

	proxy := httputil.ReverseProxy{
		Director: func(r *http.Request) {},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return transport.Dial(serverAddr, func(host string) (string, []byte) {
					if shouldProxy(host) {
						return httpProxyAddr, password
					}
					return "", nil
				})
			},
		},
	}

	srv := &http.Server{
		Addr: httpProxyAddr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodConnect {
				httpsProxy(w, r, serverAddr, password, shouldProxy)
			} else {
				proxy.ServeHTTP(w, r)
			}
		}),
		// Disable HTTP/2.
		TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){},
		IdleTimeout:  90 * time.Second,
	}

	log.Infow("start sower http proxy", "http_proxy", httpProxyAddr)
	go log.Fatalw("serve http proxy", "addr", httpProxyAddr, "err", srv.ListenAndServe())
}

func httpsProxy(w http.ResponseWriter, r *http.Request,
	serverAddr string, password []byte, shouldProxy func(string) bool) {

	conn, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	conn.(*net.TCPConn).SetKeepAlive(true)
	defer conn.Close()

	if _, err := conn.Write([]byte(r.Proto + " 200 Connection established\r\n\r\n")); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	target, _ := util.WithDefaultPort(r.Host, "443")
	rc, err := transport.Dial(target, func(host string) (string, []byte) {
		if shouldProxy(host) {
			return serverAddr, password
		}
		return "", nil
	})
	if err != nil {
		conn.Write([]byte("sower dial " + serverAddr + " fail: " + err.Error()))
		conn.Close()
		return
	}
	defer rc.Close()

	relay(conn, rc)
}
