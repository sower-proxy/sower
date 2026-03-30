package upstreamtls

import (
	cryptotls "crypto/tls"
	"fmt"
	"net"
	"strings"
	"time"

	utls "github.com/refraction-networking/utls"
)

type Options struct {
	ServerName         string
	ClientHello        string
	InsecureSkipVerify bool
}

func ValidateClientHello(value string) error {
	_, err := clientHelloID(value)
	return err
}

func Dial(dialer *net.Dialer, network, addr string, options Options) (net.Conn, error) {
	if options.ClientHello == "" {
		return cryptotls.DialWithDialer(dialer, network, addr, &cryptotls.Config{
			ServerName:         options.ServerName,
			InsecureSkipVerify: options.InsecureSkipVerify,
		})
	}

	helloID, err := clientHelloID(options.ClientHello)
	if err != nil {
		return nil, err
	}

	rawConn, err := dialer.Dial(network, addr)
	if err != nil {
		return nil, err
	}

	if timeout := dialer.Timeout; timeout > 0 {
		if err := rawConn.SetDeadline(time.Now().Add(timeout)); err != nil {
			rawConn.Close()
			return nil, fmt.Errorf("set tls deadline: %w", err)
		}
		defer rawConn.SetDeadline(time.Time{})
	}

	serverName := options.ServerName
	if serverName == "" {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			rawConn.Close()
			return nil, fmt.Errorf("split tls addr %q: %w", addr, err)
		}
		serverName = host
	}

	conn := utls.UClient(rawConn, &utls.Config{
		ServerName:         serverName,
		InsecureSkipVerify: options.InsecureSkipVerify,
	}, helloID)
	if err := conn.Handshake(); err != nil {
		rawConn.Close()
		return nil, fmt.Errorf("utls handshake: %w", err)
	}
	return conn, nil
}

func clientHelloID(value string) (utls.ClientHelloID, error) {
	switch normalizeClientHello(value) {
	case "chrome":
		return utls.HelloChrome_Auto, nil
	case "firefox":
		return utls.HelloFirefox_Auto, nil
	case "ios":
		return utls.HelloIOS_Auto, nil
	case "android":
		return utls.HelloAndroid_11_OkHttp, nil
	case "edge":
		return utls.HelloEdge_Auto, nil
	case "safari":
		return utls.HelloSafari_Auto, nil
	case "360":
		return utls.Hello360_Auto, nil
	case "qq":
		return utls.HelloQQ_Auto, nil
	case "randomized":
		return utls.HelloRandomized, nil
	case "randomizedalpn":
		return utls.HelloRandomizedALPN, nil
	case "randomizednoalpn":
		return utls.HelloRandomizedNoALPN, nil
	case "golang":
		return utls.HelloGolang, nil
	default:
		return utls.ClientHelloID{}, fmt.Errorf("unsupported tls client hello %q", value)
	}
}

func normalizeClientHello(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, "-", "")
	value = strings.ReplaceAll(value, "_", "")
	return value
}
