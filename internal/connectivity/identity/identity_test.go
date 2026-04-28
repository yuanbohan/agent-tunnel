package identity

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"errors"
	"testing"
	"time"
)

func TestSelfSignedCertificateSPKIMatchesPublicKey(t *testing.T) {
	priv := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x11}, ed25519.SeedSize))
	pub := priv.Public().(ed25519.PublicKey)

	cert, err := SelfSignedCertificate(priv, CertificateOptions{Now: time.Unix(1700000000, 0)})
	if err != nil {
		t.Fatalf("SelfSignedCertificate returned error: %v", err)
	}

	got, err := CertificateSPKI(cert.Certificate[0])
	if err != nil {
		t.Fatalf("CertificateSPKI returned error: %v", err)
	}
	want, err := PublicKeySPKI(pub)
	if err != nil {
		t.Fatalf("PublicKeySPKI returned error: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("SPKI = %x, want %x", got, want)
	}

	if err := VerifyPinnedCertificate([][]byte{cert.Certificate[0]}, want); err != nil {
		t.Fatalf("VerifyPinnedCertificate returned error: %v", err)
	}
}

func TestVerifyPinnedCertificateRejectsDifferentSPKI(t *testing.T) {
	first := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x22}, ed25519.SeedSize))
	second := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x33}, ed25519.SeedSize))

	cert, err := SelfSignedCertificate(first, CertificateOptions{Now: time.Unix(1700000000, 0)})
	if err != nil {
		t.Fatalf("SelfSignedCertificate returned error: %v", err)
	}
	wrongSPKI, err := PublicKeySPKI(second.Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatalf("PublicKeySPKI returned error: %v", err)
	}

	err = VerifyPinnedCertificate([][]byte{cert.Certificate[0]}, wrongSPKI)
	if !errors.Is(err, ErrPinnedKeyMismatch) {
		t.Fatalf("err = %v, want ErrPinnedKeyMismatch", err)
	}
}

func TestVerifyPinnedCertificateRejectsMissingPeerCertificate(t *testing.T) {
	err := VerifyPinnedCertificate(nil, []byte("pin"))
	if !errors.Is(err, ErrMissingPeerCertificate) {
		t.Fatalf("err = %v, want ErrMissingPeerCertificate", err)
	}
}

func TestSelfSignedCertificateRejectsWrongPrivateKeyType(t *testing.T) {
	_, err := SelfSignedCertificate(struct{}{}, CertificateOptions{})
	if !errors.Is(err, ErrInvalidPrivateKey) {
		t.Fatalf("err = %v, want ErrInvalidPrivateKey", err)
	}
}

func TestCertificateSPKIRejectsInvalidCertificate(t *testing.T) {
	_, err := CertificateSPKI([]byte("not a cert"))
	if err == nil {
		t.Fatal("CertificateSPKI returned nil error for invalid DER")
	}
	if _, ok := err.(x509.CertificateInvalidError); ok {
		t.Fatalf("err = %T, want parse error, not certificate validity error", err)
	}
}
