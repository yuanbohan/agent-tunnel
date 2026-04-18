package update

import (
	"bytes"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

const (
	officialReleaseSigningKeyType                 = "ssh-ed25519"
	defaultOfficialReleaseSigningPublicKey        = "AAAAC3NzaC1lZDI1NTE5AAAAIO+yV8bMgSRdfozlBhqQ+xdFJZ5cAPI2T9sI6OSZRPXZ"
	officialReleaseSignatureMagicPreamble         = "SSHSIG"
	officialReleaseSignatureNamespace             = "tunnel-release"
	officialReleaseSignatureHashAlgorithm         = "sha512"
	officialReleaseSignatureVersion        uint32 = 1
)

var officialReleaseSigningPublicKeyBase64 = defaultOfficialReleaseSigningPublicKey

type sshsigBlob struct {
	Version       uint32
	PublicKey     []byte
	Namespace     string
	Reserved      string
	HashAlgorithm string
	Signature     []byte
}

type sshWireSignature struct {
	Format string
	Blob   []byte
	Rest   []byte `ssh:"rest"`
}

type sshsigSignedData struct {
	Namespace     string
	Reserved      string
	HashAlgorithm string
	Hash          []byte
}

func OfficialReleaseSigningPublicKeyBase64() string {
	return strings.TrimSpace(officialReleaseSigningPublicKeyBase64)
}

func OfficialReleaseSignatureNamespace() string {
	return officialReleaseSignatureNamespace
}

func verifyOfficialReleaseChecksumsSignature(checksumsPayload, signaturePayload []byte) error {
	return verifyChecksumsSignatureWithPublicKeyBase64(OfficialReleaseSigningPublicKeyBase64())(checksumsPayload, signaturePayload)
}

func verifyChecksumsSignatureWithPublicKeyBase64(publicKeyBase64 string) func([]byte, []byte) error {
	return func(checksumsPayload, signaturePayload []byte) error {
		publicKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(authorizedKeyLine(publicKeyBase64)))
		if err != nil {
			return fmt.Errorf("parse release signing public key: %w", err)
		}

		blob, err := parseArmoredSSHSIG(signaturePayload)
		if err != nil {
			return err
		}
		if blob.Version != officialReleaseSignatureVersion {
			return fmt.Errorf("unsupported checksums signature version %d", blob.Version)
		}
		if blob.Namespace != officialReleaseSignatureNamespace {
			return fmt.Errorf("unexpected checksums signature namespace %q", blob.Namespace)
		}
		if blob.HashAlgorithm != officialReleaseSignatureHashAlgorithm {
			return fmt.Errorf("unexpected checksums signature hash algorithm %q", blob.HashAlgorithm)
		}
		if !bytes.Equal(blob.PublicKey, publicKey.Marshal()) {
			return fmt.Errorf("checksums signature public key does not match trusted key")
		}

		var signature sshWireSignature
		if err := ssh.Unmarshal(blob.Signature, &signature); err != nil {
			return fmt.Errorf("parse checksums signature payload: %w", err)
		}
		signedData := buildSSHSIGSignedData(checksumsPayload, blob.Namespace, blob.Reserved, blob.HashAlgorithm)
		if err := publicKey.Verify(signedData, &ssh.Signature{
			Format: signature.Format,
			Blob:   signature.Blob,
			Rest:   signature.Rest,
		}); err != nil {
			return fmt.Errorf("invalid checksums signature")
		}
		return nil
	}
}

func authorizedKeyLine(publicKeyBase64 string) string {
	return fmt.Sprintf("%s %s", officialReleaseSigningKeyType, strings.TrimSpace(publicKeyBase64))
}

func buildSSHSIGSignedData(payload []byte, namespace, reserved, hashAlgorithm string) []byte {
	hash := sha512.Sum512(payload)
	return append([]byte(officialReleaseSignatureMagicPreamble), ssh.Marshal(sshsigSignedData{
		Namespace:     namespace,
		Reserved:      reserved,
		HashAlgorithm: hashAlgorithm,
		Hash:          hash[:],
	})...)
}

func parseArmoredSSHSIG(signaturePayload []byte) (sshsigBlob, error) {
	trimmed := strings.TrimSpace(string(signaturePayload))
	trimmed = strings.TrimPrefix(trimmed, "-----BEGIN SSH SIGNATURE-----")
	trimmed = strings.TrimSuffix(trimmed, "-----END SSH SIGNATURE-----")
	trimmed = strings.ReplaceAll(strings.TrimSpace(trimmed), "\n", "")
	if trimmed == "" {
		return sshsigBlob{}, fmt.Errorf("decode checksums signature: empty signature")
	}

	rawSignature, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		return sshsigBlob{}, fmt.Errorf("decode checksums signature: %w", err)
	}
	if !bytes.HasPrefix(rawSignature, []byte(officialReleaseSignatureMagicPreamble)) {
		return sshsigBlob{}, fmt.Errorf("decode checksums signature: missing SSHSIG preamble")
	}

	var blob sshsigBlob
	if err := ssh.Unmarshal(rawSignature[len(officialReleaseSignatureMagicPreamble):], &blob); err != nil {
		return sshsigBlob{}, fmt.Errorf("parse checksums signature: %w", err)
	}
	return blob, nil
}
