package transport

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"time"

	"github.com/quic-go/quic-go"

	"yuanbohan/tunnel/internal/connectivity/identity"
)

const ALPN = "tunnel-conn/1"

var (
	ErrALPNMismatch = errors.New("alpn mismatch")
	ErrEarlyData    = errors.New("0-rtt early data used")
)

type EndpointConfig struct {
	Certificate    tls.Certificate
	PinnedPeerSPKI []byte
	ServerName     string
}

func DaemonTLSConfig(config EndpointConfig) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		NextProtos: []string{ALPN},
		Certificates: []tls.Certificate{
			config.Certificate,
		},
		ClientAuth: tls.RequireAnyClientCert,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			return identity.VerifyPinnedCertificate(rawCerts, config.PinnedPeerSPKI)
		},
	}
}

func AndroidTLSConfig(config EndpointConfig) *tls.Config {
	return &tls.Config{
		MinVersion:         tls.VersionTLS13,
		NextProtos:         []string{ALPN},
		ServerName:         config.ServerName,
		InsecureSkipVerify: true,
		Certificates: []tls.Certificate{
			config.Certificate,
		},
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			return identity.VerifyPinnedCertificate(rawCerts, config.PinnedPeerSPKI)
		},
	}
}

func QUICConfig() *quic.Config {
	return &quic.Config{
		HandshakeIdleTimeout:  2 * time.Second,
		MaxIdleTimeout:        5 * time.Second,
		MaxIncomingStreams:    16,
		MaxIncomingUniStreams: 16,
		Allow0RTT:             false,
	}
}

func ValidateConnectionState(state quic.ConnectionState) error {
	if state.TLS.NegotiatedProtocol != ALPN {
		return fmt.Errorf("%w: got %q want %q", ErrALPNMismatch, state.TLS.NegotiatedProtocol, ALPN)
	}
	if state.Used0RTT {
		return ErrEarlyData
	}
	return nil
}
