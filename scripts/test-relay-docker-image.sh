#!/bin/sh

set -eu

script_dir=$(CDPATH= cd -- "$(dirname "$0")" && pwd)

# shellcheck source=/dev/null
. "$script_dir/release-common.sh"

version="${1:-v0.1.0}"
relay_image="${RELAY_DOCKER_TEST_IMAGE:-agent-tunnel-relay:test}"
stun_image="${STUN_DOCKER_TEST_IMAGE:-agent-tunnel-stun:test}"
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
    -t "$relay_image" \
    .

docker tag "$relay_image" "$stun_image"

got="$(docker run --rm "$relay_image" version | awk 'NR==1 {print $2}')"
if [ "$got" != "$version" ]; then
    printf 'error: relay image reports version %s, want %s\n' "$got" "$version" >&2
    exit 1
fi

got_branch="$(docker run --rm "$relay_image" version | awk 'NR==2 {print $2}')"
if [ "$got_branch" != "$git_branch" ]; then
    printf 'error: relay image reports branch %s, want %s\n' "$got_branch" "$git_branch" >&2
    exit 1
fi

got_stun="$(docker run --rm "$stun_image" version | awk 'NR==1 {print $2}')"
if [ "$got_stun" != "$version" ]; then
    printf 'error: stun image reports version %s, want %s\n' "$got_stun" "$version" >&2
    exit 1
fi

stun_help="$(docker run --rm "$stun_image" stun serve --help)"
case "$stun_help" in
    *"Start the Binding-only STUN UDP service"*) ;;
    *)
        printf 'error: stun image does not expose relay stun serve help\n' >&2
        exit 1
        ;;
esac

image_cmd="$(docker image inspect "$relay_image" --format '{{json .Config.Cmd}}')"
if [ "$image_cmd" != '["serve"]' ]; then
    printf 'error: relay image default command is %s, want ["serve"]\n' "$image_cmd" >&2
    exit 1
fi

exposed_ports="$(docker image inspect "$relay_image" --format '{{json .Config.ExposedPorts}}')"
case "$exposed_ports" in
    *'"8586/tcp"'*'"3478/udp"'*|*'"3478/udp"'*'"8586/tcp"'*) ;;
    *)
        printf 'error: relay image exposes ports %s, want 8586/tcp and 3478/udp\n' "$exposed_ports" >&2
        exit 1
        ;;
esac

printf 'verified relay image version: %s\n' "$got"
printf 'verified relay image branch: %s\n' "$got_branch"
printf 'verified stun image version: %s\n' "$got_stun"
printf 'verified relay/stun image tags, STUN command, and exposed ports\n'
