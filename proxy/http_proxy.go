package proxy

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/golang/glog"
	"github.com/wweir/sower/shadow"
)

func StartHttpProxy(netType, server, cipher, password, listenIP, port string) {
	if port[0] != ':' {
		port = ":" + port
	}

	client := NewClient(netType)

	srv := &http.Server{
		Addr: listenIP + port,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodConnect {
				httpsProxy(w, r, client, server, cipher, password)
			} else {
				httpProxy(w, r, client, server, cipher, password)
			}
		}),
		// Disable HTTP/2.
		TLSNextProto: make(map[string]func(*http.Server, *tls.Conn, http.Handler)),
	}

	glog.Fatalln(srv.ListenAndServe())
}

func httpsProxy(w http.ResponseWriter, r *http.Request, client Client, server, cipher, password string) {
	// remote conn
	rc, err := client.Dial(server)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	rc = shadow.Shadow(rc, cipher, password)

	// local conn
	w.WriteHeader(http.StatusOK)
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
	}

	relay(rc, conn)
}

func httpProxy(w http.ResponseWriter, req *http.Request, client Client, server, cipher, password string) {
	roundTripper := &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			conn, err := client.Dial(server)
			if err != nil {
				return nil, err
			}
			return shadow.Shadow(conn, cipher, password), nil
		},
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	resp, err := roundTripper.RoundTrip(req)
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
