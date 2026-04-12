#!/bin/sh

set -eu

usage() {
	printf 'usage: %s <dev|prod> [--verbose] [--dry-run]\n' "$0" >&2
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

status_text() {
	case "${1:-}" in
		ok)
			printf 'OK'
			;;
		changed)
			printf 'CHANGED'
			;;
		planned)
			printf 'PLANNED'
			;;
		failed)
			printf 'FAILED'
			;;
		*)
			printf '%s' "${1:-UNKNOWN}"
			;;
	esac
}

print_step() {
	printf '%s [%d/%d] %s' "$(status_text "$step_state")" "$current_step" "$total_steps" "$label"
	if [ -n "$step_detail" ]; then
		printf ' - %s' "$step_detail"
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
		last_error_reason='see command output above'
		return 1
	fi

	tmp_output="$(mktemp "${TMPDIR:-/tmp}/agentunnel-install.XXXXXX")"
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

set_step_result() {
	step_state="$1"
	step_detail="${2:-}"
}

record_step() {
	case "$step_state" in
		ok)
			ok_count=$((ok_count + 1))
			;;
		changed)
			changed_count=$((changed_count + 1))
			;;
		planned)
			planned_count=$((planned_count + 1))
			;;
	esac
}

run_step() {
	current_step=$((current_step + 1))
	label="$1"
	shift

	step_state='ok'
	step_detail=''
	last_error_reason=''

	if "$@"; then
		record_step
		print_step
		return 0
	fi

	failed_count=$((failed_count + 1))
	step_state='failed'
	step_detail="$last_error_reason"
	print_step >&2
	exit 1
}

escape_sed_replacement() {
	printf '%s' "$1" | sed 's/[\/&]/\\&/g'
}

render_template() {
	template_path="$1"
	output_path="$2"
	escaped_server_names="$(escape_sed_replacement "$server_names")"
	escaped_upstream_addr="$(escape_sed_replacement "$nginx_upstream_addr")"
	escaped_certbot_webroot="$(escape_sed_replacement "$certbot_webroot")"
	escaped_primary_domain="$(escape_sed_replacement "$primary_domain")"

	sed \
		-e "s/__SERVER_NAMES__/$escaped_server_names/g" \
		-e "s/__UPSTREAM_ADDR__/$escaped_upstream_addr/g" \
		-e "s/__CERTBOT_WEBROOT__/$escaped_certbot_webroot/g" \
		-e "s/__PRIMARY_DOMAIN__/$escaped_primary_domain/g" \
		"$template_path" >"$output_path"
}

looks_like_email() {
	case "$1" in
		*@*|*@*.*)
			case "$1" in
				*" "*|*"	"*|@*|*@)
					return 1
					;;
				*)
					return 0
					;;
			esac
			;;
		*)
			return 1
			;;
	esac
}

resolve_certbot_email() {
	if [ -n "$certbot_email" ]; then
		if looks_like_email "$certbot_email"; then
			set_step_result ok "using $certbot_email"
			return 0
		fi
		last_error_reason='INSTALL_CERTBOT_EMAIL must look like an email address'
		return 1
	fi

	if [ ! -t 0 ]; then
		last_error_reason='INSTALL_CERTBOT_EMAIL is required for install-prod when stdin is not interactive'
		return 1
	fi

	while :; do
		printf 'INSTALL_CERTBOT_EMAIL: ' >&2
		IFS= read -r certbot_email || {
			last_error_reason='INSTALL_CERTBOT_EMAIL is required for install-prod'
			return 1
		}
		if looks_like_email "$certbot_email"; then
			set_step_result ok "using $certbot_email"
			return 0
		fi
		printf 'error: please enter a valid email address\n' >&2
	done
}

detect_dev_server_names() {
	if [ -n "${INSTALL_DEV_SERVER_NAMES:-}" ]; then
		printf '%s\n' "$INSTALL_DEV_SERVER_NAMES"
		return
	fi

	resolved_host="$(ssh -G "$install_host" 2>/dev/null | awk '/^hostname / { print $2; exit }' || true)"
	case "$resolved_host" in
		''|*[!0-9.]*)
			printf '_\n'
			;;
		*)
			printf '%s _\n' "$resolved_host"
			;;
	esac
}

remote_file_sha() {
	remote_path="$1"
	ssh $ssh_opts "$install_host" "sha256sum $remote_path 2>/dev/null | cut -d' ' -f1" 2>/dev/null || true
}

install_remote_file_if_changed() {
	local_path="$1"
	remote_path="$2"
	remote_mode="$3"

	local_sha="$(hash_file "$local_path")"
	remote_sha="$(remote_file_sha "$remote_path")"
	debug "sync local=$local_path local_sha=$local_sha remote=$remote_path remote_sha=${remote_sha:-missing}"

	if [ -n "$remote_sha" ] && [ "$local_sha" = "$remote_sha" ]; then
		set_step_result ok 'already current'
		return 0
	fi

	if [ "$dry_run" -eq 1 ]; then
		set_step_result planned "would sync $(basename "$remote_path")"
		return 0
	fi

	stage_path="/tmp/agentunnel-install.$install_run_id.$(basename "$remote_path")"
	run_cmd scp -q "$local_path" "$install_host:$stage_path"
	run_cmd ssh $ssh_opts "$install_host" "sudo install -d -m 0755 $(dirname "$remote_path") && sudo install -m $remote_mode $stage_path $remote_path && rm -f $stage_path"
	set_step_result changed "synced $(basename "$remote_path")"
}

check_remote_host() {
	if ! remote_os="$(ssh $ssh_opts "$install_host" ". /etc/os-release && printf '%s %s' \"\$ID\" \"\$VERSION_ID\"")"; then
		last_error_reason="failed to reach $install_host"
		return 1
	fi

	case "$remote_os" in
		ubuntu*|debian*)
			;;
		*)
			last_error_reason="unsupported remote OS: $remote_os"
			return 1
			;;
	esac

	if ! ssh $ssh_opts "$install_host" "sudo -n true" >/dev/null 2>&1; then
		last_error_reason="passwordless sudo is required on $install_host"
		return 1
	fi

	set_step_result ok "$remote_os; sudo ready"
}

install_packages() {
	if ! missing_packages="$(ssh $ssh_opts "$install_host" "for pkg in $required_packages; do dpkg -s \$pkg >/dev/null 2>&1 || printf '%s ' \$pkg; done")"; then
		last_error_reason="failed to inspect packages on $install_host"
		return 1
	fi

	missing_packages="$(printf '%s' "$missing_packages" | awk '{$1=$1; print}')"
	if [ -z "$missing_packages" ]; then
		set_step_result ok 'already installed'
		return 0
	fi

	if [ "$dry_run" -eq 1 ]; then
		set_step_result planned "would install: $missing_packages"
		return 0
	fi

	run_cmd ssh $ssh_opts "$install_host" "sudo env DEBIAN_FRONTEND=noninteractive apt-get update && sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y $missing_packages"
	set_step_result changed "installed: $missing_packages"
}

enable_postgresql() {
	if ! pg_enabled="$(ssh $ssh_opts "$install_host" "systemctl is-enabled postgresql 2>/dev/null || true")"; then
		last_error_reason="failed to inspect postgresql.service on $install_host"
		return 1
	fi
	if ! pg_active="$(ssh $ssh_opts "$install_host" "systemctl is-active postgresql 2>/dev/null || true")"; then
		last_error_reason="failed to inspect postgresql.service state on $install_host"
		return 1
	fi

	if [ "$pg_enabled" = 'enabled' ] && [ "$pg_active" = 'active' ]; then
		set_step_result ok 'postgresql active'
		return 0
	fi

	if [ "$dry_run" -eq 1 ]; then
		set_step_result planned 'would enable/start postgresql'
		return 0
	fi

	run_cmd ssh $ssh_opts "$install_host" "sudo systemctl enable --now postgresql"
	set_step_result changed 'postgresql enabled and running'
}

sync_nginx_restart_override() {
	install_remote_file_if_changed "$repo_root/deploy/systemd/nginx-restart.conf" "/etc/systemd/system/nginx.service.d/agentunnel-restart.conf" 0644
}

sync_websocket_map() {
	install_remote_file_if_changed "$repo_root/deploy/nginx/websocket_map.conf" "/etc/nginx/conf.d/websocket_map.conf" 0644
}

sync_http_site() {
	tmp_site="$(mktemp "${TMPDIR:-/tmp}/agentunnel-http-site.XXXXXX")"
	render_template "$repo_root/deploy/nginx/agentunnel-http.conf.template" "$tmp_site"
	if install_remote_file_if_changed "$tmp_site" "$nginx_site_path" 0644; then
		rm -f "$tmp_site"
		return 0
	fi
	status=$?
	rm -f "$tmp_site"
	return $status
}

sync_tls_site() {
	tmp_site="$(mktemp "${TMPDIR:-/tmp}/agentunnel-tls-site.XXXXXX")"
	render_template "$repo_root/deploy/nginx/agentunnel-tls.conf.template" "$tmp_site"
	if install_remote_file_if_changed "$tmp_site" "$nginx_site_path" 0644; then
		rm -f "$tmp_site"
		return 0
	fi
	status=$?
	rm -f "$tmp_site"
	return $status
}

activate_nginx_site() {
	if [ "$dry_run" -eq 1 ]; then
		set_step_result planned "would enable $nginx_site_name and restart nginx"
		return 0
	fi

	run_cmd ssh $ssh_opts "$install_host" "sudo install -d -m 0755 /etc/nginx/sites-available /etc/nginx/sites-enabled && sudo ln -sfn $nginx_site_path /etc/nginx/sites-enabled/$nginx_site_name && sudo rm -f /etc/nginx/sites-enabled/default && sudo systemctl daemon-reload && sudo nginx -t && sudo systemctl enable nginx >/dev/null && sudo systemctl restart nginx && sudo systemctl is-active --quiet nginx"
	set_step_result changed "nginx restarted with $nginx_site_name"
}

sync_certbot_reload_hook() {
	install_remote_file_if_changed "$repo_root/deploy/certbot/reload-nginx.sh" "/etc/letsencrypt/renewal-hooks/deploy/reload-nginx.sh" 0755
}

issue_or_refresh_certificate() {
	if ! cert_exists="$(ssh $ssh_opts "$install_host" "sudo test -f /etc/letsencrypt/live/$primary_domain/fullchain.pem && echo yes || echo no")"; then
		last_error_reason="failed to inspect certbot state on $install_host"
		return 1
	fi

	if [ "$dry_run" -eq 1 ]; then
		set_step_result planned "would issue/refresh cert for $server_names"
		return 0
	fi

	certbot_cmd="sudo install -d -m 0755 $certbot_webroot && sudo certbot certonly --webroot -w $certbot_webroot --non-interactive --agree-tos --keep-until-expiring -m $certbot_email"
	for domain in $server_names; do
		certbot_cmd="$certbot_cmd -d $domain"
	done

	run_cmd ssh $ssh_opts "$install_host" "$certbot_cmd"
	if [ "$cert_exists" = 'yes' ]; then
		set_step_result ok "certificate already current for $primary_domain"
	else
		set_step_result changed "certificate ready for $primary_domain"
	fi
}

enable_certbot_timer() {
	if ! timer_enabled="$(ssh $ssh_opts "$install_host" "systemctl is-enabled certbot.timer 2>/dev/null || true")"; then
		last_error_reason="failed to inspect certbot.timer on $install_host"
		return 1
	fi
	if ! timer_active="$(ssh $ssh_opts "$install_host" "systemctl is-active certbot.timer 2>/dev/null || true")"; then
		last_error_reason="failed to inspect certbot.timer state on $install_host"
		return 1
	fi

	if [ "$timer_enabled" = 'enabled' ] && [ "$timer_active" = 'active' ]; then
		set_step_result ok 'certbot.timer active'
		return 0
	fi

	if [ "$dry_run" -eq 1 ]; then
		set_step_result planned 'would enable/start certbot.timer'
		return 0
	fi

	run_cmd ssh $ssh_opts "$install_host" "sudo systemctl enable --now certbot.timer"
	set_step_result changed 'certbot.timer enabled and running'
}

run_plan() {
	total_steps="$1"
	shift

	current_step=0
	ok_count=0
	changed_count=0
	planned_count=0
	failed_count=0

	printf 'INSTALL [%s] host=%s env=%s\n' "$install_env" "$install_host" "$env_file"

	while [ "$#" -gt 0 ]; do
		label="$1"
		fn="$2"
		run_step "$label" "$fn"
		shift 2
	done

	printf '\nSUMMARY [%s]\n' "$install_host"
	printf '  ok=%d  changed=%d  planned=%d  failed=%d\n' "$ok_count" "$changed_count" "$planned_count" "$failed_count"
}

install_env="${1:-}"
if [ -z "$install_env" ]; then
	usage
	exit 1
fi
shift

verbose=0
dry_run=0
if truthy "${INSTALL_VERBOSE:-0}"; then
	verbose=1
fi
if truthy "${INSTALL_DRY_RUN:-0}"; then
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

case "$install_env" in
	dev|prod)
		;;
	*)
		printf 'error: unsupported install environment: %s\n' "$install_env" >&2
		usage
		exit 1
		;;
esac

require_cmd ssh
require_cmd scp

repo_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$repo_root"

env_file="${ENV_FILE:-.env.prod}"
install_host="${INSTALL_HOST:-${DEPLOY_HOST:-}}"
nginx_upstream_addr="${INSTALL_NGINX_UPSTREAM_ADDR:-127.0.0.1:8586}"
certbot_webroot="${INSTALL_CERTBOT_WEBROOT:-/var/www/certbot}"
certbot_email="${INSTALL_CERTBOT_EMAIL:-}"
install_run_id="${INSTALL_RUN_ID:-$(date +%Y%m%d%H%M%S)-$$}"
ssh_opts='-o LogLevel=ERROR'

if [ -z "$install_host" ]; then
	printf 'error: INSTALL_HOST (or DEPLOY_HOST) is required\n' >&2
	exit 1
fi

if [ "$install_env" = 'dev' ]; then
	required_packages='nginx postgresql'
	server_names="$(detect_dev_server_names)"
	primary_domain=''
	default_site_name='agentunnel-dev'
else
	required_packages='nginx postgresql certbot'
	server_names="${INSTALL_PROD_SERVER_NAMES:-diaro.me www.diaro.me}"
	primary_domain="${INSTALL_PROD_PRIMARY_DOMAIN:-diaro.me}"
	default_site_name="$primary_domain"
fi

nginx_site_name="${INSTALL_NGINX_SITE_NAME:-$default_site_name}"
nginx_site_path="/etc/nginx/sites-available/$nginx_site_name"

if [ "$install_env" = 'prod' ]; then
	run_plan 13 \
		'Resolve certbot email' resolve_certbot_email \
		'Check remote host' check_remote_host \
		'Install packages' install_packages \
		'Enable PostgreSQL' enable_postgresql \
		'Sync nginx restart override' sync_nginx_restart_override \
		'Sync nginx websocket map' sync_websocket_map \
		'Sync bootstrap nginx site' sync_http_site \
		'Activate bootstrap nginx site' activate_nginx_site \
		'Sync certbot reload hook' sync_certbot_reload_hook \
		'Issue or refresh certificate' issue_or_refresh_certificate \
		'Sync TLS nginx site' sync_tls_site \
		'Enable certbot timer' enable_certbot_timer \
		'Activate TLS nginx site' activate_nginx_site
else
	run_plan 7 \
		'Check remote host' check_remote_host \
		'Install packages' install_packages \
		'Enable PostgreSQL' enable_postgresql \
		'Sync nginx restart override' sync_nginx_restart_override \
		'Sync nginx websocket map' sync_websocket_map \
		'Sync nginx site' sync_http_site \
		'Activate nginx site' activate_nginx_site
fi
