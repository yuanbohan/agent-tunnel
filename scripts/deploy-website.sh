#!/bin/sh

set -eu

usage() {
	printf 'usage: %s deploy [--verbose] [--dry-run]\n' "$0" >&2
	printf 'note: website deploy builds %s and publishes a static release only; nginx/certbot/postgresql config stays under make install-dev/install-prod\n' "${WEBSITE_REPO_DIR:-../agent-tunnel-website}" >&2
}

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

debug() {
	if [ "$verbose" -eq 1 ]; then
		printf '  debug: %s\n' "$*"
	fi
}

step_icon() {
	case "${1:-}" in
		'Install website dependencies')
			printf '📦'
			;;
		'Build website bundle')
			printf '🛠️'
			;;
		'Validate website bundle')
			printf '🔍'
			;;
		'Package website bundle')
			printf '📦'
			;;
		'Publish website release')
			printf '🚚'
			;;
		*)
			printf '•'
			;;
	esac
}

status_icon() {
	case "${1:-}" in
		planned)
			printf '📝'
			;;
		ok|changed)
			printf '✅'
			;;
		failed)
			printf '❌'
			;;
		*)
			printf '•'
			;;
	esac
}

print_concise_step() {
	label="$1"
	state="$2"
	detail="${3:-}"
	printf '%s [%d/%d] %s  %s' "$(step_icon "$label")" "$current_step" "$total_steps" "$label" "$(status_icon "$state")"
	if [ -n "$detail" ]; then
		printf ' %s' "$detail"
	fi
	printf '\n'
}

require_cmd() {
	if command -v "$1" >/dev/null 2>&1; then
		return 0
	fi
	printf 'error: required command not found: %s\n' "$1" >&2
	exit 1
}

require_website_repo() {
	if [ ! -d "$website_repo" ]; then
		printf 'error: website repo not found: %s\n' "$website_repo" >&2
		exit 1
	fi
	if [ ! -f "$website_repo/package.json" ]; then
		printf 'error: website repo is missing package.json: %s\n' "$website_repo/package.json" >&2
		exit 1
	fi
}

require_absolute_path() {
	name="$1"
	value="$2"
	case "$value" in
		/*)
			return 0
			;;
		*)
			printf 'error: %s must be an absolute path: %s\n' "$name" "$value" >&2
			exit 1
			;;
	esac
}

run_in_website_repo() {
	(
		cd "$website_repo"
		"$@"
	)
}

shell_quote() {
	printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"
}

cleanup_local_archive() {
	if [ -n "${local_archive:-}" ] && [ -f "$local_archive" ]; then
		rm -f "$local_archive"
	fi
}

run_cmd() {
	if [ "$dry_run" -eq 1 ]; then
		debug "$*"
		return 0
	fi

	if [ "$verbose" -eq 1 ]; then
		debug "$*"
		if "$@"; then
			return 0
		fi
		last_error_reason='see command output above'
		return 1
	fi

	tmp_output="$(mktemp "${TMPDIR:-/tmp}/agentunnel-website-deploy.XXXXXX")"
	if "$@" >"$tmp_output" 2>&1; then
		rm -f "$tmp_output"
		return 0
	fi

	last_error_reason="$(awk 'NF {print; exit}' "$tmp_output")"
	if [ -z "${last_error_reason:-}" ]; then
		last_error_reason='command failed'
	fi
	rm -f "$tmp_output"
	return 1
}

run_cmd_or_return() {
	if run_cmd "$@"; then
		return 0
	fi
	return 1
}

set_step_result() {
	step_state="$1"
	step_detail="${2:-}"
}

record_step() {
	case "$step_state" in
		ok|planned)
			ok_count=$((ok_count + 1))
			;;
		changed)
			changed_count=$((changed_count + 1))
			;;
	esac
	if [ "$step_state" = 'planned' ]; then
		planned_count=$((planned_count + 1))
	fi
}

run_step() {
	current_step=$((current_step + 1))
	label="$1"
	shift

	step_state='ok'
	step_detail=''
	last_error_reason=''

	if [ "$verbose" -eq 1 ]; then
		printf '\nTASK [%d/%d] %s\n' "$current_step" "$total_steps" "$label"
	fi

	if [ "$dry_run" -eq 1 ]; then
		if [ "$verbose" -eq 1 ]; then
			"$@" || {
				failed_count=$((failed_count + 1))
				printf '  failed\n' >&2
				exit 1
			}
		fi
		set_step_result planned
		record_step
		if [ "$verbose" -eq 1 ]; then
			printf '  planned\n'
		else
			print_concise_step "$label" planned
		fi
		return 0
	fi

	if "$@"; then
		record_step
		if [ "$verbose" -eq 1 ]; then
			if [ -n "$step_detail" ]; then
				printf '  %s: %s\n' "$step_state" "$step_detail"
			else
				printf '  %s\n' "$step_state"
			fi
		else
			print_concise_step "$label" "$step_state" "$step_detail"
		fi
		return 0
	fi

	failed_count=$((failed_count + 1))
	if [ "$verbose" -eq 1 ]; then
		if [ -n "$last_error_reason" ]; then
			printf '  failed: %s\n' "$last_error_reason" >&2
		else
			printf '  failed\n' >&2
		fi
	else
		print_concise_step "$label" failed "$last_error_reason" >&2
	fi
	exit 1
}

install_website_dependencies() {
	run_cmd_or_return run_in_website_repo npm ci --no-fund --no-audit
	set_step_result changed 'npm ci completed'
}

build_website_bundle() {
	run_cmd_or_return run_in_website_repo npm run build
	if [ "$dry_run" -eq 0 ] && [ ! -d "$website_dist_dir" ]; then
		last_error_reason="build output not found: $website_dist_dir"
		return 1
	fi
	set_step_result changed "$(basename "$website_dist_dir") ready"
}

validate_website_bundle() {
	if [ "$dry_run" -eq 1 ]; then
		return 0
	fi
	if [ ! -d "$website_dist_dir" ]; then
		last_error_reason="build output not found: $website_dist_dir"
		return 1
	fi

	first_symlink="$(find "$website_dist_dir" -type l -print -quit 2>/dev/null || true)"
	if [ -n "$first_symlink" ]; then
		last_error_reason="website bundle contains unsupported symlink: $first_symlink"
		return 1
	fi

	set_step_result ok 'no symlinks found'
}

package_website_bundle() {
	if [ "$dry_run" -eq 1 ]; then
		local_archive="${TMPDIR:-/tmp}/agentunnel-website.$deploy_run_id.tar.gz"
		return 0
	fi

	archive_tmp="$(mktemp "${TMPDIR:-/tmp}/agentunnel-website.XXXXXX")"
	local_archive="$archive_tmp.tar.gz"
	mv "$archive_tmp" "$local_archive"
	run_cmd_or_return tar -C "$website_dist_dir" -czf "$local_archive" .
	set_step_result changed "$(basename "$local_archive") ready"
}

publish_website_release() {
	q_stage_dir="$(shell_quote "$deploy_website_tmp_dir")"
	q_stage_file="$(shell_quote "$deploy_website_stage_file")"
	q_release_root="$(shell_quote "$deploy_website_root")"
	q_releases_dir="$(shell_quote "$deploy_website_releases_dir")"
	q_release_dir="$(shell_quote "$deploy_website_release_dir")"
	q_current_link="$(shell_quote "$deploy_website_current_link")"
	q_current_tmp_link="$(shell_quote "$deploy_website_current_tmp_link")"
	remote_stage_target="$deploy_website_host:$(shell_quote "$deploy_website_stage_file")"
	remote_cmd=$(cat <<EOF
set -eu
cleanup() {
	status=\$?
	if [ \$status -ne 0 ]; then
		rm -f $q_stage_file
		sudo rm -rf $q_release_dir $q_current_tmp_link
	fi
}
trap cleanup EXIT
sudo install -d -m 0755 $q_release_root $q_releases_dir
install -d -m 0755 $q_stage_dir
sudo rm -rf $q_release_dir $q_current_tmp_link
sudo install -d -m 0755 $q_release_dir
sudo tar -xzf $q_stage_file -C $q_release_dir
sudo ln -sfn $q_release_dir $q_current_tmp_link
sudo mv -Tf $q_current_tmp_link $q_current_link
rm -f $q_stage_file
trap - EXIT
EOF
	)

	if [ "$dry_run" -eq 0 ] && [ ! -f "$local_archive" ]; then
		last_error_reason="website archive not found: $local_archive"
		return 1
	fi

	# shellcheck disable=SC2086
	run_cmd_or_return ssh $ssh_opts "$deploy_website_host" "install -d -m 0755 $q_stage_dir && rm -f $q_stage_file && sudo rm -rf $q_release_dir $q_current_tmp_link"
	# shellcheck disable=SC2086
	run_cmd_or_return scp $scp_opts "$local_archive" "$remote_stage_target"
	# shellcheck disable=SC2086
	run_cmd_or_return ssh $ssh_opts "$deploy_website_host" "$remote_cmd"
	set_step_result changed "activated release $deploy_run_id"
}

run_plan() {
	total_steps="$1"
	shift

	current_step=0
	ok_count=0
	changed_count=0
	failed_count=0
	planned_count=0

	printf 'DEPLOY-WEBSITE [%s] env=%s\n' "$deploy_website_host" "$env_file"
	printf 'website: %s\n' "$website_repo"
	if [ "$dry_run" -eq 1 ]; then
		printf 'mode: dry-run\n'
	elif [ "$verbose" -eq 1 ]; then
		printf 'mode: verbose\n'
	else
		printf 'mode: concise\n'
	fi

	while [ "$#" -gt 0 ]; do
		label="$1"
		fn="$2"
		run_step "$label" "$fn"
		shift 2
	done

	printf '\n📋 SUMMARY [%s]\n' "$deploy_website_host"
	if [ "$dry_run" -eq 1 ]; then
		printf '  📝 planned=%d  ❌ failed=%d\n' "$planned_count" "$failed_count"
	else
		printf '  ✅ ok=%d  ✨ changed=%d  ❌ failed=%d\n' "$ok_count" "$changed_count" "$failed_count"
	fi
}

command="${1:-}"
if [ -z "$command" ]; then
	usage
	exit 1
fi
shift

verbose=0
dry_run=0

if truthy "${DEPLOY_VERBOSE:-0}"; then
	verbose=1
fi
if truthy "${DEPLOY_DRY_RUN:-0}"; then
	dry_run=1
fi

while [ "$#" -gt 0 ]; do
	case "$1" in
		--verbose)
			verbose=1
			;;
		--dry-run)
			dry_run=1
			;;
		-h|--help)
			usage
			exit 0
			;;
		*)
			printf 'error: unknown option: %s\n' "$1" >&2
			usage
			exit 1
			;;
	esac
	shift
done

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$repo_root"
git_common_dir="$(git rev-parse --git-common-dir 2>/dev/null || printf '.git')"
case "$git_common_dir" in
	/*)
		common_git_dir="$git_common_dir"
		;;
	*)
		common_git_dir="$repo_root/$git_common_dir"
		;;
esac
main_repo_root="$(CDPATH= cd -- "$common_git_dir/.." && pwd)"

env_file="${ENV_FILE:-.env.prod}"
website_repo_input="${WEBSITE_REPO_DIR:-../agent-tunnel-website}"
case "$website_repo_input" in
	/*)
		website_repo="$website_repo_input"
		;;
	*)
		website_repo="$main_repo_root/$website_repo_input"
		;;
esac
deploy_website_host="${DEPLOY_WEBSITE_HOST:-${DEPLOY_HOST:-}}"
deploy_website_root="${DEPLOY_WEBSITE_ROOT:-/var/www/agentunnel-website}"
deploy_website_tmp_dir="${DEPLOY_WEBSITE_TMP_DIR:-/tmp/agentunnel-website}"
deploy_run_id="${DEPLOY_RUN_ID:-$(date +%Y%m%d%H%M%S)-$$}"
website_build_dir="${WEBSITE_BUILD_DIR:-dist}"
website_dist_dir="$website_repo/$website_build_dir"
ssh_opts='-o LogLevel=ERROR'
scp_opts='-q -o LogLevel=ERROR -o ServerAliveInterval=10 -o ServerAliveCountMax=6'
deploy_website_stage_file="$deploy_website_tmp_dir/$deploy_run_id.tar.gz"
deploy_website_releases_dir="$deploy_website_root/releases"
deploy_website_release_dir="$deploy_website_releases_dir/$deploy_run_id"
deploy_website_current_link="$deploy_website_root/current"
deploy_website_current_tmp_link="$deploy_website_root/current.$deploy_run_id.tmp"
local_archive=''

if [ -z "$deploy_website_host" ]; then
	printf 'error: DEPLOY_WEBSITE_HOST (or DEPLOY_HOST) is required\n' >&2
	exit 1
fi

require_absolute_path DEPLOY_WEBSITE_ROOT "$deploy_website_root"
require_absolute_path DEPLOY_WEBSITE_TMP_DIR "$deploy_website_tmp_dir"
require_cmd npm
require_cmd scp
require_cmd ssh
require_cmd tar
require_website_repo
trap cleanup_local_archive EXIT INT TERM

case "$command" in
	deploy)
		run_plan 5 \
			'Install website dependencies' install_website_dependencies \
			'Build website bundle' build_website_bundle \
			'Validate website bundle' validate_website_bundle \
			'Package website bundle' package_website_bundle \
			'Publish website release' publish_website_release
		;;
	*)
		printf 'error: unknown command: %s\n' "$command" >&2
		usage
		exit 1
		;;
esac
