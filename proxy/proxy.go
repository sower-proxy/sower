package proxy

import (
	"crypto/tls"
	"net"
	"net/http"
	"strconv"

	_http "github.com/wweir/sower/internal/http"
	"github.com/wweir/utils/log"
	"golang.org/x/crypto/acme/autocert"
)

const configDir = "/etc/sower"

type head struct {
	checksum byte
	length   byte
}

func StartClient(password, serverAddr, httpProxy, dnsRedirectIP string, forwardMap map[string]string) {
	passwordData := []byte(password)
	if httpProxy != "" {
		startHTTPProxy(httpProxy, serverAddr, passwordData)
	}

	relayToRemote := func(tgtType byte, lnAddr string, host string, port uint16) {
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
				rc, err := tls.Dial("tcp", serverAddr, &tls.Config{})
				if err != nil {
					log.Errorw("tls dial", "addr", serverAddr, "err", err)
					return
				}

				relay(conn, _http.NewTgtConn(rc, passwordData, tgtType, host, port))
			}(conn)
		}
	}

	if dnsRedirectIP != "" {
		go relayToRemote(_http.TGT_HTTP, dnsRedirectIP+":http", "", 80)
		go relayToRemote(_http.TGT_HTTPS, dnsRedirectIP+":http", "", 443)
	}

	for from, to := range forwardMap {
		go func(from, to string) {
			host, portStr, err := net.SplitHostPort(to)
			if err != nil {
				log.Fatalw("parse port forward", "target", to, "err", err)
			}
			portNum, err := strconv.ParseUint(portStr, 10, 16)
			if err != nil {
				log.Fatalw("parse port forward", "target", to, "err", err)
			}
			port := uint16(portNum)

			relayToRemote(_http.TGT_OTHER, from, host, port)
		}(from, to)
	}

}

func StartServer(relayTarget, password, certFile, keyFile, email string) {
	certManager := autocert.Manager{
		Prompt: autocert.AcceptTOS,
		Cache:  autocert.DirCache(configDir), //folder for storing certificates
		Email:  email,
	}
	tlsConf := &tls.Config{GetCertificate: certManager.GetCertificate}
	if certFile != "" && keyFile != "" {
		if cert, err := tls.LoadX509KeyPair(certFile, keyFile); err != nil {
			log.Fatalw("load certificate", "cert", certFile, "key", keyFile, "err", err)
		} else {
			tlsConf = &tls.Config{Certificates: []tls.Certificate{cert}}
		}
	}

	// Try to redirect 80 to 443
	go http.ListenAndServe(":http", certManager.HTTPHandler(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if host, _, err := net.SplitHostPort(r.Host); err != nil {
				r.URL.Host = r.Host
			} else {
				r.URL.Host = host
			}
			r.URL.Scheme = "https"
			http.Redirect(w, r, r.URL.String(), 301)
		})))

	ln, err := tls.Listen("tcp", ":https", tlsConf)
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
			conn, domain, port, err := _http.ParseAddr(conn, passwordData)
			if err != nil {
				log.Errorw("parse relay target", "err", err)
				return
			}
			defer conn.Close()

			addr := relayTarget
			if domain != "" {
				addr = net.JoinHostPort(domain, strconv.Itoa(int(port)))
			}

			rc, err := net.Dial("tcp", addr)
			if err != nil {
				log.Errorw("tcp dial", "host", domain, "addr", addr, "err", err)
				return
			}
			defer rc.Close()

			relay(conn, rc)
		}(conn)
	}
}
