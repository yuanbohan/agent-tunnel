package buildinfo

import (
	"strings"
	"time"
)

const defaultVersion = "v0.1.0-dev"

type Distribution string

const (
	DistributionNonRelease      Distribution = "non-release"
	DistributionOfficialRelease Distribution = "official-release"
)

var (
	// Version is the current version of the application.
	Version = defaultVersion
	// DistributionMarker indicates if this is an official release or a development build.
	DistributionMarker = string(DistributionNonRelease)
	// GitBranch is the git branch the binary was built from.
	GitBranch = "unknown"
	// GitCommit is the git commit SHA the binary was built from.
	GitCommit = "unknown"
	// BuildTime is the UTC timestamp when the binary was built.
	BuildTime = "unknown"
)

// String returns the current version as a trimmed string, defaulting if empty.
func String() string {
	trimmed := strings.TrimSpace(Version)
	if trimmed == "" {
		return defaultVersion
	}
	return trimmed
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

// BuildTimeUnix returns the build time as Unix seconds.
func BuildTimeUnix() int64 {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(BuildTime))
	if err != nil {
		return 0
	}
	return t.Unix()
}
