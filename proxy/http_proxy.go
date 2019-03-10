package proxy

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/golang/glog"
	"github.com/wweir/sower/proxy/shadow"
	"github.com/wweir/sower/proxy/transport"
)

func StartHttpProxy(tran transport.Transport, server, cipher, password, addr string) {
	resolved := false

	srv := &http.Server{
		Addr: addr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !resolved {
				if addr, err := net.ResolveTCPAddr("tcp", server); err != nil {
					glog.Errorln(err)
				} else {
					server = addr.String()
					resolved = true
				}
			}

			if r.Method == http.MethodConnect {
				httpsProxy(w, r, tran, server, cipher, password)
			} else {
				httpProxy(w, r, tran, server, cipher, password)
			}
		}),
		// Disable HTTP/2.
		TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){},
		IdleTimeout:  90 * time.Second,
	}

	glog.Fatalln(srv.ListenAndServe())
}

func httpProxy(w http.ResponseWriter, req *http.Request,
	tran transport.Transport, server, cipher, password string) {

	roundTripper := &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			conn, err := tran.Dial(server)
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
		glog.Errorln("serve https proxy, get remote data:", err)
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
	tran transport.Transport, server, cipher, password string) {

	// local conn
	conn, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	conn.(*net.TCPConn).SetKeepAlive(true)

	if _, err := conn.Write([]byte(r.Proto + " 200 Connection established\r\n\r\n")); err != nil {
		conn.Close()
		glog.Errorln("serve https proxy, write data fail:", err)
		return
	}

	// remote conn
	rc, err := tran.Dial(server)
	if err != nil {
		conn.Close()
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		glog.Errorln("serve https proxy, dial remote fail:", err)
		return
	}
	rc = shadow.Shadow(rc, cipher, password)

	relay(rc, conn)
}
