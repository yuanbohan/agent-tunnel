package identity

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"time"
)

var (
	ErrInvalidPrivateKey      = errors.New("invalid private key")
	ErrMissingPeerCertificate = errors.New("missing peer certificate")
	ErrPinnedKeyMismatch      = errors.New("pinned key mismatch")
)

type CertificateOptions struct {
	Now        time.Time
	NotBefore  time.Time
	NotAfter   time.Time
	CommonName string
}

func SelfSignedCertificate(key ed25519.PrivateKey, opts CertificateOptions) (tls.Certificate, error) {
	if len(key) != ed25519.PrivateKeySize {
		return tls.Certificate{}, ErrInvalidPrivateKey
	}

	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	notBefore := opts.NotBefore
	if notBefore.IsZero() {
		notBefore = now.Add(-time.Minute)
	}
	notAfter := opts.NotAfter
	if notAfter.IsZero() {
		notAfter = now.Add(365 * 24 * time.Hour)
	}
	commonName := opts.CommonName
	if commonName == "" {
		commonName = "tunnel-device"
	}

	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return tls.Certificate{}, err
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: commonName,
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	publicKey := key.Public().(ed25519.PublicKey)
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, err
	}

	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  key,
		Leaf:        leaf,
	}, nil
}

func PublicKeySPKI(publicKey ed25519.PublicKey) ([]byte, error) {
	return x509.MarshalPKIXPublicKey(publicKey)
}

func CertificateSPKI(certDER []byte) ([]byte, error) {
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, err
	}
	return x509.MarshalPKIXPublicKey(cert.PublicKey)
}

func VerifyPinnedCertificate(peerCertificates [][]byte, pinnedSPKI []byte) error {
	if len(peerCertificates) == 0 {
		return ErrMissingPeerCertificate
	}

	peerSPKI, err := CertificateSPKI(peerCertificates[0])
	if err != nil {
		return err
	}
	if !bytes.Equal(peerSPKI, pinnedSPKI) {
		return ErrPinnedKeyMismatch
	}
	return nil
}
