#!/bin/sh

set -eu

script_dir=$(CDPATH= cd -- "$(dirname "$0")" && pwd)

# shellcheck source=/dev/null
. "$script_dir/release-common.sh"

version="${1:-v0.1.0}"
image="${RELAY_DOCKER_TEST_IMAGE:-agent-tunnel-relay:test}"
build_time="${BUILD_TIME:-2026-01-01T00:00:00Z}"
git_commit="${GIT_COMMIT:-test-commit}"
git_branch="${GIT_BRANCH:-test-branch}"

release_validate_version "$version"

docker build \
    -f Dockerfile.relay \
    --build-arg VERSION="$version" \
    --build-arg DISTRIBUTION_MARKER=official-release \
    --build-arg GIT_COMMIT="$git_commit" \
    --build-arg GIT_BRANCH="$git_branch" \
    --build-arg BUILD_TIME="$build_time" \
    -t "$image" \
    .

got="$(docker run --rm "$image" version | awk 'NR==1 {print $2}')"
if [ "$got" != "$version" ]; then
    printf 'error: relay image reports version %s, want %s\n' "$got" "$version" >&2
    exit 1
fi

got_branch="$(docker run --rm "$image" version | awk 'NR==2 {print $2}')"
if [ "$got_branch" != "$git_branch" ]; then
    printf 'error: relay image reports branch %s, want %s\n' "$got_branch" "$git_branch" >&2
    exit 1
fi

printf 'verified relay image version: %s\n' "$got"
printf 'verified relay image branch: %s\n' "$got_branch"
