#!/bin/sh

set -eu

script_dir=$(CDPATH= cd -- "$(dirname "$0")" && pwd)

tmpdir=$(mktemp -d "${TMPDIR:-/tmp}/release-source-tag-test.XXXXXX")
cleanup() {
	rm -rf "$tmpdir"
}
trap cleanup EXIT INT TERM

assert_output() {
	want="$1"
	shift
	got=$("$@")
	if [ "$got" != "$want" ]; then
		printf 'error: output = %s, want %s\n' "$got" "$want" >&2
		exit 1
	fi
}

assert_fails_with() {
	needle="$1"
	shift
	if "$@" >/dev/null 2>"$tmpdir/error"; then
		printf 'error: command unexpectedly succeeded: %s\n' "$*" >&2
		exit 1
	fi
	if ! grep -Fq "$needle" "$tmpdir/error"; then
		printf 'error: failure did not include %s\n' "$needle" >&2
		printf 'stderr:\n' >&2
		cat "$tmpdir/error" >&2
		exit 1
	fi
}

assert_output "tunnel-v0.2.3" "$script_dir/release-source-tag.sh" tunnel v0.2.3
assert_output "relay-v0.4.1" "$script_dir/release-source-tag.sh" relay v0.4.1

assert_fails_with "product must be tunnel or relay" "$script_dir/release-source-tag.sh" server v0.1.0
assert_fails_with "version must look like v0.1.0" "$script_dir/release-source-tag.sh" tunnel tunnel-v0.1.0
assert_fails_with "version must look like v0.1.0" "$script_dir/release-source-tag.sh" relay v0.1

repo="$tmpdir/repo"
mkdir "$repo"
git -C "$repo" init >/dev/null
git -C "$repo" config user.name "Release Test"
git -C "$repo" config user.email "release-test@example.invalid"
printf 'first\n' >"$repo/file.txt"
git -C "$repo" add file.txt
git -C "$repo" commit -m first >/dev/null
first_commit=$(git -C "$repo" rev-parse HEAD)

assert_output "tunnel-v0.2.3" sh -c "cd '$repo' && '$script_dir/release-source-tag.sh' --create tunnel v0.2.3"
assert_output "$first_commit" git -C "$repo" rev-list -n 1 tunnel-v0.2.3
assert_output "tunnel-v0.2.3" sh -c "cd '$repo' && '$script_dir/release-source-tag.sh' --create tunnel v0.2.3"

printf 'second\n' >"$repo/file.txt"
git -C "$repo" add file.txt
git -C "$repo" commit -m second >/dev/null

assert_fails_with "already points at" sh -c "cd '$repo' && '$script_dir/release-source-tag.sh' --create tunnel v0.2.3"

remote="$tmpdir/remote.git"
git init --bare "$remote" >/dev/null
push_repo="$tmpdir/push-repo"
git clone "$remote" "$push_repo" >/dev/null 2>&1
git -C "$push_repo" config user.name "Release Push Test"
git -C "$push_repo" config user.email "release-push-test@example.invalid"
printf 'push-first\n' >"$push_repo/file.txt"
git -C "$push_repo" add file.txt
git -C "$push_repo" commit -m first >/dev/null

assert_output "relay-v0.6.0" sh -c "cd '$push_repo' && '$script_dir/release-source-tag.sh' --push relay v0.6.0"
assert_output "$(git -C "$push_repo" rev-parse relay-v0.6.0)" git --git-dir "$remote" rev-list -n 1 relay-v0.6.0

git -C "$push_repo" push origin :refs/tags/relay-v0.6.0 >/dev/null
assert_fails_with "unknown revision" git --git-dir "$remote" rev-list -n 1 relay-v0.6.0

assert_output "relay-v0.6.0" sh -c "cd '$push_repo' && '$script_dir/release-source-tag.sh' --push relay v0.6.0"
assert_output "$(git -C "$push_repo" rev-parse relay-v0.6.0)" git --git-dir "$remote" rev-list -n 1 relay-v0.6.0

printf 'push-second\n' >"$push_repo/file.txt"
git -C "$push_repo" add file.txt
git -C "$push_repo" commit -m second >/dev/null
assert_fails_with "already points at" sh -c "cd '$push_repo' && '$script_dir/release-source-tag.sh' --push relay v0.6.0"

git -C "$repo" tag v0.5.0
if git -C "$repo" tag -l | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
	:
fi
assert_fails_with "source tag must look like" sh -c ". '$script_dir/release-common.sh'; release_validate_source_tag v0.5.0"

printf 'release source tag tests passed\n'
