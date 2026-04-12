#!/bin/sh

set -eu

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
pg_script="${LOCAL_E2E_PG_SCRIPT:-$repo_root/scripts/local-e2e-postgres.sh}"
output_file="${LOCAL_E2E_OUTPUT_FILE:-$repo_root/tmp/local-e2e/latest.log}"
output_dir="$(dirname "$output_file")"
tmp_output="${output_file}.tmp"
cleanup_started=0
up_output="${TMPDIR:-/tmp}/local-e2e-up.$$"
result_recorded=0

record_result() {
	exit_code="$1"
	failed_stage="${2:-}"

	if [ "$result_recorded" -eq 1 ]; then
		return
	fi
	result_recorded=1

	{
		printf '\n== Result ==\n'
		if [ "$exit_code" -eq 0 ]; then
			printf 'result: PASS\n'
		else
			printf 'result: FAIL\n'
			if [ -n "$failed_stage" ]; then
				printf 'failed_stage: %s\n' "$failed_stage"
			fi
		fi
		printf 'exit_code: %s\n' "$exit_code"
	} >>"$tmp_output"
}

cleanup() {
	status=$?
	if [ "$cleanup_started" -eq 1 ]; then
		exit "$status"
	fi
	cleanup_started=1

	set +e

	{
		printf '\n== Cleanup ==\n'
		printf 'finished_at_utc: %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
		printf '$ %s reset\n' "$pg_script"
	} >>"$tmp_output"
	"$pg_script" reset >>"$tmp_output" 2>&1
	cleanup_status=$?
	if [ "$cleanup_status" -eq 0 ]; then
		printf 'cleanup_result: PASS\n' >>"$tmp_output"
	else
		printf 'cleanup_result: FAIL\n' >>"$tmp_output"
		printf 'cleanup_exit_code: %s\n' "$cleanup_status" >>"$tmp_output"
	fi

	rm -f "$up_output"
	mv "$tmp_output" "$output_file"

	final_status="$status"
	if [ "$final_status" -eq 0 ] && [ "$cleanup_status" -ne 0 ]; then
		final_status="$cleanup_status"
	fi

	if [ "$final_status" -eq 0 ]; then
		printf 'local e2e passed; output saved to %s\n' "$output_file"
	else
		printf 'local e2e failed; output saved to %s\n' "$output_file" >&2
	fi

	exit "$final_status"
}

trap cleanup EXIT
trap 'exit 130' INT TERM HUP

mkdir -p "$output_dir"
rm -f "$tmp_output" "$output_file"
: >"$tmp_output"

{
	printf '== Local E2E Run ==\n'
	printf 'started_at_utc: %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
	printf 'repo_root: %s\n' "$repo_root"
	printf 'head: %s\n' "$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
	printf 'output_file: %s\n' "$output_file"
	printf 'pg_image: %s\n' "${LOCAL_E2E_PG_IMAGE:-}"
	printf 'pg_container: %s\n' "${LOCAL_E2E_PG_CONTAINER:-}"
	printf 'pg_volume: %s\n' "${LOCAL_E2E_PG_VOLUME:-}"
	printf 'pg_port: %s\n' "${LOCAL_E2E_PG_PORT:-}"
	printf '\n== Fresh PostgreSQL ==\n'
	printf '$ %s reset\n' "$pg_script"
} >>"$tmp_output"

set +e
"$pg_script" reset >>"$tmp_output" 2>&1
fresh_reset_status=$?
set -e
if [ "$fresh_reset_status" -ne 0 ]; then
	record_result "$fresh_reset_status" "postgres_reset"
	exit "$fresh_reset_status"
fi

{
	printf '\n== PostgreSQL Up ==\n'
	printf '$ %s up\n' "$pg_script"
} >>"$tmp_output"

set +e
"$pg_script" up >"$up_output" 2>&1
up_status=$?
set -e
sed 's#^\([[:space:]]*dsn: \).*#\1<redacted>#' "$up_output" >>"$tmp_output"
if [ "$up_status" -ne 0 ]; then
	record_result "$up_status" "postgres_up"
	exit "$up_status"
fi

dsn="$("$pg_script" dsn)"

{
	printf '\n== E2E Test ==\n'
	printf '$ AGENTUNNEL_RUN_LOCAL_E2E=1 AGENTUNNEL_TEST_DATABASE_URL=<redacted> go test ./internal/e2e -count=1 -v\n'
} >>"$tmp_output"

set +e
AGENTUNNEL_RUN_LOCAL_E2E=1 AGENTUNNEL_TEST_DATABASE_URL="$dsn" \
	go test ./internal/e2e -count=1 -v >>"$tmp_output" 2>&1
test_status=$?
set -e

if [ "$test_status" -eq 0 ]; then
	record_result 0
else
	record_result "$test_status" "go_test"
fi

if [ "$test_status" -ne 0 ]; then
	{
		printf '\n== PostgreSQL Logs ==\n'
		printf '$ %s logs\n' "$pg_script"
	} >>"$tmp_output"
	"$pg_script" logs >>"$tmp_output" 2>&1 || true
fi

exit "$test_status"
