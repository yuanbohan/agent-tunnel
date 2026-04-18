#!/bin/sh

set -eu

require_cmd() {
	if command -v "$1" >/dev/null 2>&1; then
		return 0
	fi
	printf 'error: required command not found: %s\n' "$1" >&2
	exit 1
}

release_validate_version() {
	version="${1:-}"
	if printf '%s\n' "$version" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
		return 0
	fi
	printf 'error: version must look like v0.1.0\n' >&2
	return 1
}

release_asset_name() {
	version="$1"
	os="$2"
	arch="$3"
	printf 'tunnel_%s_%s_%s.tar.gz\n' "$version" "$os" "$arch"
}

release_compatibility_line() {
	version="${1#v}"
	version="${version%%-*}"
	major="${version%%.*}"
	if [ "$major" = "0" ]; then
		rest="${version#*.}"
		minor="${rest%%.*}"
		if [ -n "$minor" ]; then
			printf '%s.%s\n' "$major" "$minor"
			return 0
		fi
	fi
	printf '%s\n' "$major"
}

release_hash_file() {
	if command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{print $1}'
		return 0
	fi
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
		return 0
	fi
	printf 'error: need shasum or sha256sum to compute release checksums\n' >&2
	return 1
}

detect_os() {
	if [ -n "${TUNNEL_INSTALL_OS:-}" ]; then
		printf '%s\n' "$TUNNEL_INSTALL_OS"
		return 0
	fi

	case "$(uname -s)" in
		Darwin)
			printf 'darwin\n'
			;;
		Linux)
			printf 'linux\n'
			;;
		*)
			return 1
			;;
	esac
}

detect_arch() {
	if [ -n "${TUNNEL_INSTALL_ARCH:-}" ]; then
		printf '%s\n' "$TUNNEL_INSTALL_ARCH"
		return 0
	fi

	case "$(uname -m)" in
		x86_64|amd64)
			printf 'amd64\n'
			;;
		arm64|aarch64)
			printf 'arm64\n'
			;;
		*)
			return 1
			;;
	esac
}

read_latest_manifest() {
	curl_fetch "$1"
}

curl_fetch() {
	curl \
		--fail \
		--silent \
		--show-error \
		--location \
		--connect-timeout "${TUNNEL_INSTALL_CONNECT_TIMEOUT:-10}" \
		--max-time "${TUNNEL_INSTALL_MAX_TIME:-120}" \
		--retry "${TUNNEL_INSTALL_RETRY_COUNT:-2}" \
		--retry-delay "${TUNNEL_INSTALL_RETRY_DELAY:-1}" \
		"$@"
}

read_manifest_field() {
	field="$1"
	sed -n "s/.*\"$field\"[[:space:]]*:[[:space:]]*\"\\([^\"]*\\)\".*/\\1/p"
}

verify_signed_payload() {
	payload_path="$1"
	signature_path="$2"
	artifact_name="$3"
	allowed_signers_path="$4"

	if ! ssh-keygen -Y verify \
		-f "$allowed_signers_path" \
		-I tunnel-release \
		-n tunnel-release \
		-s "$signature_path" \
		<"$payload_path" >/dev/null 2>&1
	then
		printf 'error: invalid signature for %s\n' "$artifact_name" >&2
		exit 1
	fi
}

version="${VERSION:-}"
install_base_url="${TUNNEL_INSTALL_BASE_URL:-https://raw.githubusercontent.com/yuanbohan/tunnel/main}"
release_repo="${TUNNEL_RELEASE_REPO:-yuanbohan/tunnel}"
install_dir="${TUNNEL_INSTALL_DIR:-$HOME/.local/bin}"
release_signing_public_key="${TUNNEL_RELEASE_SIGNING_PUBLIC_KEY:-AAAAC3NzaC1lZDI1NTE5AAAAIO+yV8bMgSRdfozlBhqQ+xdFJZ5cAPI2T9sI6OSZRPXZ}"

require_cmd curl
require_cmd tar
require_cmd mktemp
require_cmd ssh-keygen

tmpdir=$(mktemp -d "${TMPDIR:-/tmp}/tunnel-install.XXXXXX")
cleanup() {
	rm -rf "$tmpdir"
}
trap cleanup EXIT INT TERM

ssh_keygen_capability_err="$tmpdir/ssh-keygen-capability.err"
if ! ssh-keygen -Y verify \
	-f /dev/null \
	-I tunnel-release \
	-n tunnel-release \
	-s /dev/null </dev/null 2>"$ssh_keygen_capability_err"
then
	if grep -Eqi 'unknown option|illegal option' "$ssh_keygen_capability_err"; then
		printf 'error: ssh-keygen with -Y verify support is required (OpenSSH 8.1+)\n' >&2
		exit 1
	fi
fi

latest_manifest_path="$tmpdir/latest.json"
latest_manifest_signature_path="$tmpdir/latest.json.sig"
checksums_path="$tmpdir/checksums.txt"
checksums_signature_path="$tmpdir/checksums.txt.sig"
allowed_signers_path="$tmpdir/allowed_signers"
extract_dir="$tmpdir/extract"

printf 'tunnel-release %s %s\n' "ssh-ed25519" "$release_signing_public_key" >"$allowed_signers_path"

if [ -z "$version" ]; then
	curl_fetch -o "$latest_manifest_path" "$install_base_url/latest.json"
	curl_fetch -o "$latest_manifest_signature_path" "$install_base_url/latest.json.sig"
	verify_signed_payload "$latest_manifest_path" "$latest_manifest_signature_path" "latest.json" "$allowed_signers_path"

	manifest_json=$(cat "$latest_manifest_path")
	version=$(printf '%s' "$manifest_json" | read_manifest_field version)
	if [ -z "$version" ]; then
		printf 'error: latest.json did not contain a version\n' >&2
		exit 1
	fi

	manifest_line=$(printf '%s' "$manifest_json" | read_manifest_field compatibility_line)
	if [ -z "$manifest_line" ]; then
		printf 'error: latest.json did not contain compatibility_line\n' >&2
		exit 1
	fi
	if [ "$manifest_line" != "$(release_compatibility_line "$version")" ]; then
		printf 'error: latest.json compatibility_line does not match version %s\n' "$version" >&2
		exit 1
	fi
fi

release_validate_version "$version"

os=$(detect_os) || {
	printf 'error: unsupported operating system\n' >&2
	exit 1
}
arch=$(detect_arch) || {
	printf 'error: unsupported architecture\n' >&2
	exit 1
}

case "$os/$arch" in
	darwin/arm64|darwin/amd64|linux/amd64|linux/arm64)
		;;
	*)
		printf 'error: unsupported target %s/%s\n' "$os" "$arch" >&2
		exit 1
		;;
esac

asset_name=$(release_asset_name "$version" "$os" "$arch")
release_base_url="${TUNNEL_RELEASE_BASE_URL:-https://github.com/$release_repo/releases/download/$version}"
asset_url="$release_base_url/$asset_name"
checksums_url="$release_base_url/checksums.txt"
checksums_signature_url="$release_base_url/checksums.txt.sig"
archive_path="$tmpdir/$asset_name"

mkdir -p "$extract_dir"
curl_fetch -o "$archive_path" "$asset_url"
curl_fetch -o "$checksums_path" "$checksums_url"
curl_fetch -o "$checksums_signature_path" "$checksums_signature_url"

verify_signed_payload "$checksums_path" "$checksums_signature_path" "checksums.txt" "$allowed_signers_path"

expected_checksum=$(awk "/  $asset_name\$/ {print \$1; exit}" "$checksums_path")
if [ -z "$expected_checksum" ]; then
	printf 'error: checksums.txt did not contain %s\n' "$asset_name" >&2
	exit 1
fi

actual_checksum=$(release_hash_file "$archive_path")
if [ "$actual_checksum" != "$expected_checksum" ]; then
	printf 'error: checksum mismatch for %s\n' "$asset_name" >&2
	exit 1
fi

archive_members=$(tar -tzf "$archive_path")
if [ "$archive_members" != "tunnel" ]; then
	printf 'error: archive %s must contain only tunnel\n' "$asset_name" >&2
	exit 1
fi

tar -xzf "$archive_path" -C "$extract_dir" tunnel
if [ ! -f "$extract_dir/tunnel" ] || [ -L "$extract_dir/tunnel" ]; then
	printf 'error: archive %s did not contain a safe tunnel binary\n' "$asset_name" >&2
	exit 1
fi

mkdir -p "$install_dir"
tmp_bin="$install_dir/.tunnel.$$.tmp"
cp "$extract_dir/tunnel" "$tmp_bin"
chmod 0755 "$tmp_bin"
mv -f "$tmp_bin" "$install_dir/tunnel"

printf 'installed tunnel %s to %s/tunnel\n' "$version" "$install_dir"

case ":$PATH:" in
	*:"$install_dir":*)
		;;
	*)
		printf 'add %s to PATH to run tunnel from new shells\n' "$install_dir"
		;;
esac
