#!/bin/sh

set -eu

script_dir=$(CDPATH= cd -- "$(dirname "$0")" && pwd)

# shellcheck source=/dev/null
. "$script_dir/release-common.sh"

tmpdir=$(mktemp -d "${TMPDIR:-/tmp}/tunnel-release-publish-test.XXXXXX")
cleanup() {
	rm -rf "$tmpdir"
}
trap cleanup EXIT INT TERM

release_root="$tmpdir/releases"
go_bin="${GO:-go}"
signing_private_key="$tmpdir/release-signing-private.pem"
signing_public_key="$tmpdir/release-signing-public.txt"
"$go_bin" run ./cmd/release-sign keygen "$signing_private_key" "$signing_public_key" >/dev/null
release_signing_key=$(cat "$signing_private_key")
release_signing_public_key=$(awk 'NR==1 {print $2}' "$signing_public_key")
repo_relay_version=$("$go_bin" run ./cmd/relay version | awk 'NR==1 {print $2}')
if [ -z "$repo_relay_version" ]; then
	printf 'error: could not determine current relay version\n' >&2
	exit 1
fi
version="${TEST_RELEASE_VERSION:-$(release_fixture_version "$repo_relay_version")}"

GO="$go_bin" RELEASE_DIR="$release_root" TUNNEL_RELEASE_SIGNING_PRIVATE_KEY="$release_signing_key" TUNNEL_RELEASE_SIGNING_PUBLIC_KEY="$release_signing_public_key" "$script_dir/release-package.sh" "$version" >/dev/null

if "$script_dir/render-latest-manifest.sh" "$version" extra >/dev/null 2>"$tmpdir/manifest-args.err"
then
	printf 'error: render-latest-manifest accepted extra args\n' >&2
	exit 1
fi

if ! grep -q '^usage:' "$tmpdir/manifest-args.err"; then
	printf 'error: manifest arg-count path did not explain failure\n' >&2
	exit 1
fi

output=$(
	PUBLISH_DRY_RUN=1 \
	RELEASE_DIR="$release_root" \
	TUNNEL_DIST_REPO="yuanbohan/tunnel" \
	"$script_dir/publish-tunnel-release.sh" "$version"
)

for expected in \
	"dry-run: would publish $version from $release_root/$version to yuanbohan/tunnel" \
	"dry-run: would create draft release $version" \
	"dry-run: would upload 4 archives plus checksums.txt plus checksums.txt.sig" \
	"dry-run: would publish release $version before updating latest.json" \
	"release: publish tunnel $version"
do
	if ! printf '%s\n' "$output" | grep -Fq "$expected"; then
		printf 'error: dry-run output missing %s\n' "$expected" >&2
		exit 1
	fi
done

printf 'release publish dry-run tests passed\n'
