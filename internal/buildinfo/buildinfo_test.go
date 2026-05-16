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
