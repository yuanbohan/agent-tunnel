#!/bin/sh

set -eu

truthy() {
	case "${1:-}" in
		1|true|TRUE|yes|YES|on|ON)
			return 0
			;;
		*)
			return 1
			;;
	esac
}

require_cmd() {
	if command -v "$1" >/dev/null 2>&1; then
		return 0
	fi
	printf 'error: required command not found: %s\n' "$1" >&2
	exit 1
}

git_commit_if_needed() {
	repo_dir="$1"
	message="$2"
	if git -C "$repo_dir" diff --cached --quiet; then
		return 1
	fi
	git -C "$repo_dir" commit -m "$message" >/dev/null
	return 0
}

sync_stable_files() {
	dest_dir="$1"
	version="$2"
	install_source="$3"
	readme_source="$4"
	manifest_script="$5"
	go_bin="$6"
	signing_key="$7"

	cp "$install_source" "$dest_dir/install.sh"
	chmod 0755 "$dest_dir/install.sh"
	cp "$readme_source" "$dest_dir/README.md"
	"$manifest_script" "$version" >"$dest_dir/latest.json"
	TUNNEL_RELEASE_SIGNING_PRIVATE_KEY="$signing_key" "$release_sign_bin" sign "$dest_dir/latest.json" "$dest_dir/latest.json.sig" >/dev/null
}

ensure_repo_checkout() {
	repo_url="$1"
	branch="$2"
	dest_dir="$3"

	if ! git clone "$repo_url" "$dest_dir" >/dev/null; then
		printf 'error: failed to clone %s into %s\n' "$repo_url" "$dest_dir" >&2
		exit 1
	fi

	if git -C "$dest_dir" show-ref --verify --quiet "refs/remotes/origin/$branch"; then
		git -C "$dest_dir" checkout -B "$branch" "origin/$branch" >/dev/null
		return 0
	fi

	git -C "$dest_dir" checkout --orphan "$branch" >/dev/null
	git -C "$dest_dir" rm --cached -r . >/dev/null || true
	find "$dest_dir" -mindepth 1 -maxdepth 1 ! -name .git -exec rm -rf {} +
}

script_dir=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/.." && pwd)

# shellcheck source=/dev/null
. "$script_dir/release-common.sh"

version="${1:-}"
if [ -z "$version" ]; then
	printf 'usage: %s <version>\n' "$0" >&2
	exit 1
fi
release_validate_version "$version"

release_root="${RELEASE_DIR:-$repo_root/dist/releases}"
artifact_dir="$release_root/$version"
dist_repo="${TUNNEL_DIST_REPO:-}"
dist_branch="${TUNNEL_DIST_BRANCH:-main}"
install_source="${TUNNEL_DIST_INSTALL_SOURCE:-$script_dir/install-tunnel.sh}"
readme_source="${TUNNEL_DIST_README_SOURCE:-$repo_root/docs/public-distribution-readme.md}"
manifest_script="${TUNNEL_DIST_MANIFEST_SCRIPT:-$script_dir/render-latest-manifest.sh}"
dry_run="${PUBLISH_DRY_RUN:-0}"
clone_dir="${TUNNEL_DIST_CLONE_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/tunnel-dist.XXXXXX")}"
cleanup_clone="true"
go_bin="${GO:-go}"

cleanup() {
	if [ "$cleanup_clone" = "true" ]; then
		rm -rf "$clone_dir"
	fi
	rm -rf "${stage_dir:-}"
}
trap cleanup EXIT INT TERM

stage_dir=$(mktemp -d "${TMPDIR:-/tmp}/tunnel-publish.XXXXXX")
release_sign_bin="$stage_dir/release-sign"
"$go_bin" build -o "$release_sign_bin" "$repo_root/cmd/release-sign"

for os_arch in "darwin arm64" "darwin amd64" "linux amd64" "linux arm64"; do
	set -- $os_arch
	asset_path="$artifact_dir/$(release_asset_name "$version" "$1" "$2")"
	if [ ! -f "$asset_path" ]; then
		printf 'error: missing release artifact %s\n' "$asset_path" >&2
		exit 1
	fi
done
if [ ! -f "$artifact_dir/checksums.txt" ]; then
	printf 'error: missing %s/checksums.txt\n' "$artifact_dir" >&2
	exit 1
fi
if [ ! -f "$artifact_dir/checksums.txt.sig" ]; then
	printf 'error: missing %s/checksums.txt.sig\n' "$artifact_dir" >&2
	exit 1
fi
if [ ! -f "$install_source" ]; then
	printf 'error: missing install source %s\n' "$install_source" >&2
	exit 1
fi
if [ ! -f "$readme_source" ]; then
	printf 'error: missing public README source %s\n' "$readme_source" >&2
	exit 1
fi

if [ -z "$dist_repo" ]; then
	printf 'error: TUNNEL_DIST_REPO is required (for example yuanbohan/tunnel)\n' >&2
	exit 1
fi

if truthy "$dry_run"; then
	printf 'dry-run: would publish %s from %s to %s\n' "$version" "$artifact_dir" "$dist_repo"
	printf 'dry-run: would bootstrap %s if branch %s is missing\n' "$dist_repo" "$dist_branch"
	printf 'dry-run: would create draft release %s\n' "$version"
	printf 'dry-run: would upload 4 archives plus checksums.txt plus checksums.txt.sig\n'
	printf 'dry-run: would publish release %s before updating latest.json\n' "$version"
	printf 'dry-run: would sync install.sh, latest.json, latest.json.sig, and README.md with commit message "release: publish tunnel %s"\n' "$version"
	exit 0
fi

token="${TUNNEL_DIST_REPO_TOKEN:-${GH_TOKEN:-}}"
if [ -z "$token" ]; then
	printf 'error: TUNNEL_DIST_REPO_TOKEN or GH_TOKEN is required\n' >&2
	exit 1
fi
release_signing_key="${TUNNEL_RELEASE_SIGNING_PRIVATE_KEY:-}"
if [ -z "$release_signing_key" ]; then
	printf 'error: TUNNEL_RELEASE_SIGNING_PRIVATE_KEY is required\n' >&2
	exit 1
fi

require_cmd git
require_cmd gh

repo_url="https://x-access-token:$token@github.com/$dist_repo.git"
ensure_repo_checkout "$repo_url" "$dist_branch" "$clone_dir"

git -C "$clone_dir" config user.name "github-actions[bot]"
git -C "$clone_dir" config user.email "41898282+github-actions[bot]@users.noreply.github.com"

if ! git -C "$clone_dir" rev-parse --verify HEAD >/dev/null 2>&1; then
	printf 'bootstrapping public distribution repo at %s branch %s...\n' "$dist_repo" "$dist_branch"
	cp "$install_source" "$clone_dir/install.sh"
	chmod 0755 "$clone_dir/install.sh"
	cp "$readme_source" "$clone_dir/README.md"
	"$manifest_script" "$version" >"$clone_dir/latest.json"
	TUNNEL_RELEASE_SIGNING_PRIVATE_KEY="$release_signing_key" "$release_sign_bin" sign "$clone_dir/latest.json" "$clone_dir/latest.json.sig" >/dev/null
	git -C "$clone_dir" add install.sh README.md latest.json latest.json.sig
	if git_commit_if_needed "$clone_dir" "docs: bootstrap public tunnel distribution repo"; then
		printf 'pushing bootstrap commit...\n'
		git -C "$clone_dir" push -u origin "$dist_branch" >/dev/null
	fi
fi

GH_TOKEN="$token" gh release view "$version" --repo "$dist_repo" >/dev/null 2>&1 && {
	printf 'error: release %s already exists in %s; delete the draft or choose a new version\n' "$version" "$dist_repo" >&2
	exit 1
}

printf 'creating draft release %s in %s...\n' "$version" "$dist_repo"
compatibility_line=$(release_compatibility_line "$version")
GH_TOKEN="$token" gh release create "$version" \
	"$artifact_dir/"tunnel_*.tar.gz \
	"$artifact_dir/checksums.txt" \
	"$artifact_dir/checksums.txt.sig" \
	--repo "$dist_repo" \
	--target "$dist_branch" \
	--title "tunnel $version" \
	--notes "Public tunnel release $version.

Compatibility line: $compatibility_line
Tunnel and Relay are guaranteed compatible within the same compatibility line." \
	--draft

printf 'publishing release %s...\n' "$version"
GH_TOKEN="$token" gh release edit "$version" --repo "$dist_repo" --draft=false

printf 'syncing stable files to %s branch %s...\n' "$dist_repo" "$dist_branch"
sync_stable_files "$clone_dir" "$version" "$install_source" "$readme_source" "$manifest_script" "$go_bin" "$release_signing_key"
git -C "$clone_dir" add install.sh latest.json latest.json.sig README.md
if git_commit_if_needed "$clone_dir" "release: publish tunnel $version"; then
	git -C "$clone_dir" push origin "$dist_branch" >/dev/null
fi

printf 'published tunnel %s to %s\n' "$version" "$dist_repo"
