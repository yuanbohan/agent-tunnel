#!/bin/sh

set -eu

usage() {
	printf 'usage: %s <version>\n' "$0" >&2
}

script_dir=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/.." && pwd)

# shellcheck source=/dev/null
. "$script_dir/release-common.sh"

version="${1:-}"
if [ -z "$version" ]; then
	usage
	exit 1
fi
release_validate_version "$version"

go_bin="${GO:-go}"
release_root="${RELEASE_DIR:-$repo_root/dist/releases}"
output_dir="$release_root/$version"
release_signing_key="${TUNNEL_RELEASE_SIGNING_PRIVATE_KEY:-}"
trusted_signing_public_key="${TUNNEL_RELEASE_SIGNING_PUBLIC_KEY:-}"
stage_dir=$(mktemp -d "${TMPDIR:-/tmp}/tunnel-release.XXXXXX")
cleanup() {
	rm -rf "$stage_dir"
}
trap cleanup EXIT INT TERM

repo_relay_version=$("$go_bin" run ./cmd/relay version | awk 'NR==1 {print $2}')
if [ -z "$repo_relay_version" ]; then
	printf 'error: could not determine relay compatibility line from current repo\n' >&2
	exit 1
fi
release_line=$(release_compatibility_line "$version")
relay_line=$(release_compatibility_line "$repo_relay_version")
if [ "$release_line" != "$relay_line" ]; then
	printf 'error: release version %s is outside the current relay compatibility line %s\n' "$version" "$relay_line" >&2
	exit 1
fi
if [ -z "$release_signing_key" ]; then
	printf 'error: TUNNEL_RELEASE_SIGNING_PRIVATE_KEY is required to package signed release artifacts\n' >&2
	exit 1
fi
if [ -z "$trusted_signing_public_key" ]; then
	trusted_signing_public_key=$("$go_bin" run ./cmd/release-sign trusted-public-key)
fi
derived_signing_public_key=$(TUNNEL_RELEASE_SIGNING_PRIVATE_KEY="$release_signing_key" "$go_bin" run ./cmd/release-sign public-key)
if [ "$derived_signing_public_key" != "$trusted_signing_public_key" ]; then
	printf 'error: TUNNEL_RELEASE_SIGNING_PRIVATE_KEY does not match the trusted release signing public key\n' >&2
	exit 1
fi

rm -rf "$output_dir"
mkdir -p "$output_dir"

release_targets | while IFS=' ' read -r os arch; do
	target_dir="$stage_dir/$os-$arch"
	bin_path="$target_dir/tunnel"
	archive_path="$output_dir/$(release_asset_name "$version" "$os" "$arch")"
	ldflags="-s -w -X yuanbohan/tunnel/internal/buildinfo.Version=$version -X yuanbohan/tunnel/internal/buildinfo.DistributionMarker=official-release -X yuanbohan/tunnel/internal/tunnel/update.OfficialReleaseSigningPublicKeyBase64=$trusted_signing_public_key"

	mkdir -p "$target_dir"
	printf 'building tunnel for %s/%s...\n' "$os" "$arch"
	GOOS="$os" GOARCH="$arch" "$go_bin" build \
		-trimpath \
		-ldflags="$ldflags" \
		-o "$bin_path" \
		./cmd/tunnel

	if [ ! -f "$bin_path" ]; then
		printf 'error: failed to build tunnel for %s/%s\n' "$os" "$arch" >&2
		exit 1
	fi

	tar -C "$target_dir" -czf "$archive_path" tunnel
done

checksums_file="$output_dir/checksums.txt"
: >"$checksums_file"
release_targets | while IFS=' ' read -r os arch; do
	archive_name=$(release_asset_name "$version" "$os" "$arch")
	archive_path="$output_dir/$archive_name"
	printf '%s  %s\n' "$(release_hash_file "$archive_path")" "$archive_name" >>"$checksums_file"
done

TUNNEL_RELEASE_SIGNING_PRIVATE_KEY="$release_signing_key" "$go_bin" run ./cmd/release-sign sign "$checksums_file" "$output_dir/checksums.txt.sig"

printf 'packaged tunnel release assets in %s\n' "$output_dir"
