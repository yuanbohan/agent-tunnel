package transport

import (
	"crypto/ed25519"
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
	Certificate         tls.Certificate
	PinnedPeerPublicKey ed25519.PublicKey
	ServerName          string
}

func DaemonTLSConfig(config EndpointConfig) *tls.Config {
	pinnedPeerSPKI, pinnedPeerSPKIErr := identity.PublicKeySPKI(config.PinnedPeerPublicKey)
	return &tls.Config{
		MinVersion:             tls.VersionTLS13,
		NextProtos:             []string{ALPN},
		SessionTicketsDisabled: true,
		Certificates: []tls.Certificate{
			config.Certificate,
		},
		ClientAuth: tls.RequireAnyClientCert,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if pinnedPeerSPKIErr != nil {
				return pinnedPeerSPKIErr
			}
			return identity.VerifyPinnedCertificate(rawCerts, pinnedPeerSPKI)
		},
	}
}

func AndroidTLSConfig(config EndpointConfig) *tls.Config {
	pinnedPeerSPKI, pinnedPeerSPKIErr := identity.PublicKeySPKI(config.PinnedPeerPublicKey)
	return &tls.Config{
		MinVersion:             tls.VersionTLS13,
		NextProtos:             []string{ALPN},
		ServerName:             config.ServerName,
		InsecureSkipVerify:     true,
		SessionTicketsDisabled: true,
		Certificates: []tls.Certificate{
			config.Certificate,
		},
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if pinnedPeerSPKIErr != nil {
				return pinnedPeerSPKIErr
			}
			return identity.VerifyPinnedCertificate(rawCerts, pinnedPeerSPKI)
		},
	}
}

func QUICConfig() *quic.Config {
	return &quic.Config{
		HandshakeIdleTimeout:  2 * time.Second,
		MaxIdleTimeout:        2 * time.Minute,
		KeepAlivePeriod:       30 * time.Second,
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
