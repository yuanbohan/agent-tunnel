package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"strings"

	"golang.org/x/crypto/ssh"

	tunnelupdate "yuanbohan/tunnel/internal/tunnel/update"
)

const signingKeyEnv = "TUNNEL_RELEASE_SIGNING_PRIVATE_KEY"

func main() {
	if len(os.Args) < 2 {
		die("usage: release-sign <keygen|sign|public-key|trusted-public-key> [args]")
	}

	switch os.Args[1] {
	case "keygen":
		if len(os.Args) != 4 {
			die("usage: release-sign keygen <private-key-path> <public-key-path>")
		}
		if err := runKeygen(os.Args[2], os.Args[3]); err != nil {
			die(err.Error())
		}
	case "sign":
		if len(os.Args) != 4 {
			die("usage: release-sign sign <input-path> <signature-path>")
		}
		if err := runSign(os.Args[2], os.Args[3]); err != nil {
			die(err.Error())
		}
	case "public-key":
		if len(os.Args) != 2 {
			die("usage: release-sign public-key")
		}
		if err := runPublicKey(); err != nil {
			die(err.Error())
		}
	case "trusted-public-key":
		if len(os.Args) != 2 {
			die("usage: release-sign trusted-public-key")
		}
		if err := runTrustedPublicKey(); err != nil {
			die(err.Error())
		}
	default:
		die("usage: release-sign <keygen|sign|public-key|trusted-public-key> [args]")
	}
}

func runKeygen(privateKeyPath, publicKeyPath string) error {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate ed25519 key: %w", err)
	}

	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return fmt.Errorf("marshal private key: %w", err)
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privateKeyDER,
	})
	if privatePEM == nil {
		return fmt.Errorf("encode private key")
	}
	if err := os.WriteFile(privateKeyPath, privatePEM, 0o600); err != nil {
		return fmt.Errorf("write private key: %w", err)
	}

	sshPublicKey, err := ssh.NewPublicKey(publicKey)
	if err != nil {
		return fmt.Errorf("build authorized public key: %w", err)
	}
	publicLine := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPublicKey))) + "\n"
	if err := os.WriteFile(publicKeyPath, []byte(publicLine), 0o644); err != nil {
		return fmt.Errorf("write public key: %w", err)
	}
	return nil
}

func runSign(inputPath, signaturePath string) error {
	privateKey, err := loadSigningPrivateKey()
	if err != nil {
		return err
	}
	payload, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", inputPath, err)
	}
	signature, err := buildArmoredSignature(privateKey, payload)
	if err != nil {
		return err
	}
	if err := os.WriteFile(signaturePath, signature, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", signaturePath, err)
	}
	return nil
}

func runPublicKey() error {
	privateKey, err := loadSigningPrivateKey()
	if err != nil {
		return err
	}
	publicKeyLine, err := publicKeyLineFromPrivateKey(privateKey)
	if err != nil {
		return err
	}
	fields := strings.Fields(publicKeyLine)
	if len(fields) < 2 {
		return fmt.Errorf("parse derived public key")
	}
	fmt.Println(fields[1])
	return nil
}

func runTrustedPublicKey() error {
	fmt.Println(strings.TrimSpace(tunnelupdate.OfficialReleaseSigningPublicKeyBase64))
	return nil
}

func loadSigningPrivateKey() (ed25519.PrivateKey, error) {
	rawKey := os.Getenv(signingKeyEnv)
	if strings.TrimSpace(rawKey) == "" {
		return nil, fmt.Errorf("%s is required", signingKeyEnv)
	}

	block, _ := pem.Decode([]byte(rawKey))
	if block == nil {
		return nil, fmt.Errorf("parse %s: missing PEM block", signingKeyEnv)
	}
	parsedKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", signingKeyEnv, err)
	}
	ed25519PrivateKey, ok := parsedKey.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s must contain an ed25519 private key", signingKeyEnv)
	}
	return ed25519PrivateKey, nil
}

func buildArmoredSignature(privateKey ed25519.PrivateKey, payload []byte) ([]byte, error) {
	publicKey, err := ssh.NewPublicKey(privateKey.Public())
	if err != nil {
		return nil, fmt.Errorf("build signing public key: %w", err)
	}
	signer, err := ssh.NewSignerFromSigner(privateKey)
	if err != nil {
		return nil, fmt.Errorf("build ssh signer: %w", err)
	}

	hash := sha512.Sum512(payload)
	signedData := append([]byte("SSHSIG"), ssh.Marshal(struct {
		Namespace     string
		Reserved      string
		HashAlgorithm string
		Hash          []byte
	}{
		Namespace:     tunnelupdate.OfficialReleaseSignatureNamespace(),
		Reserved:      "",
		HashAlgorithm: "sha512",
		Hash:          hash[:],
	})...)

	signature, err := signer.Sign(rand.Reader, signedData)
	if err != nil {
		return nil, fmt.Errorf("sign checksums payload: %w", err)
	}

	raw := append([]byte("SSHSIG"), ssh.Marshal(struct {
		Version       uint32
		PublicKey     []byte
		Namespace     string
		Reserved      string
		HashAlgorithm string
		Signature     []byte
	}{
		Version:       1,
		PublicKey:     publicKey.Marshal(),
		Namespace:     tunnelupdate.OfficialReleaseSignatureNamespace(),
		Reserved:      "",
		HashAlgorithm: "sha512",
		Signature: ssh.Marshal(struct {
			Format string
			Blob   []byte
		}{
			Format: signature.Format,
			Blob:   signature.Blob,
		}),
	})...)

	var builder strings.Builder
	builder.WriteString("-----BEGIN SSH SIGNATURE-----\n")
	encoded := base64.StdEncoding.EncodeToString(raw)
	for len(encoded) > 76 {
		builder.WriteString(encoded[:76])
		builder.WriteByte('\n')
		encoded = encoded[76:]
	}
	builder.WriteString(encoded)
	builder.WriteString("\n-----END SSH SIGNATURE-----\n")
	return []byte(builder.String()), nil
}

func publicKeyLineFromPrivateKey(privateKey ed25519.PrivateKey) (string, error) {
	publicKey, err := ssh.NewPublicKey(privateKey.Public())
	if err != nil {
		return "", fmt.Errorf("build authorized public key: %w", err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(publicKey))), nil
}

func die(message string) {
	fmt.Fprintln(os.Stderr, "error:", message)
	os.Exit(1)
}
