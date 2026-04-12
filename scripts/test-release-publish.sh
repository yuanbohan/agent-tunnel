#!/bin/sh

set -eu

script_dir=$(CDPATH= cd -- "$(dirname "$0")" && pwd)

tmpdir=$(mktemp -d "${TMPDIR:-/tmp}/tunnel-release-publish-test.XXXXXX")
cleanup() {
	rm -rf "$tmpdir"
}
trap cleanup EXIT INT TERM

version="v0.1.2"
release_root="$tmpdir/releases"
go_bin="${GO:-go}"

GO="$go_bin" RELEASE_DIR="$release_root" "$script_dir/release-package.sh" "$version" >/dev/null

output=$(
	PUBLISH_DRY_RUN=1 \
	RELEASE_DIR="$release_root" \
	TUNNEL_DIST_REPO="yuanbohan/tunnel" \
	"$script_dir/publish-tunnel-release.sh" "$version"
)

for expected in \
	"dry-run: would publish $version from $release_root/$version to yuanbohan/tunnel" \
	"dry-run: would create draft release $version" \
	"dry-run: would upload 4 archives plus checksums.txt" \
	"dry-run: would publish release $version before updating latest.json" \
	"release: publish tunnel $version"
do
	if ! printf '%s\n' "$output" | grep -Fq "$expected"; then
		printf 'error: dry-run output missing %s\n' "$expected" >&2
		exit 1
	fi
done

printf 'release publish dry-run tests passed\n'
