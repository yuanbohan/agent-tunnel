package buildinfo

import "strings"

const defaultVersion = "v0.1.0-dev"

type Distribution string

const (
	DistributionNonRelease      Distribution = "non-release"
	DistributionOfficialRelease Distribution = "official-release"
)

var (
	// Version is the current version of the application.
	Version            = defaultVersion
	// DistributionMarker indicates if this is an official release or a development build.
	DistributionMarker = string(DistributionNonRelease)
	// GitBranch is the git branch the binary was built from.
	GitBranch          = "unknown"
	// GitCommit is the git commit SHA the binary was built from.
	GitCommit          = "unknown"
	// BuildTime is the UTC timestamp when the binary was built.
	BuildTime          = "unknown"
)

// String returns the current version as a trimmed string, defaulting if empty.
func String() string {
	trimmed := strings.TrimSpace(Version)
	if trimmed == "" {
		return defaultVersion
	}
	return trimmed
}

// Major returns the major version part of the current application version.
func Major() string {
	return MajorOf(String())
}

// CompatibilityLine returns the version compatibility line (major.minor for 0.x, else major).
func CompatibilityLine() string {
	return CompatibilityLineOf(String())
}

// DistributionString returns the current distribution marker as a Distribution type.
func DistributionString() Distribution {
	switch Distribution(strings.TrimSpace(DistributionMarker)) {
	case DistributionOfficialRelease:
		return DistributionOfficialRelease
	default:
		return DistributionNonRelease
	}
}

// IsOfficialRelease returns true if the current build is an official release.
func IsOfficialRelease() bool {
	return DistributionString() == DistributionOfficialRelease
}

// MajorOf extracts the major version part from the provided version string.
func MajorOf(version string) string {
	return majorOf(version)
}

// CompatibilityLineOf extracts the compatibility line from the provided version string.
func CompatibilityLineOf(version string) string {
	return compatibilityLineOf(version)
}

func majorOf(version string) string {
	trimmed := canonicalize(version)
	if trimmed == "" {
		return "0"
	}
	if dot := strings.IndexByte(trimmed, '.'); dot >= 0 {
		return trimmed[:dot]
	}
	if dash := strings.IndexByte(trimmed, '-'); dash >= 0 {
		return trimmed[:dash]
	}
	return trimmed
}

func compatibilityLineOf(version string) string {
	trimmed := canonicalize(version)
	if trimmed == "" {
		return "0"
	}

	major := trimmed
	minor := ""
	if dot := strings.IndexByte(trimmed, '.'); dot >= 0 {
		major = trimmed[:dot]
		rest := trimmed[dot+1:]
		if nextDot := strings.IndexByte(rest, '.'); nextDot >= 0 {
			minor = rest[:nextDot]
		} else {
			minor = rest
		}
	}

	if major == "0" && minor != "" {
		return major + "." + minor
	}
	return major
}

func canonicalize(version string) string {
	trimmed := strings.TrimSpace(version)
	trimmed = strings.TrimPrefix(trimmed, "v")
	if dash := strings.IndexByte(trimmed, '-'); dash >= 0 {
		trimmed = trimmed[:dash]
	}
	return trimmed
}
