package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestEngineInstallLatestOfficialRelease(t *testing.T) {
	ctx := context.Background()
	currentBinary := mustExecutableFile(t, []byte("old-binary"))
	newBinary := []byte("new-binary")
	server, verifySignature := releaseTestServer(t, "v0.1.9", "linux", "amd64", newBinary)
	engine := NewEngine(Config{
		HTTPClient:               server.Client(),
		InstallBaseURL:           server.URL,
		ReleaseBaseURL:           func(version string) string { return server.URL + "/download/" + version },
		ExecutablePath:           func() (string, error) { return currentBinary, nil },
		CurrentVersion:           func() string { return "v0.1.7" },
		CurrentOfficial:          func() bool { return true },
		CurrentTarget:            func() (string, string, error) { return "linux", "amd64", nil },
		VerifyChecksumsSignature: verifySignature,
	})

	result, err := engine.InstallLatest(ctx)
	if err != nil {
		t.Fatalf("InstallLatest returned error: %v", err)
	}
	if !result.Updated {
		t.Fatal("Updated = false, want true")
	}
	if !result.RollbackAvailable || result.RollbackVersion != "v0.1.7" {
		t.Fatalf("rollback result = %#v, want rollback to previous official version", result)
	}

	payload, err := os.ReadFile(currentBinary)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(payload) != string(newBinary) {
		t.Fatalf("updated binary = %q, want %q", payload, newBinary)
	}
}

func TestEngineInstallLatestFromNonReleaseBuildClearsRollback(t *testing.T) {
	ctx := context.Background()
	currentBinary := mustExecutableFile(t, []byte("dev-binary"))
	newBinary := []byte("official-binary")
	server, verifySignature := releaseTestServer(t, "v0.1.9", "linux", "amd64", newBinary)
	engine := NewEngine(Config{
		HTTPClient:               server.Client(),
		InstallBaseURL:           server.URL,
		ReleaseBaseURL:           func(version string) string { return server.URL + "/download/" + version },
		ExecutablePath:           func() (string, error) { return currentBinary, nil },
		CurrentVersion:           func() string { return "v0.1.9-dev" },
		CurrentOfficial:          func() bool { return false },
		CurrentTarget:            func() (string, string, error) { return "linux", "amd64", nil },
		VerifyChecksumsSignature: verifySignature,
	})

	result, err := engine.InstallLatest(ctx)
	if err != nil {
		t.Fatalf("InstallLatest returned error: %v", err)
	}
	if !result.Updated {
		t.Fatal("Updated = false, want true")
	}
	if result.RollbackAvailable || result.RollbackVersion != "" {
		t.Fatalf("rollback result = %#v, want unavailable rollback", result)
	}
	if result.RollbackUnavailableReason == "" {
		t.Fatal("RollbackUnavailableReason = empty, want explanation")
	}
}

func TestEngineInstallLatestNoopsWhenAlreadyCurrentOfficialRelease(t *testing.T) {
	ctx := context.Background()
	currentBinary := mustExecutableFile(t, []byte("current-binary"))
	server, verifySignature := releaseTestServer(t, "v0.1.9", "linux", "amd64", []byte("should-not-install"))
	engine := NewEngine(Config{
		HTTPClient:               server.Client(),
		InstallBaseURL:           server.URL,
		ReleaseBaseURL:           func(version string) string { return server.URL + "/download/" + version },
		ExecutablePath:           func() (string, error) { return currentBinary, nil },
		CurrentVersion:           func() string { return "v0.1.9" },
		CurrentOfficial:          func() bool { return true },
		CurrentTarget:            func() (string, string, error) { return "linux", "amd64", nil },
		VerifyChecksumsSignature: verifySignature,
	})

	result, err := engine.InstallLatest(ctx)
	if err != nil {
		t.Fatalf("InstallLatest returned error: %v", err)
	}
	if result.Updated {
		t.Fatalf("Updated = true, want false: %#v", result)
	}

	payload, err := os.ReadFile(currentBinary)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(payload) != "current-binary" {
		t.Fatalf("binary payload = %q, want unchanged current-binary", payload)
	}
}

func TestEngineInstallLatestRejectsChecksumMismatch(t *testing.T) {
	ctx := context.Background()
	currentBinary := mustExecutableFile(t, []byte("old-binary"))
	server, verifySignature := releaseTestServer(t, "v0.1.9", "linux", "amd64", []byte("new-binary"))
	engine := NewEngine(Config{
		HTTPClient:               server.Client(),
		InstallBaseURL:           server.URL,
		ReleaseBaseURL:           func(version string) string { return server.URL + "/corrupt/" + version },
		ExecutablePath:           func() (string, error) { return currentBinary, nil },
		CurrentVersion:           func() string { return "v0.1.7" },
		CurrentOfficial:          func() bool { return true },
		CurrentTarget:            func() (string, string, error) { return "linux", "amd64", nil },
		VerifyChecksumsSignature: verifySignature,
	})

	if _, err := engine.InstallLatest(ctx); err == nil {
		t.Fatal("InstallLatest error = nil, want checksum failure")
	}
}

func TestEngineInstallLatestRejectsSignatureMismatch(t *testing.T) {
	ctx := context.Background()
	currentBinary := mustExecutableFile(t, []byte("old-binary"))
	server, _ := releaseTestServer(t, "v0.1.9", "linux", "amd64", []byte("new-binary"))
	engine := NewEngine(Config{
		HTTPClient:      server.Client(),
		InstallBaseURL:  server.URL,
		ReleaseBaseURL:  func(version string) string { return server.URL + "/download/" + version },
		ExecutablePath:  func() (string, error) { return currentBinary, nil },
		CurrentVersion:  func() string { return "v0.1.7" },
		CurrentOfficial: func() bool { return true },
		CurrentTarget:   func() (string, string, error) { return "linux", "amd64", nil },
		VerifyChecksumsSignature: func(_, _ []byte) error {
			return fmt.Errorf("invalid checksums signature")
		},
	})

	if _, err := engine.InstallLatest(ctx); err == nil {
		t.Fatal("InstallLatest error = nil, want signature failure")
	}
}

func TestEngineInstallLatestAbortsBeforeReplaceWhenStateHookFails(t *testing.T) {
	ctx := context.Background()
	currentBinary := mustExecutableFile(t, []byte("old-binary"))
	server, verifySignature := releaseTestServer(t, "v0.1.9", "linux", "amd64", []byte("new-binary"))
	engine := NewEngine(Config{
		HTTPClient:      server.Client(),
		InstallBaseURL:  server.URL,
		ReleaseBaseURL:  func(version string) string { return server.URL + "/download/" + version },
		ExecutablePath:  func() (string, error) { return currentBinary, nil },
		CurrentVersion:  func() string { return "v0.1.7" },
		CurrentOfficial: func() bool { return true },
		CurrentTarget:   func() (string, string, error) { return "linux", "amd64", nil },
		BeforeReplace: func(InstallResult) error {
			return fmt.Errorf("save updater state")
		},
		VerifyChecksumsSignature: verifySignature,
	})

	if _, err := engine.InstallLatest(ctx); err == nil {
		t.Fatal("InstallLatest error = nil, want beforeReplace failure")
	}

	payload, err := os.ReadFile(currentBinary)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(payload) != "old-binary" {
		t.Fatalf("binary payload = %q, want unchanged old-binary", payload)
	}
}

func TestEngineInstallLatestRestoresStateWhenReplaceFails(t *testing.T) {
	ctx := context.Background()
	currentBinary := mustExecutableFile(t, []byte("old-binary"))
	server, verifySignature := releaseTestServer(t, "v0.1.9", "linux", "amd64", []byte("new-binary"))

	beforeCalled := false
	restoreCalled := false
	engine := NewEngine(Config{
		HTTPClient:     server.Client(),
		InstallBaseURL: server.URL,
		ReleaseBaseURL: func(version string) string { return server.URL + "/download/" + version },
		ExecutablePath: func() (string, error) { return currentBinary, nil },
		ReplaceExecutable: func(string, []byte, os.FileMode) error {
			return fmt.Errorf("disk full")
		},
		CurrentVersion:  func() string { return "v0.1.7" },
		CurrentOfficial: func() bool { return true },
		CurrentTarget:   func() (string, string, error) { return "linux", "amd64", nil },
		BeforeReplace: func(result InstallResult) error {
			beforeCalled = true
			if result.RollbackVersion != "v0.1.7" {
				t.Fatalf("RollbackVersion = %q, want v0.1.7", result.RollbackVersion)
			}
			return nil
		},
		OnReplaceFailure: func(result InstallResult) error {
			restoreCalled = true
			if result.RollbackVersion != "v0.1.7" {
				t.Fatalf("RollbackVersion = %q during restore, want v0.1.7", result.RollbackVersion)
			}
			return nil
		},
		VerifyChecksumsSignature: verifySignature,
	})

	if _, err := engine.InstallLatest(ctx); err == nil {
		t.Fatal("InstallLatest error = nil, want replace failure")
	}
	if !beforeCalled {
		t.Fatal("BeforeReplace was not called")
	}
	if !restoreCalled {
		t.Fatal("OnReplaceFailure was not called")
	}
}

func releaseTestServer(t *testing.T, version, goos, goarch string, binaryPayload []byte) (*httptest.Server, func([]byte, []byte) error) {
	t.Helper()

	assetName := releaseAssetName(version, goos, goarch)
	archivePayload := mustReleaseArchive(t, binaryPayload)
	archiveChecksum := sha256.Sum256(archivePayload)
	checksumsPayload := []byte(fmt.Sprintf("%s  %s\n", hex.EncodeToString(archiveChecksum[:]), assetName))
	corruptChecksumsPayload := []byte(fmt.Sprintf("%s  %s\n", hex.EncodeToString(bytes.Repeat([]byte{'0'}, sha256.Size))[:64], assetName))
	signingPublicKey, signingKey, checksumsSignaturePayload := mustReleaseSignature(t, checksumsPayload)
	corruptChecksumsSignaturePayload := mustSignReleasePayload(t, signingKey, corruptChecksumsPayload)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest.json":
			_, _ = w.Write([]byte(fmt.Sprintf(`{"version":"%s","compatibility_line":"%s"}`, version, "0.1")))
		case "/download/" + version + "/" + assetName:
			_, _ = w.Write(archivePayload)
		case "/download/" + version + "/" + checksumsFileName:
			_, _ = w.Write(checksumsPayload)
		case "/download/" + version + "/" + checksumsSignatureFileName:
			_, _ = w.Write(checksumsSignaturePayload)
		case "/corrupt/" + version + "/" + assetName:
			_, _ = w.Write(archivePayload)
		case "/corrupt/" + version + "/" + checksumsFileName:
			_, _ = w.Write(corruptChecksumsPayload)
		case "/corrupt/" + version + "/" + checksumsSignatureFileName:
			_, _ = w.Write(corruptChecksumsSignaturePayload)
		default:
			http.NotFound(w, r)
		}
	}))
	return server, verifyChecksumsSignatureWithPublicKeyBase64(signingPublicKey)
}

func mustExecutableFile(t *testing.T, payload []byte) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "tunnel")
	if err := os.WriteFile(path, payload, 0o755); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	return path
}

func mustReleaseArchive(t *testing.T, binaryPayload []byte) []byte {
	t.Helper()

	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)

	header := &tar.Header{
		Name: "tunnel",
		Mode: 0o755,
		Size: int64(len(binaryPayload)),
	}
	if err := tarWriter.WriteHeader(header); err != nil {
		t.Fatalf("WriteHeader returned error: %v", err)
	}
	if _, err := tarWriter.Write(binaryPayload); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("tarWriter.Close returned error: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("gzipWriter.Close returned error: %v", err)
	}
	return archive.Bytes()
}

func mustReleaseSignature(t *testing.T, payload []byte) (string, ed25519.PrivateKey, []byte) {
	t.Helper()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey returned error: %v", err)
	}
	sshPublicKey, err := ssh.NewPublicKey(publicKey)
	if err != nil {
		t.Fatalf("NewPublicKey returned error: %v", err)
	}
	fields := strings.Fields(strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPublicKey))))
	if len(fields) < 2 {
		t.Fatal("MarshalAuthorizedKey returned malformed key")
	}
	return fields[1], privateKey, mustSignReleasePayload(t, privateKey, payload)
}

func mustSignReleasePayload(t *testing.T, privateKey ed25519.PrivateKey, payload []byte) []byte {
	t.Helper()
	publicKey, err := ssh.NewPublicKey(privateKey.Public())
	if err != nil {
		t.Fatalf("NewPublicKey returned error: %v", err)
	}
	signer, err := ssh.NewSignerFromSigner(privateKey)
	if err != nil {
		t.Fatalf("NewSignerFromSigner returned error: %v", err)
	}

	signedData := buildSSHSIGSignedData(payload, officialReleaseSignatureNamespace, "", officialReleaseSignatureHashAlgorithm)
	signature, err := signer.Sign(rand.Reader, signedData)
	if err != nil {
		t.Fatalf("Sign returned error: %v", err)
	}

	raw := append([]byte(officialReleaseSignatureMagicPreamble), ssh.Marshal(sshsigBlob{
		Version:       officialReleaseSignatureVersion,
		PublicKey:     publicKey.Marshal(),
		Namespace:     officialReleaseSignatureNamespace,
		Reserved:      "",
		HashAlgorithm: officialReleaseSignatureHashAlgorithm,
		Signature: ssh.Marshal(sshWireSignature{
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
	return []byte(builder.String())
}
