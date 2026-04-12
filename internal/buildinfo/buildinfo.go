package buildinfo

import "strings"

const defaultVersion = "v0.1.0-dev"

var Version = defaultVersion

func String() string {
	trimmed := strings.TrimSpace(Version)
	if trimmed == "" {
		return defaultVersion
	}
	return trimmed
}

func Major() string {
	return majorOf(String())
}

func CompatibilityLine() string {
	return compatibilityLineOf(String())
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
