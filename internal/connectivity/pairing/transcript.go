package pairing

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const Version = 1

var (
	ErrInvalidVersion      = errors.New("invalid pairing version")
	ErrInvalidSignature    = errors.New("invalid pairing signature")
	ErrInvalidHexField     = errors.New("invalid pairing hex field")
	ErrFingerprintMismatch = errors.New("pairing fingerprint mismatch")
	ErrInvitationExpired   = errors.New("pairing invitation expired")
	ErrTranscriptMismatch  = errors.New("pairing transcript mismatch")
)

func PublicKeyFingerprintHex(publicKey []byte) (string, error) {
	if len(publicKey) != ed25519.PublicKeySize {
		return "", ErrInvalidPublicKeyLength
	}
	digest := sha256.Sum256(publicKey)
	return hex.EncodeToString(digest[:]), nil
}

func decodePublicKeyHex(raw string) (ed25519.PublicKey, error) {
	decoded, err := hex.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: public_key", ErrInvalidHexField)
	}
	if len(decoded) != ed25519.PublicKeySize {
		return nil, ErrInvalidPublicKeyLength
	}
	return ed25519.PublicKey(decoded), nil
}

func decodeHexField(name, raw string) ([]byte, error) {
	decoded, err := hex.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidHexField, name)
	}
	return decoded, nil
}

func normalizeFingerprint(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func validateFingerprint(publicKey []byte, fingerprint string) error {
	computed, err := PublicKeyFingerprintHex(publicKey)
	if err != nil {
		return err
	}
	if computed != normalizeFingerprint(fingerprint) {
		return ErrFingerprintMismatch
	}
	return nil
}

func validateExpiry(expiresAt int64, now time.Time) error {
	if expiresAt <= 0 {
		return ErrInvitationExpired
	}
	if now.IsZero() {
		return nil
	}
	if !time.Unix(expiresAt, 0).After(now) {
		return ErrInvitationExpired
	}
	return nil
}
