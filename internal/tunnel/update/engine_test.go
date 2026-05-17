package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestEngineInstallLatestOfficialRelease(t *testing.T) {
	ctx := context.Background()
	currentBinary := mustExecutableFile(t, []byte("old-binary"))
	newBinary := []byte("new-binary")
	server := releaseTestServer(t, "v0.1.9", "linux", "amd64", newBinary)
	engine := NewEngine(Config{
		HTTPClient:     server.Client(),
		InstallBaseURL: server.URL,
		ReleaseBaseURL: func(version string) string { return server.URL + "/download/" + version },
		ExecutablePath: func() (string, error) { return currentBinary, nil },
		CurrentVersion: func() string { return "v0.1.7" },
		CurrentOfficial: func() bool {
			return true
		},
		CurrentTarget: func() (string, string, error) { return "linux", "amd64", nil },
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
	server := releaseTestServer(t, "v0.1.9", "linux", "amd64", newBinary)
	engine := NewEngine(Config{
		HTTPClient:     server.Client(),
		InstallBaseURL: server.URL,
		ReleaseBaseURL: func(version string) string { return server.URL + "/download/" + version },
		ExecutablePath: func() (string, error) { return currentBinary, nil },
		CurrentVersion: func() string { return "v0.1.9-dev" },
		CurrentOfficial: func() bool {
			return false
		},
		CurrentTarget: func() (string, string, error) { return "linux", "amd64", nil },
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
	currentPayload := []byte("current-binary")
	currentBinary := mustExecutableFile(t, currentPayload)
	server := releaseTestServer(t, "v0.1.9", "linux", "amd64", []byte("should-not-install"))
	engine := NewEngine(Config{
		HTTPClient:     server.Client(),
		InstallBaseURL: server.URL,
		ReleaseBaseURL: func(version string) string { return server.URL + "/download/" + version },
		ExecutablePath: func() (string, error) { return currentBinary, nil },
		CurrentVersion: func() string { return "v0.1.9" },
		CurrentOfficial: func() bool {
			return true
		},
		CurrentTarget: func() (string, string, error) { return "linux", "amd64", nil },
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
	if string(payload) != string(currentPayload) {
		t.Fatalf("current binary = %q, want unchanged %q", payload, currentPayload)
	}
}

func TestEngineInstallLatestResolvesExecutableSymlink(t *testing.T) {
	ctx := context.Background()
	targetBinary := mustExecutableFile(t, []byte("old-binary"))
	linkPath := filepath.Join(t.TempDir(), "tunnel-link")
	if err := os.Symlink(targetBinary, linkPath); err != nil {
		t.Fatalf("Symlink returned error: %v", err)
	}
	newBinary := []byte("new-binary")
	server := releaseTestServer(t, "v0.1.9", "linux", "amd64", newBinary)
	engine := NewEngine(Config{
		HTTPClient:     server.Client(),
		InstallBaseURL: server.URL,
		ReleaseBaseURL: func(version string) string { return server.URL + "/download/" + version },
		ExecutablePath: func() (string, error) { return linkPath, nil },
		CurrentVersion: func() string { return "v0.1.7" },
		CurrentOfficial: func() bool {
			return true
		},
		CurrentTarget: func() (string, string, error) { return "linux", "amd64", nil },
	})

	result, err := engine.InstallLatest(ctx)
	if err != nil {
		t.Fatalf("InstallLatest returned error: %v", err)
	}
	if !result.Updated {
		t.Fatal("Updated = false, want true")
	}
	linkInfo, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("Lstat returned error: %v", err)
	}
	if linkInfo.Mode()&os.ModeSymlink == 0 {
		t.Fatal("link path was replaced, want symlink preserved")
	}
	payload, err := os.ReadFile(targetBinary)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(payload) != string(newBinary) {
		t.Fatalf("target binary = %q, want %q", payload, newBinary)
	}
}

func TestEngineInstallLatestRejectsChecksumMismatch(t *testing.T) {
	ctx := context.Background()
	currentBinary := mustExecutableFile(t, []byte("old-binary"))
	server := releaseTestServer(t, "v0.1.9", "linux", "amd64", []byte("new-binary"))
	engine := NewEngine(Config{
		HTTPClient:     server.Client(),
		InstallBaseURL: server.URL,
		ReleaseBaseURL: func(version string) string { return server.URL + "/corrupt/" + version },
		ExecutablePath: func() (string, error) { return currentBinary, nil },
		CurrentVersion: func() string { return "v0.1.7" },
		CurrentOfficial: func() bool {
			return true
		},
		CurrentTarget: func() (string, string, error) { return "linux", "amd64", nil },
	})

	if _, err := engine.InstallLatest(ctx); err == nil {
		t.Fatal("InstallLatest error = nil, want checksum failure")
	}
}

func TestEngineUpdateAvailableRejectsMissingChecksums(t *testing.T) {
	ctx := context.Background()
	server := releaseTestServer(t, "v0.1.9", "linux", "amd64", []byte("new-binary"))
	engine := NewEngine(Config{
		HTTPClient:     server.Client(),
		InstallBaseURL: server.URL,
		ReleaseBaseURL: func(version string) string { return server.URL + "/missing/" + version },
		CurrentVersion: func() string { return "v0.1.7" },
		CurrentOfficial: func() bool {
			return true
		},
		CurrentTarget: func() (string, string, error) { return "linux", "amd64", nil },
	})

	if _, _, err := engine.UpdateAvailable(ctx); err == nil {
		t.Fatal("UpdateAvailable error = nil, want release metadata failure")
	}
}

func TestEngineInstallLatestAbortsBeforeReplaceWhenStateHookFails(t *testing.T) {
	ctx := context.Background()
	currentBinary := mustExecutableFile(t, []byte("old-binary"))
	server := releaseTestServer(t, "v0.1.9", "linux", "amd64", []byte("new-binary"))
	engine := NewEngine(Config{
		HTTPClient:     server.Client(),
		InstallBaseURL: server.URL,
		ReleaseBaseURL: func(version string) string { return server.URL + "/download/" + version },
		ExecutablePath: func() (string, error) { return currentBinary, nil },
		CurrentVersion: func() string { return "v0.1.7" },
		CurrentOfficial: func() bool {
			return true
		},
		CurrentTarget: func() (string, string, error) { return "linux", "amd64", nil },
		BeforeReplace: func(InstallResult) error {
			return fmt.Errorf("save updater state")
		},
	})

	if _, err := engine.InstallLatest(ctx); err == nil {
		t.Fatal("InstallLatest error = nil, want beforeReplace failure")
	}
}

func TestEngineInstallLatestRestoresStateWhenReplaceFails(t *testing.T) {
	ctx := context.Background()
	currentBinary := mustExecutableFile(t, []byte("old-binary"))
	server := releaseTestServer(t, "v0.1.9", "linux", "amd64", []byte("new-binary"))

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
		CurrentVersion: func() string { return "v0.1.7" },
		CurrentOfficial: func() bool {
			return true
		},
		CurrentTarget: func() (string, string, error) { return "linux", "amd64", nil },
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

func releaseTestServer(t *testing.T, version, goos, goarch string, binaryPayload []byte) *httptest.Server {
	t.Helper()

	assetName := releaseAssetName(version, goos, goarch)
	archivePayload := mustReleaseArchive(t, binaryPayload)
	archiveChecksum := sha256.Sum256(archivePayload)
	checksumsPayload := []byte(fmt.Sprintf("%s  %s\n", hex.EncodeToString(archiveChecksum[:]), assetName))
	corruptChecksumsPayload := []byte(fmt.Sprintf("%s  %s\n", hex.EncodeToString(bytes.Repeat([]byte{'0'}, sha256.Size))[:64], assetName))
	latestManifestPayload := []byte(fmt.Sprintf(`{"version":"%s"}`, version))
	badLatestManifestPayload := []byte(`{}`)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest.json":
			_, _ = w.Write(latestManifestPayload)
		case "/bad-latest/" + releaseLatestManifestFileName():
			_, _ = w.Write(badLatestManifestPayload)
		case "/download/" + version + "/" + assetName:
			_, _ = w.Write(archivePayload)
		case "/download/" + version + "/" + checksumsFileName:
			_, _ = w.Write(checksumsPayload)
		case "/corrupt/" + version + "/" + assetName:
			_, _ = w.Write(archivePayload)
		case "/corrupt/" + version + "/" + checksumsFileName:
			_, _ = w.Write(corruptChecksumsPayload)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
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
