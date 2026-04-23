#!/bin/sh

set -eu

usage() {
	printf 'usage: %s [--create] [--push] [--commit <commit>] <product> <version>\n' "$0" >&2
}

script_dir=$(CDPATH= cd -- "$(dirname "$0")" && pwd)

# shellcheck source=/dev/null
. "$script_dir/release-common.sh"

create=0
push=0
commit=HEAD

while [ "$#" -gt 0 ]; do
	case "$1" in
		--create)
			create=1
			shift
			;;
		--push)
			create=1
			push=1
			shift
			;;
		--commit)
			if [ "$#" -lt 2 ]; then
				usage
				exit 1
			fi
			commit="$2"
			shift 2
			;;
		-h|--help)
			usage
			exit 0
			;;
		--)
			shift
			break
			;;
		-*)
			printf 'error: unknown option: %s\n' "$1" >&2
			usage
			exit 1
			;;
		*)
			break
			;;
	esac
done

if [ "$#" -ne 2 ]; then
	usage
	exit 1
fi

product="$1"
version="$2"
tag=$(release_source_tag "$product" "$version")

if [ "$create" -eq 0 ]; then
	printf '%s\n' "$tag"
	exit 0
fi

target_commit=$(git rev-parse "$commit^{commit}")
git fetch --tags origin >/dev/null 2>&1 || true

if git rev-parse --verify --quiet "refs/tags/$tag" >/dev/null; then
	existing_commit=$(git rev-list -n 1 "$tag")
	if [ "$existing_commit" != "$target_commit" ]; then
		printf 'error: source tag %s already points at %s, not %s\n' "$tag" "$existing_commit" "$target_commit" >&2
		exit 1
	fi
	if [ "$push" -eq 1 ]; then
		git push origin "refs/tags/$tag"
	fi
	printf '%s\n' "$tag"
	exit 0
fi

git tag "$tag" "$target_commit"

if [ "$push" -eq 1 ]; then
	git push origin "refs/tags/$tag"
fi

printf '%s\n' "$tag"
