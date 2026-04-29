package daemon

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

var ErrInvalidConnectivityIdentity = errors.New("invalid daemon connectivity identity")

type ConnectivityIdentity struct {
	PrivateKey  ed25519.PrivateKey `json:"-"`
	PublicKey   ed25519.PublicKey  `json:"-"`
	Fingerprint string             `json:"fingerprint"`
	CreatedAt   int64              `json:"created_at"`
}

type connectivityIdentityFile struct {
	PrivateKey  string `json:"private_key"`
	PublicKey   string `json:"public_key"`
	Fingerprint string `json:"fingerprint"`
	CreatedAt   int64  `json:"created_at"`
}

func ReadOrCreateConnectivityIdentity(paths Paths) (ConnectivityIdentity, error) {
	identity, err := ReadConnectivityIdentity(paths)
	if err == nil {
		return identity, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return ConnectivityIdentity{}, err
	}

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return ConnectivityIdentity{}, err
	}
	identity = ConnectivityIdentity{
		PrivateKey:  privateKey,
		PublicKey:   publicKey,
		Fingerprint: PublicKeyFingerprint(publicKey),
		CreatedAt:   time.Now().UTC().Unix(),
	}
	if err := WriteConnectivityIdentity(paths, identity); err != nil {
		return ConnectivityIdentity{}, err
	}
	return identity, nil
}

func ReadConnectivityIdentity(paths Paths) (ConnectivityIdentity, error) {
	payload, err := os.ReadFile(paths.ConnectivityIdentityFile)
	if err != nil {
		return ConnectivityIdentity{}, err
	}
	var stored connectivityIdentityFile
	if err := json.Unmarshal(payload, &stored); err != nil {
		return ConnectivityIdentity{}, err
	}
	return decodeConnectivityIdentity(stored)
}

func WriteConnectivityIdentity(paths Paths, identity ConnectivityIdentity) error {
	if err := validateConnectivityIdentity(identity); err != nil {
		return err
	}
	return writePrivateJSONFile(paths.ConnectivityIdentityFile, connectivityIdentityFile{
		PrivateKey:  base64.StdEncoding.EncodeToString(identity.PrivateKey),
		PublicKey:   base64.StdEncoding.EncodeToString(identity.PublicKey),
		Fingerprint: identity.Fingerprint,
		CreatedAt:   identity.CreatedAt,
	})
}

func PublicKeyFingerprint(publicKey ed25519.PublicKey) string {
	digest := sha256.Sum256(publicKey)
	return hex.EncodeToString(digest[:])
}

func decodeConnectivityIdentity(stored connectivityIdentityFile) (ConnectivityIdentity, error) {
	privateKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(stored.PrivateKey))
	if err != nil {
		return ConnectivityIdentity{}, fmt.Errorf("%w: private key is not base64", ErrInvalidConnectivityIdentity)
	}
	publicKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(stored.PublicKey))
	if err != nil {
		return ConnectivityIdentity{}, fmt.Errorf("%w: public key is not base64", ErrInvalidConnectivityIdentity)
	}
	identity := ConnectivityIdentity{
		PrivateKey:  ed25519.PrivateKey(privateKey),
		PublicKey:   ed25519.PublicKey(publicKey),
		Fingerprint: strings.ToLower(strings.TrimSpace(stored.Fingerprint)),
		CreatedAt:   stored.CreatedAt,
	}
	if err := validateConnectivityIdentity(identity); err != nil {
		return ConnectivityIdentity{}, err
	}
	return identity, nil
}

func validateConnectivityIdentity(identity ConnectivityIdentity) error {
	if len(identity.PrivateKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("%w: private key length", ErrInvalidConnectivityIdentity)
	}
	if len(identity.PublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: public key length", ErrInvalidConnectivityIdentity)
	}
	privatePublic, ok := identity.PrivateKey.Public().(ed25519.PublicKey)
	if !ok || !bytes.Equal(privatePublic, identity.PublicKey) {
		return fmt.Errorf("%w: public key mismatch", ErrInvalidConnectivityIdentity)
	}
	if identity.Fingerprint != PublicKeyFingerprint(identity.PublicKey) {
		return fmt.Errorf("%w: fingerprint mismatch", ErrInvalidConnectivityIdentity)
	}
	if identity.CreatedAt <= 0 {
		return fmt.Errorf("%w: missing created_at", ErrInvalidConnectivityIdentity)
	}
	return nil
}
