package update

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"

	"yuanbohan/tunnel/internal/buildinfo"
)

const (
	checksumsFileName          = "checksums.txt"
	checksumsSignatureFileName = "checksums.txt.sig"
)

type LatestManifest struct {
	Version           string `json:"version"`
	CompatibilityLine string `json:"compatibility_line"`
}

type semver struct {
	major int
	minor int
	patch int
}

func parseLatestManifest(payload []byte) (LatestManifest, error) {
	var manifest LatestManifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		return LatestManifest{}, fmt.Errorf("parse latest manifest: %w", err)
	}

	manifest.Version = strings.TrimSpace(manifest.Version)
	if manifest.Version == "" {
		return LatestManifest{}, fmt.Errorf("latest manifest did not contain version")
	}
	manifest.CompatibilityLine = strings.TrimSpace(manifest.CompatibilityLine)
	if manifest.CompatibilityLine == "" {
		return LatestManifest{}, fmt.Errorf("latest manifest did not contain compatibility_line")
	}

	expectedLine := buildinfo.CompatibilityLineOf(manifest.Version)
	if manifest.CompatibilityLine != expectedLine {
		return LatestManifest{}, fmt.Errorf(
			"latest manifest compatibility_line %q does not match version %s",
			manifest.CompatibilityLine,
			manifest.Version,
		)
	}

	return manifest, nil
}

func releaseAssetName(version, goos, goarch string) string {
	return fmt.Sprintf("tunnel_%s_%s_%s.tar.gz", version, goos, goarch)
}

func releaseChecksumsFileName() string {
	return checksumsFileName
}

func releaseChecksumsSignatureFileName() string {
	return checksumsSignatureFileName
}

func currentReleaseTarget() (string, string, error) {
	if !supportedReleaseTarget(runtime.GOOS, runtime.GOARCH) {
		return "", "", fmt.Errorf("unsupported target %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return runtime.GOOS, runtime.GOARCH, nil
}

func supportedReleaseTarget(goos, goarch string) bool {
	switch goos + "/" + goarch {
	case "darwin/arm64", "darwin/amd64", "linux/amd64", "linux/arm64":
		return true
	default:
		return false
	}
}

func isNewerReleaseVersion(latest, current string) (bool, error) {
	latestVersion, err := parseReleaseSemver(latest)
	if err != nil {
		return false, err
	}
	currentVersion, err := parseReleaseSemver(current)
	if err != nil {
		return false, err
	}
	if latestVersion.major != currentVersion.major {
		return latestVersion.major > currentVersion.major, nil
	}
	if latestVersion.minor != currentVersion.minor {
		return latestVersion.minor > currentVersion.minor, nil
	}
	return latestVersion.patch > currentVersion.patch, nil
}

func parseReleaseSemver(raw string) (semver, error) {
	trimmed := strings.TrimSpace(raw)
	var version semver
	if _, err := fmt.Sscanf(trimmed, "v%d.%d.%d", &version.major, &version.minor, &version.patch); err != nil {
		return semver{}, fmt.Errorf("parse release version %q: %w", raw, err)
	}
	return version, nil
}
