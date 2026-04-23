#!/bin/sh

set -eu

repo_root=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)

if grep -R "git-version.sh" "$repo_root/makefiles" "$repo_root/scripts" | grep -v 'test-local-build-version.sh' >/dev/null 2>&1; then
	printf 'error: local build/install path still references git-version.sh\n' >&2
	exit 1
fi

if grep -R "^tag-version:" "$repo_root/makefiles" >/dev/null 2>&1; then
	printf 'error: tag-version target should not be available for local auto tagging\n' >&2
	exit 1
fi

tmpdir=$(mktemp -d "${TMPDIR:-/tmp}/local-build-version-test.XXXXXX")
probe_dir="$repo_root/.tmp-local-build-version-probe"
cleanup() {
	rm -rf "$tmpdir"
	rm -rf "$probe_dir"
}
trap cleanup EXIT INT TERM

go_bin="${GO:-go}"
make_bin="${MAKE:-make}"
default_bin_dir="$tmpdir/bin-default"
override_bin_dir="$tmpdir/bin-override"
install_bin_dir="$tmpdir/bin-install"
install_dir="$tmpdir/install"

mkdir -p "$probe_dir"
probe="$probe_dir/main.go"
cat >"$probe" <<'EOF'
package main

import (
	"fmt"

	"yuanbohan/tunnel/internal/buildinfo"
)

func main() {
	fmt.Printf("%s %s\n", buildinfo.String(), buildinfo.DistributionString())
}
EOF

default_output=$(cd "$repo_root" && "$go_bin" run "$probe")
if [ "$default_output" != "v0.1.0-dev non-release" ]; then
	printf 'error: default buildinfo output = %s, want v0.1.0-dev non-release\n' "$default_output" >&2
	exit 1
fi

override_output=$(cd "$repo_root" && "$go_bin" run \
	-ldflags="-X yuanbohan/tunnel/internal/buildinfo.Version=v0.2.3" \
	"$probe")
if [ "$override_output" != "v0.2.3 non-release" ]; then
	printf 'error: override buildinfo output = %s, want v0.2.3 non-release\n' "$override_output" >&2
	exit 1
fi

(
	cd "$repo_root"
	"$make_bin" build GO="$go_bin" BIN_DIR="$default_bin_dir" >/dev/null
)

default_binary_output=$("$default_bin_dir/tunnel" --version)
if [ "$default_binary_output" != "tunnel v0.1.0-dev" ]; then
	printf 'error: default tunnel binary output = %s, want tunnel v0.1.0-dev\n' "$default_binary_output" >&2
	exit 1
fi

(
	cd "$repo_root"
	"$make_bin" build GO="$go_bin" BIN_DIR="$override_bin_dir" VERSION=v0.2.3 >/dev/null
)

override_binary_output=$("$override_bin_dir/tunnel" --version)
if [ "$override_binary_output" != "tunnel v0.2.3" ]; then
	printf 'error: override tunnel binary output = %s, want tunnel v0.2.3\n' "$override_binary_output" >&2
	exit 1
fi

if (
	cd "$repo_root" &&
	"$make_bin" build GO="$go_bin" BIN_DIR="$tmpdir/bin-invalid" VERSION=relay-v0.2.3
) >/dev/null 2>"$tmpdir/invalid-version.err"
then
	printf 'error: make build unexpectedly accepted product-prefixed VERSION\n' >&2
	exit 1
fi

if ! grep -Fq 'version must look like v0.1.0' "$tmpdir/invalid-version.err"; then
	printf 'error: invalid VERSION path did not explain failure\n' >&2
	cat "$tmpdir/invalid-version.err" >&2
	exit 1
fi

(
	cd "$repo_root"
	"$make_bin" install GO="$go_bin" BIN_DIR="$install_bin_dir" INSTALL_DIR="$install_dir" >/dev/null
)

installed_binary_output=$("$install_dir/tunnel" --version)
if [ "$installed_binary_output" != "tunnel v0.1.0-dev" ]; then
	printf 'error: installed tunnel binary output = %s, want tunnel v0.1.0-dev\n' "$installed_binary_output" >&2
	exit 1
fi

printf 'local build version tests passed\n'
