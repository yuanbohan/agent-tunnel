#!/usr/bin/env bash
set -uo pipefail

domain="${RELAY_CN_DOMAIN:-agentunnel.cn}"
stun_domain="${RELAY_CN_STUN_DOMAIN:-stun.${domain}}"
stun_port="${RELAY_CN_STUN_PORT:-3478}"
stun_target="${RELAY_CN_STUN_TARGET:-${stun_domain}:${stun_port}}"
expected_host="${RELAY_CN_HOST:-8.133.195.191}"
ssh_user="${RELAY_CN_SSH_USER:-ubuntu}"
compose_dir="${RELAY_CN_COMPOSE_DIR:-/opt/agentunnel/compose}"
website_root="${RELAY_CN_WEBSITE_ROOT:-/var/www/agentunnel-website}"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

results_file="$tmpdir/results.tsv"
compose_ps_file="$tmpdir/compose-ps.txt"
: >"$results_file"

has_failures=0

repeat_char() {
	local char="$1"
	local count="$2"
	local out=""
	local i
	for ((i = 0; i < count; i++)); do
		out="${out}${char}"
	done
	printf '%s' "$out"
}

record_result() {
	local check="$1"
	local status="$2"
	local details="$3"
	printf '%s\t%s\t%s\n' "$check" "$status" "$details" >>"$results_file"
}

pass_result() {
	record_result "$1" "✅ PASS" "$2"
}

fail_result() {
	record_result "$1" "❌ FAIL" "$2"
	has_failures=1
}

print_results_table() {
	local check_header="Check"
	local status_header="Result"
	local details_header="Details"
	local check_width="${#check_header}"
	local status_width="${#status_header}"
	local details_width="${#details_header}"
	local check
	local status
	local details

	while IFS="$(printf '\t')" read -r check status details; do
		[ "${#check}" -gt "$check_width" ] && check_width="${#check}"
		[ "${#status}" -gt "$status_width" ] && status_width="${#status}"
		[ "${#details}" -gt "$details_width" ] && details_width="${#details}"
	done <"$results_file"

	local border="+-$(repeat_char "-" "$check_width")-+-$(repeat_char "-" "$status_width")-+-$(repeat_char "-" "$details_width")-+"

	printf '%s\n' "$border"
	printf '| %-*s | %-*s | %-*s |\n' "$check_width" "$check_header" "$status_width" "$status_header" "$details_width" "$details_header"
	printf '%s\n' "$border"
	while IFS="$(printf '\t')" read -r check status details; do
		printf '| %-*s | %-*s | %-*s |\n' "$check_width" "$check" "$status_width" "$status" "$details_width" "$details"
	done <"$results_file"
	printf '%s\n' "$border"
}

print_diagnostics() {
	local label="$1"
	local file="$2"

	if [ -f "$file" ]; then
		printf '\n🔎 %s\n' "$label" >&2
		cat "$file" >&2
	fi
}

http_check_remote() {
	local check="$1"
	local expected_code="$2"
	local path="$3"
	local body_pattern="${4:-}"
	local headers_file="$tmpdir/${check}.headers"
	local body_file="$tmpdir/${check}.body"
	local raw_file="$tmpdir/${check}.raw"
	local code

	if ! ssh -o StrictHostKeyChecking=no "${ssh_user}@${expected_host}" \
		"curl -ksS --resolve ${domain}:443:127.0.0.1 -D - -o - https://${domain}${path}" \
		>"$raw_file" 2>"$tmpdir/${check}.stderr"; then
		fail_result "$check" "request failed for ${path}"
		print_diagnostics "${check} stderr" "$tmpdir/${check}.stderr"
		return
	fi

	awk '{
		line = $0
		sub(/\r$/, "", line)
		if (body) {
			print line
		} else if (line == "") {
			body = 1
		}
	}' "$raw_file" >"$body_file"

	awk '{
		line = $0
		sub(/\r$/, "", line)
		if (!body) {
			print line
		}
		if (line == "") {
			body = 1
		}
	}' "$raw_file" >"$headers_file"

	code="$(awk '/^HTTP\// { code = $2 } END { print code }' "$raw_file")"

	if [ "$code" != "$expected_code" ]; then
		fail_result "$check" "expected HTTP ${expected_code}, got ${code:-<empty>}"
		print_diagnostics "${check} headers" "$headers_file"
		print_diagnostics "${check} body" "$body_file"
		return
	fi

	if [ -n "$body_pattern" ] && ! grep -q "$body_pattern" "$body_file"; then
		fail_result "$check" "HTTP ${code}, body did not match ${body_pattern}"
		print_diagnostics "${check} body" "$body_file"
		return
	fi

	pass_result "$check" "HTTP ${code} ${path}"
}

http_check_websocket_auth() {
	local check="$1"
	local expected_code="$2"
	local path="$3"
	local raw_file="$tmpdir/${check}.raw"
	local code

	if ! ssh -o StrictHostKeyChecking=no "${ssh_user}@${expected_host}" \
		"curl -kisS --http1.1 --resolve ${domain}:443:127.0.0.1 https://${domain}${path} \
		 -H 'Connection: Upgrade' \
		 -H 'Upgrade: websocket' \
		 -H 'Sec-WebSocket-Version: 13' \
		 -H 'Sec-WebSocket-Key: SGVsbG8sIHdvcmxkIQ=='" \
		>"$raw_file" 2>"$tmpdir/${check}.stderr"; then
		fail_result "$check" "request failed for ${path}"
		print_diagnostics "${check} stderr" "$tmpdir/${check}.stderr"
		return
	fi

	code="$(awk '/^HTTP\// { code = $2 } END { print code }' "$raw_file")"

	if [ "$code" != "$expected_code" ]; then
		fail_result "$check" "expected HTTP ${expected_code}, got ${code:-<empty>}"
		print_diagnostics "${check} response" "$raw_file"
		return
	fi

	pass_result "$check" "HTTP ${code} ${path}"
}

printf '🚦 relay-cn status for %s\n' "$domain"
printf '   target host: %s\n' "$expected_host"
printf '   STUN host: %s\n\n' "$stun_target"

resolved_host="$(dig +short "${domain}" A | tail -n 1 || true)"
if [ -z "$resolved_host" ]; then
	fail_result "dns" "A record for ${domain} is empty"
elif [ "$resolved_host" != "$expected_host" ]; then
	fail_result "dns" "resolved ${resolved_host}, expected ${expected_host}"
else
	pass_result "dns" "${domain} -> ${resolved_host}"
fi

resolved_stun_host="$(dig +short "${stun_domain}" A | tail -n 1 || true)"
if [ -z "$resolved_stun_host" ]; then
	fail_result "dns-stun" "A record for ${stun_domain} is empty"
elif [ "$resolved_stun_host" != "$expected_host" ]; then
	fail_result "dns-stun" "resolved ${resolved_stun_host}, expected ${expected_host}"
else
	pass_result "dns-stun" "${stun_domain} -> ${resolved_stun_host}"
fi

if ssh -o StrictHostKeyChecking=no "${ssh_user}@${expected_host}" \
	"test -f '${compose_dir}/.env'" >/dev/null 2>"$tmpdir/remote-env.stderr"; then
	pass_result "remote-env" "${compose_dir}/.env exists"
else
	fail_result "remote-env" "${compose_dir}/.env is missing"
	print_diagnostics "remote-env stderr" "$tmpdir/remote-env.stderr"
fi

if ssh -o StrictHostKeyChecking=no "${ssh_user}@${expected_host}" \
	"cd '${compose_dir}' && sudo docker compose --env-file .env ps" \
	>"$compose_ps_file" 2>"$tmpdir/compose-ps.stderr"; then
	if grep -q "compose-relay-1" "$compose_ps_file" && grep -q "compose-postgres-1" "$compose_ps_file" && grep -q "compose-stun-1" "$compose_ps_file"; then
		pass_result "compose" "relay, postgres, and stun containers are present"
	else
		fail_result "compose" "docker compose ps did not list relay, postgres, and stun"
		print_diagnostics "compose ps" "$compose_ps_file"
	fi
else
	fail_result "compose" "docker compose ps failed"
	print_diagnostics "compose stderr" "$tmpdir/compose-ps.stderr"
fi

relay_health="$(
	ssh -o StrictHostKeyChecking=no "${ssh_user}@${expected_host}" \
		"curl -fsS http://127.0.0.1:8586/healthz" 2>"$tmpdir/local-health.stderr"
)"
if printf '%s\n' "$relay_health" | grep -q '"status":"ok"'; then
	pass_result "local-relay" "127.0.0.1:8586/healthz reports ok"
else
	fail_result "local-relay" "127.0.0.1:8586/healthz did not report ok"
	print_diagnostics "local-relay stderr" "$tmpdir/local-health.stderr"
fi

if ssh -o StrictHostKeyChecking=no "${ssh_user}@${expected_host}" \
	"test -f '${website_root}/current/index.html'" >/dev/null 2>"$tmpdir/website-file.stderr"; then
	pass_result "website-files" "${website_root}/current/index.html exists"
else
	fail_result "website-files" "${website_root}/current/index.html is missing"
	print_diagnostics "website-files stderr" "$tmpdir/website-file.stderr"
fi

http_check_remote "website-root" "200" "/" "<!doctype html\\|<html"
http_check_remote "healthz" "200" "/healthz" '"status":"ok"'
http_check_remote "api-sessions" "401" "/api/sessions" '"code":1016'
http_check_websocket_auth "connectivity-app-ws" "401" "/api/connectivity/app/ws"
http_check_websocket_auth "agent-ws" "401" "/agent/ws"
http_check_websocket_auth "device-ws" "401" "/device/ws"
http_check_websocket_auth "connectivity-daemon-ws" "401" "/connectivity/daemon/ws"
http_check_websocket_auth "connectivity-tunnel-ws" "403" "/connectivity/tunnel/ws?token=invalid"

stun_check_out="$tmpdir/stun-check.out"
if ./scripts/stun-check.sh "$stun_target" >"$stun_check_out" 2>"$tmpdir/stun-check.stderr"; then
	stun_details="$(tr '\n' ' ' <"$stun_check_out" | sed 's/[[:space:]]*$//')"
	pass_result "stun-binding" "${stun_details:-valid Binding response from ${stun_target}}"
else
	fail_result "stun-binding" "no valid Binding response from ${stun_target}"
	print_diagnostics "stun-binding stdout" "$stun_check_out"
	print_diagnostics "stun-binding stderr" "$tmpdir/stun-check.stderr"
fi

printf '\n📋 Summary\n'
print_results_table

printf '\n🐳 Compose\n'
if [ -f "$compose_ps_file" ]; then
	cat "$compose_ps_file"
else
	printf 'docker compose ps output unavailable\n'
fi

if [ "$has_failures" -eq 0 ]; then
	printf '\n🎉 relay-cn status checks passed\n'
else
	printf '\n🚨 relay-cn status checks failed\n' >&2
	exit 1
fi
