package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

func AuthContextFingerprint(authToken string) string {
	authToken = strings.TrimSpace(authToken)
	if authToken == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(authToken))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func AuthContextMatches(status StatusInfo, authToken string) bool {
	expected := AuthContextFingerprint(authToken)
	if expected == "" {
		return false
	}
	return strings.TrimSpace(status.AuthContextFingerprint) == expected
}
