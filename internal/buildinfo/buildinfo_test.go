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
			oldVersion := Version
			Version = tc.version
			t.Cleanup(func() { Version = oldVersion })

			if got := Major(); got != tc.want {
				t.Fatalf("Major() = %q, want %q", got, tc.want)
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
			oldVersion := Version
			Version = tc.version
			t.Cleanup(func() { Version = oldVersion })

			if got := CompatibilityLine(); got != tc.want {
				t.Fatalf("CompatibilityLine() = %q, want %q", got, tc.want)
			}
		})
	}
}
