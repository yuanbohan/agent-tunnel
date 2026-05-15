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
nginx_site_path="${RELAY_CN_NGINX_SITE_PATH:-/etc/nginx/sites-available/${domain}}"
remote_timeout="${RELAY_CN_REMOTE_COMMAND_TIMEOUT:-20}"
curl_connect_timeout="${RELAY_CN_CURL_CONNECT_TIMEOUT:-5}"
curl_max_time="${RELAY_CN_CURL_MAX_TIME:-15}"
ssh_options=(
	-o StrictHostKeyChecking=no
	-o BatchMode=yes
	-o ConnectTimeout="${RELAY_CN_SSH_CONNECT_TIMEOUT:-10}"
	-o ServerAliveInterval="${RELAY_CN_SSH_SERVER_ALIVE_INTERVAL:-5}"
	-o ServerAliveCountMax="${RELAY_CN_SSH_SERVER_ALIVE_COUNT_MAX:-2}"
)

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

remote_ssh() {
	ssh "${ssh_options[@]}" "${ssh_user}@${expected_host}" "$@"
}

truncate_text() {
	local text="$1"
	local max_len="${2:-160}"

	if [ "${#text}" -le "$max_len" ]; then
		printf '%s' "$text"
		return
	fi

	printf '%s...' "${text:0:$((max_len - 3))}"
}

join_by_comma_space() {
	local out=""
	local item

	for item in "$@"; do
		if [ -n "$out" ]; then
			out="${out}, "
		fi
		out="${out}${item}"
	done

	printf '%s' "$out"
}

split_http_response() {
	local raw_file="$1"
	local headers_file="$2"
	local body_file="$3"

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
}

print_http_response_summary() {
	local label="$1"
	local path="$2"
	local headers_file="$3"
	local body_file="$4"
	local status_line
	local content_type
	local body_summary
	local html_title

	status_line="$(awk '/^HTTP\// { line = $0 } END { print line }' "$headers_file")"
	content_type="$(awk 'BEGIN { IGNORECASE = 1 } /^Content-Type:/ { sub(/^Content-Type:[[:space:]]*/, "", $0); print; exit }' "$headers_file")"

	printf '\n🔎 %s response\n' "$label" >&2
	[ -n "$status_line" ] && printf 'Status: %s\n' "$status_line" >&2
	printf 'Path: %s\n' "$path" >&2
	[ -n "$content_type" ] && printf 'Content-Type: %s\n' "$content_type" >&2

	if printf '%s' "$content_type" | grep -qi 'text/html'; then
		html_title="$(
			tr '\n' ' ' <"$body_file" |
				sed -n 's:.*<title>[[:space:]]*\([^<][^<]*\)[[:space:]]*</title>.*:\1:p' |
				head -n 1
		)"
		if [ -n "$html_title" ]; then
			printf 'HTML title: %s\n' "$(truncate_text "$html_title" 120)" >&2
		fi
	fi

	body_summary="$(
		awk 'NF { print; exit }' "$body_file" |
			sed 's/[[:space:]]\+/ /g'
	)"
	if [ -n "$body_summary" ] && [ -z "$html_title" ]; then
		printf 'Body summary: %s\n' "$(truncate_text "$body_summary" 160)" >&2
	fi

	if printf '%s' "$content_type" | grep -qi 'text/html' && grep -Eqi '<!doctype html|<html' "$body_file"; then
		printf 'Hint: request likely fell through to the website root instead of an exact relay/nginx websocket location.\n' >&2
	fi
}

check_remote_nginx_routes() {
	local check="nginx-site-routes"
	local site_file="$tmpdir/${check}.conf"
	local summary_file="$tmpdir/${check}.summary"
	local missing=()
	local route

	if ! remote_ssh \
		"timeout ${remote_timeout}s sudo cat '${nginx_site_path}'" \
		>"$site_file" 2>"$tmpdir/${check}.stderr"; then
		fail_result "$check" "could not read ${nginx_site_path}"
		print_diagnostics "${check} stderr" "$tmpdir/${check}.stderr"
		return
	fi

	for route in \
		"/agent/ws" \
		"/device/ws" \
		"/healthz" \
		"/connectivity/daemon/ws" \
		"/connectivity/computer/ws" \
		"/connectivity/tunnel/ws"; do
		if ! grep -Fq "location = ${route}" "$site_file"; then
			missing+=("${route}")
		fi
	done

	if [ "${#missing[@]}" -ne 0 ]; then
		grep -n 'location = /' "$site_file" >"$summary_file" || true
		fail_result "$check" "missing locations in ${nginx_site_path}: $(join_by_comma_space "${missing[@]}"); run make nginx-relay-cn"
		print_diagnostics "${check} configured locations" "$summary_file"
		return
	fi

	pass_result "$check" "${nginx_site_path} contains relay websocket and health routes"
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

	if ! remote_ssh \
		"timeout ${remote_timeout}s curl -ksS --connect-timeout ${curl_connect_timeout} --max-time ${curl_max_time} --resolve ${domain}:443:127.0.0.1 -D - -o - https://${domain}${path}" \
		>"$raw_file" 2>"$tmpdir/${check}.stderr"; then
		fail_result "$check" "request failed for ${path}"
		print_diagnostics "${check} stderr" "$tmpdir/${check}.stderr"
		return
	fi

	split_http_response "$raw_file" "$headers_file" "$body_file"

	code="$(awk '/^HTTP\// { code = $2 } END { print code }' "$raw_file")"

	if [ "$code" != "$expected_code" ]; then
		fail_result "$check" "expected HTTP ${expected_code}, got ${code:-<empty>}"
		print_http_response_summary "$check" "$path" "$headers_file" "$body_file"
		return
	fi

	if [ -n "$body_pattern" ] && ! grep -q "$body_pattern" "$body_file"; then
		fail_result "$check" "HTTP ${code}, body did not match ${body_pattern}"
		print_http_response_summary "$check" "$path" "$headers_file" "$body_file"
		return
	fi

	pass_result "$check" "HTTP ${code} ${path}"
}

http_check_websocket_auth() {
	local check="$1"
	local expected_code="$2"
	local path="$3"
	local bearer_token="${4:-}"
	local raw_file="$tmpdir/${check}.raw"
	local headers_file="$tmpdir/${check}.headers"
	local body_file="$tmpdir/${check}.body"
	local code
	local auth_header=""

	if [ -n "$bearer_token" ]; then
		auth_header="-H 'Authorization: Bearer ${bearer_token}'"
	fi

	if ! remote_ssh \
		"timeout ${remote_timeout}s curl -kisS --http1.1 --connect-timeout ${curl_connect_timeout} --max-time ${curl_max_time} --resolve ${domain}:443:127.0.0.1 https://${domain}${path} \
		 -H 'Connection: Upgrade' \
		 -H 'Upgrade: websocket' \
		 -H 'Sec-WebSocket-Version: 13' \
		 -H 'Sec-WebSocket-Key: SGVsbG8sIHdvcmxkIQ==' \
		 ${auth_header}" \
		>"$raw_file" 2>"$tmpdir/${check}.stderr"; then
		fail_result "$check" "request failed for ${path}"
		print_diagnostics "${check} stderr" "$tmpdir/${check}.stderr"
		return
	fi

	split_http_response "$raw_file" "$headers_file" "$body_file"
	code="$(awk '/^HTTP\// { code = $2 } END { print code }' "$raw_file")"

	if [ "$code" != "$expected_code" ]; then
		fail_result "$check" "expected HTTP ${expected_code}, got ${code:-<empty>}"
		print_http_response_summary "$check" "$path" "$headers_file" "$body_file"
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

check_remote_nginx_routes

if remote_ssh \
	"timeout ${remote_timeout}s test -f '${compose_dir}/.env'" >/dev/null 2>"$tmpdir/remote-env.stderr"; then
	pass_result "remote-env" "${compose_dir}/.env exists"
else
	fail_result "remote-env" "${compose_dir}/.env is missing"
	print_diagnostics "remote-env stderr" "$tmpdir/remote-env.stderr"
fi

if remote_ssh \
	"timeout ${remote_timeout}s sh -lc 'cd ${compose_dir} && sudo docker compose --env-file .env ps'" \
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
	remote_ssh \
		"timeout ${remote_timeout}s curl -fsS --connect-timeout ${curl_connect_timeout} --max-time ${curl_max_time} http://127.0.0.1:8586/healthz" 2>"$tmpdir/local-health.stderr"
)"
if printf '%s\n' "$relay_health" | grep -q '"status":"ok"'; then
	pass_result "local-relay" "127.0.0.1:8586/healthz reports ok"
else
	fail_result "local-relay" "127.0.0.1:8586/healthz did not report ok"
	print_diagnostics "local-relay stderr" "$tmpdir/local-health.stderr"
fi

if remote_ssh \
	"timeout ${remote_timeout}s test -f '${website_root}/current/index.html'" >/dev/null 2>"$tmpdir/website-file.stderr"; then
	pass_result "website-files" "${website_root}/current/index.html exists"
else
	fail_result "website-files" "${website_root}/current/index.html is missing"
	print_diagnostics "website-files stderr" "$tmpdir/website-file.stderr"
fi

http_check_remote "website-root" "200" "/" "<!doctype html\\|<html"
http_check_remote "healthz" "200" "/healthz" '"status":"ok"'
http_check_remote "account-policy" "401" "/api/account/policy" '"code":1016'
http_check_websocket_auth "connectivity-app-ws" "401" "/api/connectivity/ws"
http_check_websocket_auth "connectivity-app-ws-legacy" "401" "/api/connectivity/app/ws"
http_check_websocket_auth "agent-ws" "401" "/agent/ws"
http_check_websocket_auth "device-ws" "401" "/device/ws"
http_check_websocket_auth "connectivity-computer-ws" "401" "/connectivity/computer/ws"
http_check_websocket_auth "connectivity-daemon-ws-legacy" "401" "/connectivity/daemon/ws"
http_check_websocket_auth "connectivity-tunnel-ws" "403" "/connectivity/tunnel/ws" "invalid"

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
