package proxy

import (
	"crypto/tls"
	"net"
	"net/http"
	"strconv"

	_http "github.com/wweir/sower/internal/http"
	"github.com/wweir/sower/internal/socks5"
	"github.com/wweir/sower/util"
	"github.com/wweir/utils/log"
	"golang.org/x/crypto/acme/autocert"
)

const configDir = "/etc/sower"

type head struct {
	checksum byte
	length   byte
}

func StartClient(password, serverAddr, httpProxy, dnsServeIP string, forwards map[string]string) {
	passwordData := []byte(password)
	_, isSocks5 := socks5.IsSocks5Schema(serverAddr)

	if httpProxy != "" {
		go startHTTPProxy(httpProxy, serverAddr, passwordData)
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
				defer conn.Close()

				if isSocks5 {
					teeConn := &util.TeeConn{Conn: conn}
					teeConn.StartOrReset()

					switch tgtType {
					case _http.TGT_HTTP:
						conn, host, port, err = _http.ParseHTTP(teeConn)
					case _http.TGT_HTTPS:
						conn, host, err = _http.ParseHTTPS(teeConn)
					}
					if err != nil {
						log.Errorw("parse socks5 target", "err", err)
						return
					}
					teeConn.Stop()
				}

				rc, err := dial(serverAddr, passwordData, tgtType, host, port)
				if err != nil {
					log.Errorw("dial", "addr", serverAddr, "err", err)
					return
				}
				defer rc.Close()

				relay(conn, rc)
			}(conn)
		}
	}

	if dnsServeIP != "" {
		go relayToRemote(_http.TGT_HTTP, dnsServeIP+":http", "", 80)
		go relayToRemote(_http.TGT_HTTPS, dnsServeIP+":https", "", 443)
	}

	for from, to := range forwards {
		go func(from, to string) {
			host, port := util.ParseHostPort(to, 0)
			relayToRemote(_http.TGT_OTHER, from, host, port)
		}(from, to)
	}

	select {}
}

func StartServer(relayTarget, password, certFile, keyFile, email string) {
	certManager := autocert.Manager{
		Prompt: autocert.AcceptTOS,
		Cache:  autocert.DirCache(configDir), //folder for storing certificates
		Email:  email,
	}
	tlsConf := &tls.Config{
		GetCertificate: certManager.GetCertificate,
		MinVersion:     tls.VersionTLS12,
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
				log.Errorw("tcp dial", "addr", addr, "err", err)
				return
			}
			defer rc.Close()

			relay(conn, rc)
		}(conn)
	}
}
