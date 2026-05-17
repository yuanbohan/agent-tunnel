#!/bin/sh

release_validate_version() {
	version="${1:-}"
	if printf '%s\n' "$version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
		return 0
	fi
	printf 'error: version must look like v0.1.0\n' >&2
	return 1
}

release_validate_product() {
	product="${1:-}"
	case "$product" in
		tunnel|relay)
			return 0
			;;
	esac
	printf 'error: product must be tunnel or relay\n' >&2
	return 1
}

release_source_tag() {
	product="$1"
	version="$2"
	release_validate_product "$product" || return 1
	release_validate_version "$version" || return 1
	printf '%s-%s\n' "$product" "$version"
}

release_validate_source_tag() {
	tag="${1:-}"
	if printf '%s\n' "$tag" | grep -Eq '^(tunnel|relay)-v[0-9]+\.[0-9]+\.[0-9]+$'; then
		return 0
	fi
	printf 'error: source tag must look like tunnel-v0.1.0 or relay-v0.1.0\n' >&2
	return 1
}

release_source_tag_product() {
	tag="$1"
	release_validate_source_tag "$tag" || return 1
	printf '%s\n' "${tag%%-v*}"
}

release_source_tag_version() {
	tag="$1"
	release_validate_source_tag "$tag" || return 1
	printf 'v%s\n' "${tag#*-v}"
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
