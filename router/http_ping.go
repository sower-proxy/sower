package router

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"time"
)

// Port ==========================
type Port uint16

const (
	HTTP  Port = 80
	HTTPS Port = 443
)

// Ping try connect to a http(s) server with domain though the http addr
func (p Port) Ping(domain string, timeout time.Duration) error {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(domain, p.String()), timeout)
	if err != nil {
		return err
	}
	defer conn.Close()

	return p.PingWithConn(domain, conn, timeout)
}

// PingWithConn try connect to a http(s) server with domain though the http addr
func (p Port) PingWithConn(domain string, conn net.Conn, timeout time.Duration) error {
	conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write(p.PingMsg(domain)); err != nil {
		return err
	}

	// err -> nil:		read something succ
	// err -> io.EOF:	no such domain or connection refused
	// err -> timeout:	tcp package has been dropped
	_, err := conn.Read(make([]byte, 1))
	if err == nil || err == io.EOF {
		return nil
	}
	return err
}

func (p Port) String() string {
	return strconv.Itoa(int(p))
}

func (p Port) PingMsg(domain string) []byte {
	switch p {
	case HTTP:
		return []byte("TRACE / HTTP/1.1\r\nHost: " + domain + "\r\n\r\n")
	case HTTPS:
		return NewClientHelloSNIMsg(domain)
	default:
		panic("invalid port")
	}
}

// SNI ==========================
type clientHelloSNI struct {
	ContentType uint8
	Version     uint16
	Length      uint16
	handshakeProtocol
}
type handshakeProtocol struct {
	HandshakeType            uint8
	LengthExpand             uint8
	Length                   uint16
	Version                  uint16
	Random                   [32]byte
	SessionIDLength          uint8
	CipherSuitesLength       uint16
	CipherSuite              uint16
	CompressionMethodsLength uint8
	CompressionMethod        uint8
	ExtensionsLength         uint16
	extensionServerName
}
type extensionServerName struct {
	Type   uint16
	Length uint16
	serverNameIndicationExtension
}
type serverNameIndicationExtension struct {
	ServerNameListlength uint16
	ServerNameType       uint8
	ServerNamelength     uint16
	// ServerName        []byte // Disable for fix length
}

func NewClientHelloSNIMsg(domain string) []byte {
	length := uint16(len(domain))
	msg := &clientHelloSNI{
		ContentType: 0x16,   // Content Type: Handshake (22)
		Version:     0x0301, // Version: TLS 1.0
		Length:      length + 56,
		handshakeProtocol: handshakeProtocol{
			HandshakeType:            0x01, // Handshake Type: Client Hello (1)
			Length:                   length + 52,
			Version:                  0x0303,     // Version: TLS 1.2 (0x0303)
			Random:                   [32]byte{}, // [32]byte{},
			SessionIDLength:          0x0,        // Session ID Length: 0
			CipherSuitesLength:       2,          // Cipher Suites Length: 84
			CipherSuite:              tls.TLS_RSA_WITH_AES_128_GCM_SHA256,
			CompressionMethodsLength: 1,    // Compression Methods Length: 1
			CompressionMethod:        0x00, // Compression null
			ExtensionsLength:         length + 9,
			extensionServerName: extensionServerName{
				Type:   0x0000, // Type: server_name (0)
				Length: length + 5,
				serverNameIndicationExtension: serverNameIndicationExtension{
					ServerNameListlength: length + 3,
					ServerNameType:       0x00, // Server Name Type: host_name (0)
					ServerNamelength:     length,
				},
			},
		},
	}

	buf := bytes.NewBuffer(make([]byte, 0, length+61))
	binary.Write(buf, binary.BigEndian, msg)
	buf.WriteString(domain)
	return buf.Bytes()
}
