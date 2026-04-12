#!/bin/sh

set -eu

usage() {
	printf 'usage: %s <command> [--verbose] [--dry-run]\n' "$0" >&2
	printf 'commands: build-linux install-migrator install-relay install sync-schema install-env migrate restart deploy\n' >&2
	printf 'note: deploy commands manage relay artifacts only; nginx/certbot/postgresql installation and host config live under make install-dev/install-prod\n' >&2
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
		'Build Linux binaries')
			printf '🔨'
			;;
		'Install migrator'|'Install relay')
			printf '📦'
			;;
		'Sync schema files')
			printf '🗂️'
			;;
		'Run schema migrations')
			printf '🧭'
			;;
		'Install remote env file')
			printf '⚙️'
			;;
		'Restart relay service')
			printf '🔁'
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

require_env_file() {
	if [ -f "$env_file" ]; then
		return 0
	fi
	printf 'error: env file not found: %s\n' "$env_file" >&2
	exit 1
}

require_build_deps() {
	require_cmd go
}

require_copy_deps() {
	require_cmd rsync
	require_cmd ssh
}

require_scp_deps() {
	require_cmd scp
	require_cmd ssh
}

require_ssh_deps() {
	require_cmd ssh
}

hash_file() {
	if command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{print $1}'
		return
	fi
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
		return
	fi
	printf 'error: need shasum or sha256sum to compute file hashes\n' >&2
	exit 1
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
		last_error_reason="see command output above"
		return 1
	fi

	tmp_output="$(mktemp "${TMPDIR:-/tmp}/agentunnel-deploy.XXXXXX")"
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

acquire_remote_lock() {
	if [ "$dry_run" -eq 1 ] || [ "$remote_lock_held" -eq 1 ]; then
		return 0
	fi

	require_ssh_deps
	run_cmd ssh $ssh_opts "$deploy_host" "sudo /bin/sh -lc 'lock_dir=\"$remote_lock_dir\"; attempts=0; while ! mkdir \"\$lock_dir\" 2>/dev/null; do attempts=\$((attempts + 1)); if [ \"\$attempts\" -ge 300 ]; then echo \"error: timed out waiting for deploy lock $remote_lock_dir\" >&2; exit 1; fi; sleep 1; done'"
	remote_lock_held=1
}

release_remote_lock() {
	if [ "${remote_lock_held:-0}" -ne 1 ] || [ "${dry_run:-0}" -eq 1 ]; then
		return 0
	fi

	ssh $ssh_opts "$deploy_host" "sudo rmdir \"$remote_lock_dir\"" >/dev/null 2>&1 || true
	remote_lock_held=0
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
	if [ "$step_state" = "planned" ]; then
		planned_count=$((planned_count + 1))
	fi
}

run_step() {
	current_step=$((current_step + 1))
	label="$1"
	shift

	step_state="ok"
	step_detail=""
	last_error_reason=""

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

build_deploy_env_file() {
	local_tmp_env="$1"
	sed '/^RELAY_LOG_FILE=/d' "$env_file" >"$local_tmp_env"
	printf '\nRELAY_LOG_FILE=%s\n' "$deploy_relay_log_file" >>"$local_tmp_env"
}

build_linux() {
	debug "repo_root=$repo_root"
	run_cmd mkdir -p "$bin_dir"
	run_cmd env GOOS=linux GOARCH=amd64 go build -ldflags=-s\ -w -o "$relay_bin" ./cmd/relay
	run_cmd env GOOS=linux GOARCH=amd64 go build -ldflags=-s\ -w -o "$migrator_bin" ./cmd/migrate
	set_step_result ok "$bin_dir ready"
}

install_binary_if_changed() {
	label="$1"
	local_path="$2"
	remote_stage="$3"
	remote_install="$4"

	if [ "$dry_run" -eq 1 ]; then
		debug "$label local_sha=<skipped in dry-run>"
		debug "$label remote_sha=<skipped in dry-run>"
		run_cmd rsync -aqz --partial --inplace -e "ssh $ssh_keepalive_opts" "$local_path" "$deploy_host:$remote_stage"
		run_cmd ssh $ssh_opts "$deploy_host" "sudo install -m 0755 $remote_stage $remote_install"
		set_step_result ok 'hash check skipped in dry-run'
		return 0
	fi

	local_sha="$(hash_file "$local_path")"
	debug "$label local_sha=$local_sha"

	remote_sha="$(ssh $ssh_opts "$deploy_host" "sha256sum $remote_install 2>/dev/null | cut -d' ' -f1" 2>/dev/null || true)"
	debug "$label remote_sha=${remote_sha:-missing}"

	if [ -n "$remote_sha" ] && [ "$local_sha" = "$remote_sha" ]; then
		set_step_result ok 'already up to date'
		return 0
	fi

	acquire_remote_lock
	run_cmd rsync -aqz --partial --inplace -e "ssh $ssh_keepalive_opts" "$local_path" "$deploy_host:$remote_stage"

	uploaded_sha="$(ssh $ssh_opts "$deploy_host" "sha256sum $remote_stage | cut -d' ' -f1")"
	debug "$label uploaded_sha=$uploaded_sha"
	if [ "$local_sha" != "$uploaded_sha" ]; then
		last_error_reason="uploaded $label hash mismatch on $deploy_host"
		printf 'error: uploaded %s hash mismatch on %s\n' "$label" "$deploy_host" >&2
		return 1
	fi

	run_cmd ssh $ssh_opts "$deploy_host" "sudo install -m 0755 $remote_stage $remote_install && rm -f $remote_stage"
	set_step_result changed 'uploaded and installed'
}

install_migrator() {
	install_binary_if_changed migrator "$migrator_bin" "$migrator_stage_path" "$deploy_migrator_install_path"
}

install_relay() {
	install_binary_if_changed relay "$relay_bin" "$relay_stage_path" "$deploy_install_path"
}

sync_schema() {
	acquire_remote_lock
	run_cmd ssh $ssh_opts "$deploy_host" "rm -rf $deploy_schema_stage_dir && mkdir -p $deploy_schema_stage_dir"
	run_cmd rsync -aqz --delete -e "ssh $ssh_keepalive_opts" "$repo_root/schema/" "$deploy_host:$deploy_schema_stage_dir/"
	run_cmd ssh $ssh_opts "$deploy_host" "sudo rm -rf $deploy_schema_dir && sudo install -d -m 0755 $deploy_schema_dir && sudo install -m 0644 $deploy_schema_stage_dir/*.sql $deploy_schema_dir/ && rm -rf $deploy_schema_stage_dir"
	set_step_result changed 'remote schema refreshed'
}

install_env() {
	local_tmp_env="$(mktemp "${TMPDIR:-/tmp}/agentunnel-env.XXXXXX")"
	build_deploy_env_file "$local_tmp_env"
	acquire_remote_lock
	if run_cmd scp -q "$local_tmp_env" "$deploy_host:$deploy_env_stage_file"; then
		rm -f "$local_tmp_env"
	else
		status=$?
		rm -f "$local_tmp_env"
		return "$status"
	fi

	run_cmd ssh $ssh_opts "$deploy_host" "sudo install -d -m 0755 $deploy_env_dir && sudo install -m 0600 $deploy_env_stage_file $deploy_env_file && rm -f $deploy_env_stage_file"
	set_step_result changed "installed env with RELAY_LOG_FILE=$deploy_relay_log_file"
}

run_migrate() {
	local_tmp_env="$(mktemp "${TMPDIR:-/tmp}/agentunnel-migrate-env.XXXXXX")"
	build_deploy_env_file "$local_tmp_env"
	acquire_remote_lock
	if run_cmd scp -q "$local_tmp_env" "$deploy_host:$deploy_migrate_env_stage_file"; then
		rm -f "$local_tmp_env"
	else
		status=$?
		rm -f "$local_tmp_env"
		return "$status"
	fi

	remote_cmd="set -e; if $deploy_migrator_install_path --env-file $deploy_migrate_env_stage_file --schema-dir $deploy_schema_dir"
	if [ -n "$migrator_args" ]; then
		remote_cmd="$remote_cmd $migrator_args"
	fi
	remote_cmd="$remote_cmd >/dev/null; then rc=0; else rc=\$?; fi; rm -f $deploy_migrate_env_stage_file; exit \$rc"

	run_cmd ssh $ssh_opts "$deploy_host" "sudo /bin/sh -lc '$remote_cmd'"
	set_step_result ok 'schema is current'
}

restart_service() {
	acquire_remote_lock
	run_cmd ssh $ssh_opts "$deploy_host" "sudo systemctl restart $deploy_service"
	set_step_result changed 'service restarted'
}

run_plan() {
	total_steps="$1"
	shift

	current_step=0
	ok_count=0
	changed_count=0
	failed_count=0
	planned_count=0

	printf 'DEPLOY [%s] env=%s\n' "$deploy_host" "$env_file"
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

	printf '\n📋 SUMMARY [%s]\n' "$deploy_host"
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

bin_dir="${BIN_DIR:-bin}"
env_file="${ENV_FILE:-.env.prod}"
deploy_host="${DEPLOY_HOST:-diarome}"
deploy_relay_path="${DEPLOY_RELAY_PATH:-~/relay}"
deploy_migrator_path="${DEPLOY_MIGRATOR_PATH:-~/relay-migrate}"
deploy_service="${DEPLOY_SERVICE:-agentunnel-relay}"
deploy_install_path="${DEPLOY_INSTALL_PATH:-/usr/local/bin/relay}"
deploy_migrator_install_path="${DEPLOY_MIGRATOR_INSTALL_PATH:-/usr/local/bin/relay-migrate}"
deploy_env_file="${DEPLOY_ENV_FILE:-/etc/agentunnel/relay.env}"
deploy_env_dir="$(dirname "$deploy_env_file")"
deploy_relay_log_file="${DEPLOY_RELAY_LOG_FILE:-/var/log/agentunnel/relay.log}"
deploy_schema_dir="${DEPLOY_SCHEMA_DIR:-/etc/agentunnel/schema}"
deploy_schema_tmp_dir="${DEPLOY_SCHEMA_TMP_DIR:-/tmp/agentunnel-relay-schema}"
migrator_args="${MIGRATOR_ARGS:-}"
deploy_run_id="${DEPLOY_RUN_ID:-$(date +%Y%m%d%H%M%S)-$$}"
remote_lock_dir="${DEPLOY_LOCK_DIR:-/tmp/agentunnel-relay-deploy.lock}"
relay_stage_path="${deploy_relay_path}.${deploy_run_id}"
migrator_stage_path="${deploy_migrator_path}.${deploy_run_id}"
deploy_schema_stage_dir="${deploy_schema_tmp_dir}.${deploy_run_id}"
deploy_env_stage_file="/tmp/agentunnel-relay.env.${deploy_run_id}"
deploy_migrate_env_stage_file="/tmp/agentunnel-relay.migrate.env.${deploy_run_id}"
remote_lock_held=0

relay_bin="$bin_dir/relay"
migrator_bin="$bin_dir/relay-migrate"

ssh_opts='-o LogLevel=ERROR'
ssh_keepalive_opts='-o LogLevel=ERROR -o ServerAliveInterval=10 -o ServerAliveCountMax=6'

trap release_remote_lock EXIT INT TERM

prepare_build_linux() {
	require_build_deps
}

prepare_install_binary() {
	require_build_deps
	require_copy_deps
}

prepare_sync_schema() {
	require_copy_deps
}

prepare_install_env() {
	require_scp_deps
	require_env_file
}

prepare_migrate() {
	require_build_deps
	require_copy_deps
	require_scp_deps
	require_env_file
}

prepare_restart() {
	require_ssh_deps
}

prepare_deploy() {
	prepare_migrate
}

case "$command" in
	build-linux)
		prepare_build_linux
		run_plan 1 \
			'Build Linux binaries' build_linux
		;;
	install-migrator)
		prepare_install_binary
		run_plan 2 \
			'Build Linux binaries' build_linux \
			'Install migrator' install_migrator
		;;
	install-relay)
		prepare_install_binary
		run_plan 2 \
			'Build Linux binaries' build_linux \
			'Install relay' install_relay
		;;
	install)
		prepare_install_binary
		run_plan 3 \
			'Build Linux binaries' build_linux \
			'Install migrator' install_migrator \
			'Install relay' install_relay
		;;
	sync-schema)
		prepare_sync_schema
		run_plan 1 \
			'Sync schema files' sync_schema
		;;
	install-env)
		prepare_install_env
		run_plan 1 \
			'Install remote env file' install_env
		;;
	migrate)
		prepare_migrate
		run_plan 4 \
			'Build Linux binaries' build_linux \
			'Install migrator' install_migrator \
			'Sync schema files' sync_schema \
			'Run schema migrations' run_migrate
		;;
	restart)
		prepare_restart
		run_plan 1 \
			'Restart relay service' restart_service
		;;
	deploy)
		prepare_deploy
		run_plan 7 \
			'Build Linux binaries' build_linux \
			'Install migrator' install_migrator \
			'Sync schema files' sync_schema \
			'Run schema migrations' run_migrate \
			'Install relay' install_relay \
			'Install remote env file' install_env \
			'Restart relay service' restart_service
		;;
	*)
		printf 'error: unknown command: %s\n' "$command" >&2
		usage
		exit 1
		;;
esac
