package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"yuanbohan/tunnel/internal/buildinfo"
)

const (
	defaultInstallBaseURL = "https://raw.githubusercontent.com/yuanbohan/tunnel/main"
	defaultReleaseRepo    = "yuanbohan/tunnel"
)

type httpClient interface {
	Do(*http.Request) (*http.Response, error)
}

type Config struct {
	HTTPClient               httpClient
	InstallBaseURL           string
	ReleaseBaseURL           func(version string) string
	ExecutablePath           func() (string, error)
	ReplaceExecutable        func(path string, payload []byte, mode os.FileMode) error
	CurrentVersion           func() string
	CurrentOfficial          func() bool
	CurrentTarget            func() (string, string, error)
	BeforeReplace            func(InstallResult) error
	OnReplaceFailure         func(InstallResult) error
	VerifyChecksumsSignature func(checksumsPayload, signaturePayload []byte) error
}

type Engine struct {
	httpClient               httpClient
	installBaseURL           string
	releaseBaseURL           func(version string) string
	executablePath           func() (string, error)
	replaceExecutable        func(path string, payload []byte, mode os.FileMode) error
	currentVersion           func() string
	currentOfficial          func() bool
	currentTarget            func() (string, string, error)
	beforeReplace            func(InstallResult) error
	onReplaceFailure         func(InstallResult) error
	verifyChecksumsSignature func(checksumsPayload, signaturePayload []byte) error
}

type InstallResult struct {
	Updated                   bool
	CurrentVersion            string
	InstalledVersion          string
	RollbackVersion           string
	RollbackAvailable         bool
	RollbackUnavailableReason string
	PreviousWasOfficial       bool
}

func NewEngine(cfg Config) *Engine {
	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	installBaseURL := strings.TrimRight(strings.TrimSpace(cfg.InstallBaseURL), "/")
	if installBaseURL == "" {
		installBaseURL = defaultInstallBaseURL
	}

	releaseBaseURL := cfg.ReleaseBaseURL
	if releaseBaseURL == nil {
		releaseBaseURL = func(version string) string {
			return fmt.Sprintf("https://github.com/%s/releases/download/%s", defaultReleaseRepo, version)
		}
	}

	executablePath := cfg.ExecutablePath
	if executablePath == nil {
		executablePath = os.Executable
	}
	replaceExecutableFn := cfg.ReplaceExecutable
	if replaceExecutableFn == nil {
		replaceExecutableFn = replaceExecutable
	}

	currentVersion := cfg.CurrentVersion
	if currentVersion == nil {
		currentVersion = buildinfo.String
	}

	currentOfficial := cfg.CurrentOfficial
	if currentOfficial == nil {
		currentOfficial = buildinfo.IsOfficialRelease
	}

	currentTarget := cfg.CurrentTarget
	if currentTarget == nil {
		currentTarget = currentReleaseTarget
	}

	verifyChecksumsSignature := cfg.VerifyChecksumsSignature
	if verifyChecksumsSignature == nil {
		verifyChecksumsSignature = verifyOfficialReleaseChecksumsSignature
	}

	return &Engine{
		httpClient:               client,
		installBaseURL:           installBaseURL,
		releaseBaseURL:           releaseBaseURL,
		executablePath:           executablePath,
		replaceExecutable:        replaceExecutableFn,
		currentVersion:           currentVersion,
		currentOfficial:          currentOfficial,
		currentTarget:            currentTarget,
		beforeReplace:            cfg.BeforeReplace,
		onReplaceFailure:         cfg.OnReplaceFailure,
		verifyChecksumsSignature: verifyChecksumsSignature,
	}
}

func (e *Engine) UpdateAvailable(ctx context.Context) (LatestManifest, bool, error) {
	manifest, err := e.fetchLatestRelease(ctx)
	if err != nil {
		return LatestManifest{}, false, err
	}
	if !e.currentOfficial() {
		return manifest, true, nil
	}
	newer, err := isNewerReleaseVersion(manifest.Version, e.currentVersion())
	if err != nil {
		return LatestManifest{}, false, err
	}
	return manifest, newer, nil
}

func (e *Engine) InstallLatest(ctx context.Context) (InstallResult, error) {
	manifest, available, err := e.UpdateAvailable(ctx)
	if err != nil {
		return InstallResult{}, err
	}

	result := InstallResult{
		CurrentVersion:   e.currentVersion(),
		InstalledVersion: manifest.Version,
	}
	if !available {
		return result, nil
	}

	return e.installVersion(ctx, manifest.Version)
}

func (e *Engine) InstallVersion(ctx context.Context, version string) (InstallResult, error) {
	version = strings.TrimSpace(version)
	if version == "" {
		return InstallResult{}, fmt.Errorf("install version must not be empty")
	}
	return e.installVersion(ctx, version)
}

func (e *Engine) fetchLatestRelease(ctx context.Context) (LatestManifest, error) {
	payload, err := e.fetchBytes(ctx, e.installBaseURL+"/latest.json")
	if err != nil {
		return LatestManifest{}, err
	}
	manifest, err := parseLatestManifest(payload)
	if err != nil {
		return LatestManifest{}, err
	}
	if err := e.verifySignedRelease(ctx, manifest.Version); err != nil {
		return LatestManifest{}, err
	}
	return manifest, nil
}

func (e *Engine) installVersion(ctx context.Context, version string) (InstallResult, error) {
	result := InstallResult{
		CurrentVersion:      e.currentVersion(),
		InstalledVersion:    version,
		PreviousWasOfficial: e.currentOfficial(),
	}
	if result.PreviousWasOfficial && version == result.CurrentVersion {
		return result, nil
	}

	goos, goarch, err := e.currentTarget()
	if err != nil {
		return InstallResult{}, err
	}
	assetName := releaseAssetName(version, goos, goarch)
	releaseBaseURL := strings.TrimRight(e.releaseBaseURL(version), "/")

	checksumsPayload, err := e.fetchVerifiedChecksums(ctx, releaseBaseURL)
	if err != nil {
		return InstallResult{}, err
	}
	archivePayload, err := e.fetchBytes(ctx, releaseBaseURL+"/"+assetName)
	if err != nil {
		return InstallResult{}, err
	}
	if err := verifyArchiveChecksum(assetName, archivePayload, checksumsPayload); err != nil {
		return InstallResult{}, err
	}
	binaryPayload, err := extractTunnelBinary(archivePayload)
	if err != nil {
		return InstallResult{}, err
	}

	targetPath, mode, err := resolveExecutableTarget(e.executablePath)
	if err != nil {
		return InstallResult{}, err
	}
	if result.PreviousWasOfficial {
		result.RollbackAvailable = true
		result.RollbackVersion = result.CurrentVersion
	} else {
		result.RollbackUnavailableReason = "rollback is unavailable because the previous build was not an official release"
	}
	if e.beforeReplace != nil {
		if err := e.beforeReplace(result); err != nil {
			return InstallResult{}, err
		}
	}
	if err := e.replaceExecutable(targetPath, binaryPayload, mode); err != nil {
		if e.onReplaceFailure != nil {
			if restoreErr := e.onReplaceFailure(result); restoreErr != nil {
				return InstallResult{}, fmt.Errorf("%w (also failed to restore updater state: %v)", err, restoreErr)
			}
		}
		return InstallResult{}, err
	}

	result.Updated = true
	return result, nil
}

func (e *Engine) verifySignedRelease(ctx context.Context, version string) error {
	releaseBaseURL := strings.TrimRight(e.releaseBaseURL(version), "/")
	if _, err := e.fetchVerifiedChecksums(ctx, releaseBaseURL); err != nil {
		return fmt.Errorf("verify signed release metadata for %s: %w", version, err)
	}
	return nil
}

func (e *Engine) fetchVerifiedChecksums(ctx context.Context, releaseBaseURL string) ([]byte, error) {
	checksumsPayload, err := e.fetchBytes(ctx, releaseBaseURL+"/"+releaseChecksumsFileName())
	if err != nil {
		return nil, err
	}
	checksumsSignaturePayload, err := e.fetchBytes(ctx, releaseBaseURL+"/"+releaseChecksumsSignatureFileName())
	if err != nil {
		return nil, err
	}
	if err := e.verifyChecksumsSignature(checksumsPayload, checksumsSignaturePayload); err != nil {
		return nil, fmt.Errorf("verify %s: %w", releaseChecksumsSignatureFileName(), err)
	}
	return checksumsPayload, nil
}

func (e *Engine) fetchBytes(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request for %s: %w", rawURL, err)
	}

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download %s: unexpected HTTP status %s", rawURL, resp.Status)
	}
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", rawURL, err)
	}
	return payload, nil
}

func verifyArchiveChecksum(assetName string, archivePayload, checksumsPayload []byte) error {
	expectedChecksum := ""
	for _, line := range strings.Split(string(checksumsPayload), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 2 {
			continue
		}
		if fields[1] == assetName {
			expectedChecksum = fields[0]
			break
		}
	}
	if expectedChecksum == "" {
		return fmt.Errorf("checksums.txt did not contain %s", assetName)
	}

	actual := sha256.Sum256(archivePayload)
	actualChecksum := hex.EncodeToString(actual[:])
	if actualChecksum != expectedChecksum {
		return fmt.Errorf("checksum mismatch for %s", assetName)
	}
	return nil
}

func extractTunnelBinary(archivePayload []byte) ([]byte, error) {
	gzipReader, err := gzip.NewReader(bytes.NewReader(archivePayload))
	if err != nil {
		return nil, fmt.Errorf("open archive gzip stream: %w", err)
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	var binaryPayload []byte
	entryCount := 0
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read archive entry: %w", err)
		}
		entryCount++
		if entryCount > 1 {
			return nil, fmt.Errorf("archive must contain only tunnel")
		}
		if header.Typeflag != tar.TypeReg || strings.TrimSpace(header.Name) != "tunnel" {
			return nil, fmt.Errorf("archive must contain only tunnel")
		}
		binaryPayload, err = io.ReadAll(tarReader)
		if err != nil {
			return nil, fmt.Errorf("read tunnel binary from archive: %w", err)
		}
	}
	if entryCount == 0 || len(binaryPayload) == 0 {
		return nil, fmt.Errorf("archive must contain only tunnel")
	}
	return binaryPayload, nil
}

func resolveExecutableTarget(resolve func() (string, error)) (string, os.FileMode, error) {
	path, err := resolve()
	if err != nil {
		return "", 0, fmt.Errorf("resolve executable path: %w", err)
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return "", 0, fmt.Errorf("resolve executable path: empty path")
	}

	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", 0, fmt.Errorf("resolve executable path %s: %w", path, err)
	}
	info, err := os.Stat(resolvedPath)
	if err != nil {
		return "", 0, fmt.Errorf("stat executable path %s: %w", resolvedPath, err)
	}
	if !info.Mode().IsRegular() {
		return "", 0, fmt.Errorf("self-update only supports regular executable files")
	}
	file, err := os.OpenFile(resolvedPath, os.O_WRONLY, 0)
	if err != nil {
		return "", 0, fmt.Errorf("self-update requires a writable executable at %s: %w", resolvedPath, err)
	}
	_ = file.Close()
	return resolvedPath, info.Mode().Perm(), nil
}

func replaceExecutable(path string, binaryPayload []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmpFile, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp executable for %s: %w", path, err)
	}
	tmpPath := tmpFile.Name()
	cleanup := func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
	}
	if _, err := tmpFile.Write(binaryPayload); err != nil {
		cleanup()
		return fmt.Errorf("write temp executable for %s: %w", path, err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp executable for %s: %w", path, err)
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod temp executable for %s: %w", path, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("replace executable %s: %w", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("chmod executable %s: %w", path, err)
	}
	return nil
}
