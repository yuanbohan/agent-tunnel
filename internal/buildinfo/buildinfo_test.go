package buildinfo

import "testing"

func TestStringReturnsDefaultVersionWhenUnset(t *testing.T) {
	oldVersion := Version
	Version = ""
	t.Cleanup(func() { Version = oldVersion })

	if got := String(); got != defaultVersion {
		t.Fatalf("String() = %q, want %q", got, defaultVersion)
	}
}

func TestDistributionDefaultsToNonRelease(t *testing.T) {
	oldDistribution := DistributionMarker
	DistributionMarker = ""
	t.Cleanup(func() { DistributionMarker = oldDistribution })

	if got := DistributionString(); got != DistributionNonRelease {
		t.Fatalf("DistributionString() = %q, want %q", got, DistributionNonRelease)
	}
	if IsOfficialRelease() {
		t.Fatal("IsOfficialRelease() = true, want false")
	}
}

func TestDistributionRecognizesOfficialRelease(t *testing.T) {
	oldDistribution := DistributionMarker
	DistributionMarker = string(DistributionOfficialRelease)
	t.Cleanup(func() { DistributionMarker = oldDistribution })

	if got := DistributionString(); got != DistributionOfficialRelease {
		t.Fatalf("DistributionString() = %q, want %q", got, DistributionOfficialRelease)
	}
	if !IsOfficialRelease() {
		t.Fatal("IsOfficialRelease() = false, want true")
	}
}

func TestMajorUsesSemanticMajorForVersionContract(t *testing.T) {
	cases := []struct {
		name    string
		version string
		want    string
	}{
		{name: "default", version: "v0.1.0-dev", want: "0"},
		{name: "release", version: "v1.2.3", want: "1"},
		{name: "no-v-prefix", version: "2.0.1", want: "2"},
		{name: "dash-only", version: "3-dev", want: "3"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MajorOf(tc.version); got != tc.want {
				t.Fatalf("MajorOf(%q) = %q, want %q", tc.version, got, tc.want)
			}
		})
	}
}

func TestCompatibilityLineUsesPreOneMinorLine(t *testing.T) {
	cases := []struct {
		name    string
		version string
		want    string
	}{
		{name: "default", version: "v0.1.0-dev", want: "0.1"},
		{name: "pre-one-release", version: "v0.3.7", want: "0.3"},
		{name: "stable-release", version: "v1.2.3", want: "1"},
		{name: "no-v-prefix", version: "2.4.0", want: "2"},
		{name: "dash-only", version: "3-dev", want: "3"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CompatibilityLineOf(tc.version); got != tc.want {
				t.Fatalf("CompatibilityLineOf(%q) = %q, want %q", tc.version, got, tc.want)
			}
		})
	}
}
