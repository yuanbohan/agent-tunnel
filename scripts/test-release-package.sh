#!/bin/sh

set -eu

script_dir=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/.." && pwd)

# shellcheck source=/dev/null
. "$script_dir/release-common.sh"

tmpdir=$(mktemp -d "${TMPDIR:-/tmp}/tunnel-release-test.XXXXXX")
cleanup() {
	rm -rf "$tmpdir"
	rm -rf "${probe_dir:-}"
}
trap cleanup EXIT INT TERM

release_root="$tmpdir/releases"
go_bin="${GO:-go}"
version="${TEST_RELEASE_VERSION:-v0.1.2}"
output_dir="$release_root/$version"

GO="$go_bin" RELEASE_DIR="$release_root" "$script_dir/release-package.sh" "$version" >/dev/null

if [ ! -d "$output_dir" ]; then
	printf 'error: missing output dir %s\n' "$output_dir" >&2
	exit 1
fi

archive_count=$(find "$output_dir" -maxdepth 1 -name 'tunnel_*.tar.gz' | wc -l | tr -d ' ')
if [ "$archive_count" != "4" ]; then
	printf 'error: archive count = %s, want 4\n' "$archive_count" >&2
	exit 1
fi

if [ ! -f "$output_dir/checksums.txt" ]; then
	printf 'error: missing checksums.txt\n' >&2
	exit 1
fi
if find "$output_dir" -maxdepth 1 -name '*.sig' | grep -q .; then
	printf 'error: unexpected .sig artifacts in %s\n' "$output_dir" >&2
	exit 1
fi

checksum_lines=$(wc -l <"$output_dir/checksums.txt" | tr -d ' ')
if [ "$checksum_lines" != "4" ]; then
	printf 'error: checksums line count = %s, want 4\n' "$checksum_lines" >&2
	exit 1
fi

release_targets | while IFS=' ' read -r os arch; do
	archive_name=$(release_asset_name "$version" "$os" "$arch")
	archive_path="$output_dir/$archive_name"
	if [ ! -f "$archive_path" ]; then
		printf 'error: missing archive %s\n' "$archive_name" >&2
		exit 1
	fi
	if ! grep -q "  $archive_name\$" "$output_dir/checksums.txt"; then
		printf 'error: checksums.txt missing %s\n' "$archive_name" >&2
		exit 1
	fi
	if [ "$(tar -tzf "$archive_path")" != "tunnel" ]; then
		printf 'error: archive %s should contain only tunnel\n' "$archive_name" >&2
		exit 1
	fi
done

current_os=$("$go_bin" env GOOS)
current_arch=$("$go_bin" env GOARCH)
current_archive="$output_dir/$(release_asset_name "$version" "$current_os" "$current_arch")"
extract_dir="$tmpdir/current"
mkdir -p "$extract_dir"
tar -xzf "$current_archive" -C "$extract_dir"

if [ "$("$extract_dir/tunnel" --version)" != "tunnel $version" ]; then
	printf 'error: packaged tunnel version output mismatch\n' >&2
	exit 1
fi

probe_dir="$repo_root/.tmp-release-package-probe"
mkdir -p "$probe_dir"
cat >"$probe_dir/main.go" <<'EOF'
package main

import (
	"fmt"

	"yuanbohan/tunnel/internal/buildinfo"
)

func main() {
	fmt.Println(buildinfo.DistributionString())
}
EOF

probe_output=$(cd "$repo_root" && "$go_bin" run \
	-trimpath \
	-ldflags="-X yuanbohan/tunnel/internal/buildinfo.Version=$version -X yuanbohan/tunnel/internal/buildinfo.DistributionMarker=official-release" \
	"$probe_dir/main.go")
rm -rf "$probe_dir"

if [ "$probe_output" != "official-release" ]; then
	printf 'error: official-release ldflags did not set distribution marker\n' >&2
	exit 1
fi

if GO="$go_bin" RELEASE_DIR="$release_root" "$script_dir/release-package.sh" "v0.1.2foo" >/dev/null 2>"$tmpdir/invalid-version.err"
then
	printf 'error: invalid version unexpectedly passed validation\n' >&2
	exit 1
fi

if ! grep -q 'version must look like v0.1.0' "$tmpdir/invalid-version.err"; then
	printf 'error: invalid version path did not explain failure\n' >&2
	exit 1
fi

if GO="$go_bin" RELEASE_DIR="$release_root" "$script_dir/release-package.sh" "tunnel-$version" >/dev/null 2>"$tmpdir/prefixed-version.err"
then
	printf 'error: product-prefixed version unexpectedly packaged\n' >&2
	exit 1
fi

if ! grep -q 'version must look like v0.1.0' "$tmpdir/prefixed-version.err"; then
	printf 'error: product-prefixed version path did not explain failure\n' >&2
	exit 1
fi

printf 'release packaging smoke tests passed\n'
