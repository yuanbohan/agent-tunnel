#!/bin/sh

set -eu

usage() {
	printf 'usage: %s <version>\n' "$0" >&2
}

script_dir=$(CDPATH= cd -- "$(dirname "$0")" && pwd)

# shellcheck source=/dev/null
. "$script_dir/release-common.sh"

version="${1:-}"
if [ -z "$version" ]; then
	usage
	exit 1
fi
release_validate_version "$version"

printf '{"version":"%s","compatibility_line":"%s"}\n' "$version" "$(release_compatibility_line "$version")"
