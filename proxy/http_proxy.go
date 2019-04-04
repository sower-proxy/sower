package proxy

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/golang/glog"
	"github.com/wweir/sower/proxy/parser"
	"github.com/wweir/sower/proxy/shadow"
	"github.com/wweir/sower/proxy/socks5"
	"github.com/wweir/sower/proxy/transport"
)

func StartHttpProxy(tran transport.Transport, isSocks5 bool, server, cipher, password, addr string) {
	srv := &http.Server{
		Addr: addr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resolveAddr(&server)

			if r.Method == http.MethodConnect {
				httpsProxy(w, r, tran, isSocks5, server, cipher, password)
			} else {
				httpProxy(w, r, tran, isSocks5, server, cipher, password)
			}
		}),
		// Disable HTTP/2.
		TLSNextProto: map[string]func(*http.Server, *tls.Conn, http.Handler){},
		IdleTimeout:  90 * time.Second,
	}

	glog.Fatalln(srv.ListenAndServe())
}

func httpProxy(w http.ResponseWriter, r *http.Request,
	tran transport.Transport, isSocks5 bool, server, cipher, password string) {

	roundTripper := &http.Transport{
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	if isSocks5 {
		roundTripper.Proxy = func(*http.Request) (*url.URL, error) {
			return url.Parse("socks5://" + server)
		}

	} else {
		roundTripper.DialContext = func(context.Context, string, string) (net.Conn, error) {
			conn, err := tran.Dial(server)
			if err != nil {
				return nil, err
			}

			conn = shadow.Shadow(conn, cipher, password)
			return parser.NewHttpConn(conn), nil
		}
	}

	resp, err := roundTripper.RoundTrip(r)
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
	tran transport.Transport, isSocks5 bool, server, cipher, password string) {

	// local conn
	conn, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	conn.(*net.TCPConn).SetKeepAlive(true)

	if _, err := conn.Write([]byte(r.Proto + " 200 Connection established\r\n\r\n")); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		conn.Close()
		glog.Errorln("serve https proxy, write data fail:", err)
		return
	}

	// remote conn
	rc, err := tran.Dial(server)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		conn.Close()
		glog.Errorln("serve https proxy, dial remote fail:", err)
		return
	}

	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		conn.Close()
		glog.Errorln("serve https proxy, dial remote fail:", err)
		return
	}

	if isSocks5 {
		rc = socks5.ToSocks5(rc, host, port)

	} else {
		rc = shadow.Shadow(rc, cipher, password)
		rc = parser.NewHttpsConn(rc, port)
	}

	relay(rc, conn)
}
