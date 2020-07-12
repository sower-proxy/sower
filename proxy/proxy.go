package proxy

import (
	"crypto/tls"
	"net"
	"net/http"

	"github.com/wweir/sower/transport"
	"github.com/wweir/util-go/log"
	"golang.org/x/crypto/acme/autocert"
)

func StartClient(serverAddr, password string,
	forwards map[string]string, shouldProxy func(string) (bool, bool)) {

	passwordData := []byte(password)
	relayToRemote := func(lnAddr, target string,
		parseFn func(net.Conn) (net.Conn, string, error),
		shouldProxy func(string) (bool, bool)) {

		ln, err := net.Listen("tcp", lnAddr)
		if err != nil {
			log.Fatalw("tcp listen", "port", lnAddr, "err", err)
		}

		for {
			conn, err := ln.Accept()
			if err != nil {
				log.Errorw("tcp accept", "port", lnAddr, "err", err)
				continue
			}

			go func(conn net.Conn) {
				defer conn.Close()

				if parseFn != nil {
					if conn, target, err = parseFn(conn); err != nil {
						log.Warnw("parse target", "err", err)
						return
					}
				}

				rc, err := transport.Dial(target, func(domain string) (string, []byte) {
					if _, ok := shouldProxy(domain); ok {
						return serverAddr, passwordData
					}
					return "", nil
				})
				if err != nil {
					log.Warnw("dial", "addr", target, "err", err)
					return
				}
				defer rc.Close()

				relay(conn, rc)
			}(conn)
		}
	}

	for from, to := range forwards {
		go relayToRemote(from, to, nil, func(string) (bool, bool) { return false, true })
	}

	go relayToRemote(":http", "", ParseHTTP, shouldProxy)
	go relayToRemote(":https", "", ParseHTTPS, shouldProxy)

	log.Infow("start sower client", "forwards", forwards)

	select {}
}

func StartServer(relayTarget, password, cacheDir, certFile, keyFile, email string) {
	certManager := autocert.Manager{
		Prompt: autocert.AcceptTOS,
		Email:  email,
		Cache:  autocert.DirCache(cacheDir),
	}

	tlsConf := &tls.Config{
		GetCertificate: certManager.GetCertificate,
		MinVersion:     tls.VersionTLS12,
		NextProtos:     []string{"http/1.1", "h2"},
	}
	if certFile != "" && keyFile != "" {
		if cert, err := tls.LoadX509KeyPair(certFile, keyFile); err != nil {
			log.Fatalw("load certificate", "cert", certFile, "key", keyFile, "err", err)
		} else {
			tlsConf.GetCertificate = nil
			tlsConf.Certificates = []tls.Certificate{cert}
		}
	}

	// Try to redirect 80 to 443
	go http.ListenAndServe(":80", certManager.HTTPHandler(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			r.URL.Scheme = "https"
			if host, _, err := net.SplitHostPort(r.Host); err != nil {
				r.URL.Host = r.Host
			} else {
				r.URL.Host = host
			}

			http.Redirect(w, r, r.URL.String(), 301)
		})))

	log.Infow("start sower server", "relay_to", relayTarget)
	ln, err := tls.Listen("tcp", ":443", tlsConf)
	if err != nil {
		log.Fatalw("tcp listen", "err", err)
	}

	passwordData := []byte(password)
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Errorw("tcp accept", "err", err)
			continue
		}

		go func(conn net.Conn) {
			defer conn.Close()

			conn, target := transport.ParseProxyConn(conn, passwordData)
			if target == "" {
				target = relayTarget
			}

			rc, err := net.Dial("tcp", target)
			if err != nil {
				log.Errorw("tcp dial", "addr", target, "err", err)
				return
			}
			defer rc.Close()

			relay(conn, rc)
		}(conn)
	}
}
