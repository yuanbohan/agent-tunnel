#!/bin/sh

release_validate_version() {
	version="${1:-}"
	if printf '%s\n' "$version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
		return 0
	fi
	printf 'error: version must look like v0.1.0\n' >&2
	return 1
}

release_targets() {
	printf '%s\n' \
		'darwin arm64' \
		'darwin amd64' \
		'linux amd64' \
		'linux arm64'
}

release_asset_name() {
	version="$1"
	os="$2"
	arch="$3"
	printf 'tunnel_%s_%s_%s.tar.gz\n' "$version" "$os" "$arch"
}

release_compatibility_line() {
	version="${1#v}"
	version="${version%%-*}"
	major="${version%%.*}"
	if [ "$major" = "0" ]; then
		rest="${version#*.}"
		minor="${rest%%.*}"
		if [ -n "$minor" ]; then
			printf '%s.%s\n' "$major" "$minor"
			return 0
		fi
	fi
	printf '%s\n' "$major"
}

release_fixture_version() {
	line=$(release_compatibility_line "${1:-}")
	case "$line" in
		0.*)
			printf 'v%s.2\n' "$line"
			;;
		*)
			printf 'v%s.0.2\n' "$line"
			;;
	esac
}

release_incompatible_version() {
	line=$(release_compatibility_line "${1:-}")
	case "$line" in
		0.*)
			minor="${line#0.}"
			printf 'v0.%s.0\n' "$((minor + 1))"
			;;
		*)
			printf 'v%s.0.0\n' "$((line + 1))"
			;;
	esac
}

release_hash_file() {
	if command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{print $1}'
		return 0
	fi
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
		return 0
	fi
	printf 'error: need shasum or sha256sum to compute release checksums\n' >&2
	return 1
}
