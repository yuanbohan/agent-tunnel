package auth

import (
	"encoding/hex"
	"strings"
)

const DeviceFingerprintHexLength = 64

func NormalizeDeviceFingerprint(raw string) (string, error) {
	fingerprint := strings.ToLower(strings.TrimSpace(raw))
	if fingerprint == "" {
		return "", nil
	}
	if len(fingerprint) != DeviceFingerprintHexLength {
		return "", ErrInvalidDeviceFingerprint
	}
	if _, err := hex.DecodeString(fingerprint); err != nil {
		return "", ErrInvalidDeviceFingerprint
	}
	return fingerprint, nil
}
