package update

import (
	"runtime"
	"testing"
)

func TestParseLatestManifest(t *testing.T) {
	manifest, err := parseLatestManifest([]byte(`{"version":"v0.1.9"}`))
	if err != nil {
		t.Fatalf("parseLatestManifest returned error: %v", err)
	}
	if manifest.Version != "v0.1.9" {
		t.Fatalf("Version = %q, want v0.1.9", manifest.Version)
	}
}

func TestParseLatestManifestRejectsMissingVersion(t *testing.T) {
	_, err := parseLatestManifest([]byte(`{}`))
	if err == nil {
		t.Fatal("parseLatestManifest error = nil, want version error")
	}
}

func TestReleaseAssetName(t *testing.T) {
	got := releaseAssetName("v1.2.3", "darwin", "arm64")
	if got != "tunnel_v1.2.3_darwin_arm64.tar.gz" {
		t.Fatalf("releaseAssetName() = %q, want tunnel_v1.2.3_darwin_arm64.tar.gz", got)
	}
}

func TestSupportedReleaseTarget(t *testing.T) {
	cases := []struct {
		goos   string
		goarch string
		want   bool
	}{
		{goos: "darwin", goarch: "arm64", want: true},
		{goos: "darwin", goarch: "amd64", want: true},
		{goos: "linux", goarch: "amd64", want: true},
		{goos: "linux", goarch: "arm64", want: true},
		{goos: "windows", goarch: "amd64", want: false},
		{goos: "linux", goarch: "386", want: false},
	}

	for _, tc := range cases {
		if got := supportedReleaseTarget(tc.goos, tc.goarch); got != tc.want {
			t.Fatalf("supportedReleaseTarget(%q, %q) = %v, want %v", tc.goos, tc.goarch, got, tc.want)
		}
	}
}

func TestCurrentReleaseTargetMatchesRuntimeWhenSupported(t *testing.T) {
	goos, goarch, err := currentReleaseTarget()
	if supportedReleaseTarget(runtime.GOOS, runtime.GOARCH) {
		if err != nil {
			t.Fatalf("currentReleaseTarget returned error on supported runtime: %v", err)
		}
		if goos != runtime.GOOS || goarch != runtime.GOARCH {
			t.Fatalf("currentReleaseTarget = %s/%s, want %s/%s", goos, goarch, runtime.GOOS, runtime.GOARCH)
		}
		return
	}

	if err == nil {
		t.Fatalf("currentReleaseTarget error = nil on unsupported runtime %s/%s", runtime.GOOS, runtime.GOARCH)
	}
}

func TestIsNewerReleaseVersion(t *testing.T) {
	cases := []struct {
		latest  string
		current string
		want    bool
	}{
		{latest: "v0.1.9", current: "v0.1.7", want: true},
		{latest: "v0.1.7", current: "v0.1.7", want: false},
		{latest: "v0.1.6", current: "v0.1.7", want: false},
		{latest: "v1.0.0", current: "v0.9.9", want: true},
	}

	for _, tc := range cases {
		got, err := isNewerReleaseVersion(tc.latest, tc.current)
		if err != nil {
			t.Fatalf("isNewerReleaseVersion(%q, %q) returned error: %v", tc.latest, tc.current, err)
		}
		if got != tc.want {
			t.Fatalf("isNewerReleaseVersion(%q, %q) = %v, want %v", tc.latest, tc.current, got, tc.want)
		}
	}
}
