#!/bin/sh

set -eu

script_dir=$(CDPATH= cd -- "$(dirname "$0")" && pwd)

# shellcheck source=/dev/null
. "$script_dir/release-common.sh"

tmpdir=$(mktemp -d "${TMPDIR:-/tmp}/tunnel-release-test.XXXXXX")
cleanup() {
	rm -rf "$tmpdir"
}
trap cleanup EXIT INT TERM

release_root="$tmpdir/releases"
go_bin="${GO:-go}"
repo_relay_version=$("$go_bin" run ./cmd/relay version | awk 'NR==1 {print $2}')
if [ -z "$repo_relay_version" ]; then
	printf 'error: could not determine current relay version\n' >&2
	exit 1
fi
version="${TEST_RELEASE_VERSION:-$(release_fixture_version "$repo_relay_version")}"
incompatible_version=$(release_incompatible_version "$repo_relay_version")
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

if GO="$go_bin" RELEASE_DIR="$release_root" "$script_dir/release-package.sh" "v0.1.2foo" >/dev/null 2>"$tmpdir/invalid-version.err"
then
	printf 'error: invalid version unexpectedly passed validation\n' >&2
	exit 1
fi

if ! grep -q 'version must look like v0.1.0' "$tmpdir/invalid-version.err"; then
	printf 'error: invalid version path did not explain failure\n' >&2
	exit 1
fi

if GO="$go_bin" RELEASE_DIR="$release_root" "$script_dir/release-package.sh" "$incompatible_version" >/dev/null 2>"$tmpdir/compatibility.err"
then
	printf 'error: mismatched compatibility line unexpectedly packaged\n' >&2
	exit 1
fi

if ! grep -q 'outside the current relay compatibility line' "$tmpdir/compatibility.err"; then
	printf 'error: compatibility-line path did not explain failure\n' >&2
	exit 1
fi

printf 'release packaging smoke tests passed\n'
