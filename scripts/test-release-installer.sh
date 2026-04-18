#!/bin/sh

set -eu

script_dir=$(CDPATH= cd -- "$(dirname "$0")" && pwd)

# shellcheck source=/dev/null
. "$script_dir/release-common.sh"

replace_checksum_entry() {
	checksums_file="$1"
	asset_name="$2"
	checksum="$3"
	awk -v asset="$asset_name" -v checksum="$checksum" '
		$2 == asset {
			print checksum "  " asset
			next
		}
		{ print }
	' "$checksums_file" >"$checksums_file.tmp"
	mv "$checksums_file.tmp" "$checksums_file"
}

sign_checksums_file() {
	checksums_file="$1"
	signature_path="$2"
	TUNNEL_RELEASE_SIGNING_PRIVATE_KEY="$release_signing_key" "$go_bin" run ./cmd/release-sign sign "$checksums_file" "$signature_path" >/dev/null
}

start_fixture_server() {
	fixture_dir="$1"
	port_file="$2"
	server_log="$fixture_dir/http-server.log"
	python3 - <<'PY' "$fixture_dir" "$port_file" >"$server_log" 2>&1 &
import functools
import http.server
import socketserver
import sys

root = sys.argv[1]
port_file = sys.argv[2]
handler = functools.partial(http.server.SimpleHTTPRequestHandler, directory=root)
with socketserver.TCPServer(("127.0.0.1", 0), handler) as httpd:
    with open(port_file, "w", encoding="utf-8") as fh:
        fh.write(str(httpd.server_address[1]))
    httpd.serve_forever()
PY
	server_pid=$!

	for _ in 1 2 3 4 5 6 7 8 9 10; do
		if [ -s "$port_file" ]; then
			printf '%s\n' "$server_pid"
			return 0
		fi
		sleep 1
	done

	printf 'error: fixture server did not start\n' >&2
	kill "$server_pid" >/dev/null 2>&1 || true
	wait "$server_pid" 2>/dev/null || true
	exit 1
}

tmpdir=$(mktemp -d "${TMPDIR:-/tmp}/tunnel-installer-test.XXXXXX")
cleanup() {
	if [ -n "${server_pid:-}" ]; then
		kill "$server_pid" >/dev/null 2>&1 || true
		wait "$server_pid" 2>/dev/null || true
	fi
	rm -rf "$tmpdir"
}
trap cleanup EXIT INT TERM

fixture_root="$tmpdir/fixture"
release_root="$fixture_root/releases/download"
mkdir -p "$release_root"
go_bin="${GO:-go}"
signing_private_key="$tmpdir/release-signing-private.pem"
signing_public_key="$tmpdir/release-signing-public.txt"
"$go_bin" run ./cmd/release-sign keygen "$signing_private_key" "$signing_public_key" >/dev/null
release_signing_key=$(cat "$signing_private_key")
release_signing_public_key=$(awk 'NR==1 {print $2}' "$signing_public_key")
repo_relay_version=$("$go_bin" run ./cmd/relay version | awk 'NR==1 {print $2}')
if [ -z "$repo_relay_version" ]; then
	printf 'error: could not determine current relay version\n' >&2
	exit 1
fi
version="${TEST_RELEASE_VERSION:-$(release_fixture_version "$repo_relay_version")}"

GO="$go_bin" RELEASE_DIR="$release_root" TUNNEL_RELEASE_SIGNING_PRIVATE_KEY="$release_signing_key" TUNNEL_RELEASE_SIGNING_PUBLIC_KEY="$release_signing_public_key" "$script_dir/release-package.sh" "$version" >/dev/null
"$script_dir/render-latest-manifest.sh" "$version" >"$fixture_root/latest.json"

port_file="$tmpdir/port"
server_pid=$(start_fixture_server "$fixture_root" "$port_file")
port=$(cat "$port_file")
base_url="http://127.0.0.1:$port"
release_base_url="$base_url/releases/download/$version"

home_dir="$tmpdir/home"
mkdir -p "$home_dir"

PATH="/usr/bin:/bin" HOME="$home_dir" \
TUNNEL_INSTALL_BASE_URL="$base_url" \
TUNNEL_RELEASE_BASE_URL="$release_base_url" \
TUNNEL_RELEASE_SIGNING_PUBLIC_KEY="$release_signing_public_key" \
TUNNEL_INSTALL_DIR="$home_dir/.local/bin" \
"$script_dir/install-tunnel.sh" >"$tmpdir/install.out"

if [ ! -x "$home_dir/.local/bin/tunnel" ]; then
	printf 'error: installer did not create tunnel binary\n' >&2
	exit 1
fi

if [ "$("$home_dir/.local/bin/tunnel" --version)" != "tunnel $version" ]; then
	printf 'error: installed tunnel version mismatch\n' >&2
	exit 1
fi

if ! grep -q "add $home_dir/.local/bin to PATH" "$tmpdir/install.out"; then
	printf 'error: installer did not print PATH guidance\n' >&2
	exit 1
fi

mv "$fixture_root/latest.json" "$fixture_root/latest.json.hidden"
PATH="/usr/bin:/bin" HOME="$home_dir" \
VERSION="$version" \
TUNNEL_RELEASE_BASE_URL="$release_base_url" \
TUNNEL_RELEASE_SIGNING_PUBLIC_KEY="$release_signing_public_key" \
TUNNEL_INSTALL_DIR="$home_dir/.local/bin" \
"$script_dir/install-tunnel.sh" >/dev/null
mv "$fixture_root/latest.json.hidden" "$fixture_root/latest.json"

if [ "$("$home_dir/.local/bin/tunnel" --version)" != "tunnel $version" ]; then
	printf 'error: pinned install path did not preserve requested version\n' >&2
	exit 1
fi

printf '{"version":"%s"}\n' "$version" >"$fixture_root/latest.json"
if PATH="/usr/bin:/bin" HOME="$home_dir" \
	TUNNEL_INSTALL_BASE_URL="$base_url" \
	TUNNEL_RELEASE_BASE_URL="$release_base_url" \
	TUNNEL_RELEASE_SIGNING_PUBLIC_KEY="$release_signing_public_key" \
	TUNNEL_INSTALL_DIR="$home_dir/.local/bin" \
	"$script_dir/install-tunnel.sh" >/dev/null 2>"$tmpdir/missing-line.err"
then
	printf 'error: missing compatibility line install unexpectedly succeeded\n' >&2
	exit 1
fi

if ! grep -q 'latest.json did not contain compatibility_line' "$tmpdir/missing-line.err"; then
	printf 'error: missing compatibility line path did not explain failure\n' >&2
	exit 1
fi

printf '{"version":"%s","compatibility_line":"9"}\n' "$version" >"$fixture_root/latest.json"
if PATH="/usr/bin:/bin" HOME="$home_dir" \
	TUNNEL_INSTALL_BASE_URL="$base_url" \
	TUNNEL_RELEASE_BASE_URL="$release_base_url" \
	TUNNEL_RELEASE_SIGNING_PUBLIC_KEY="$release_signing_public_key" \
	TUNNEL_INSTALL_DIR="$home_dir/.local/bin" \
	"$script_dir/install-tunnel.sh" >/dev/null 2>"$tmpdir/manifest.err"
then
	printf 'error: mismatched compatibility line install unexpectedly succeeded\n' >&2
	exit 1
fi

if ! grep -q "latest.json compatibility_line does not match version $version" "$tmpdir/manifest.err"; then
	printf 'error: mismatched compatibility line path did not explain failure\n' >&2
	exit 1
fi

"$script_dir/render-latest-manifest.sh" "$version" >"$fixture_root/latest.json"

if PATH="/usr/bin:/bin" HOME="$home_dir" \
	TUNNEL_INSTALL_OS="linux" \
	TUNNEL_INSTALL_ARCH="s390x" \
	TUNNEL_INSTALL_BASE_URL="$base_url" \
	TUNNEL_RELEASE_BASE_URL="$release_base_url" \
	TUNNEL_RELEASE_SIGNING_PUBLIC_KEY="$release_signing_public_key" \
	TUNNEL_INSTALL_DIR="$home_dir/.local/bin" \
	"$script_dir/install-tunnel.sh" >/dev/null 2>"$tmpdir/unsupported.err"
then
	printf 'error: unsupported target install unexpectedly succeeded\n' >&2
	exit 1
fi

if ! grep -q 'unsupported target linux/s390x' "$tmpdir/unsupported.err"; then
	printf 'error: unsupported target path did not explain failure\n' >&2
	exit 1
fi

printf '#!/bin/sh\nprintf "tunnel old-version\\n"\n' >"$home_dir/.local/bin/tunnel"
chmod 0755 "$home_dir/.local/bin/tunnel"

current_os=$("$go_bin" env GOOS)
current_arch=$("$go_bin" env GOARCH)
current_asset=$(release_asset_name "$version" "$current_os" "$current_arch")
current_asset_path="$fixture_root/releases/download/$version/$current_asset"
checksums_file="$fixture_root/releases/download/$version/checksums.txt"
checksums_signature_file="$fixture_root/releases/download/$version/checksums.txt.sig"
cp "$current_asset_path" "$current_asset_path.good"
cp "$checksums_file" "$checksums_file.good"
cp "$checksums_signature_file" "$checksums_signature_file.good"

bad_members_dir="$tmpdir/bad-members"
mkdir -p "$bad_members_dir"
cp "$home_dir/.local/bin/tunnel" "$bad_members_dir/tunnel"
printf 'surprise\n' >"$bad_members_dir/bonus"
tar -C "$bad_members_dir" -czf "$current_asset_path" tunnel bonus
replace_checksum_entry "$checksums_file" "$current_asset" "$(release_hash_file "$current_asset_path")"
sign_checksums_file "$checksums_file" "$checksums_signature_file"

if PATH="/usr/bin:/bin" HOME="$home_dir" \
	TUNNEL_INSTALL_BASE_URL="$base_url" \
	TUNNEL_RELEASE_BASE_URL="$release_base_url" \
	TUNNEL_RELEASE_SIGNING_PUBLIC_KEY="$release_signing_public_key" \
	TUNNEL_INSTALL_DIR="$home_dir/.local/bin" \
	"$script_dir/install-tunnel.sh" >/dev/null 2>"$tmpdir/archive-members.err"
then
	printf 'error: archive with extra members unexpectedly installed\n' >&2
	exit 1
fi

if ! grep -q "archive $current_asset must contain only tunnel" "$tmpdir/archive-members.err"; then
	printf 'error: archive member validation path did not explain failure\n' >&2
	exit 1
fi

if [ "$("$home_dir/.local/bin/tunnel")" != "tunnel old-version" ]; then
	printf 'error: unsafe archive replaced existing tunnel binary\n' >&2
	exit 1
fi

cp "$current_asset_path.good" "$current_asset_path"
cp "$checksums_file.good" "$checksums_file"
cp "$checksums_signature_file.good" "$checksums_signature_file"

bad_link_dir="$tmpdir/bad-link"
mkdir -p "$bad_link_dir"
ln -sf /etc/passwd "$bad_link_dir/tunnel"
tar -C "$bad_link_dir" -czf "$current_asset_path" tunnel
replace_checksum_entry "$checksums_file" "$current_asset" "$(release_hash_file "$current_asset_path")"
sign_checksums_file "$checksums_file" "$checksums_signature_file"

if PATH="/usr/bin:/bin" HOME="$home_dir" \
	TUNNEL_INSTALL_BASE_URL="$base_url" \
	TUNNEL_RELEASE_BASE_URL="$release_base_url" \
	TUNNEL_RELEASE_SIGNING_PUBLIC_KEY="$release_signing_public_key" \
	TUNNEL_INSTALL_DIR="$home_dir/.local/bin" \
	"$script_dir/install-tunnel.sh" >/dev/null 2>"$tmpdir/archive-link.err"
then
	printf 'error: symlink archive unexpectedly installed\n' >&2
	exit 1
fi

if ! grep -q "archive $current_asset did not contain a safe tunnel binary" "$tmpdir/archive-link.err"; then
	printf 'error: symlink archive path did not explain failure\n' >&2
	exit 1
fi

if [ "$("$home_dir/.local/bin/tunnel")" != "tunnel old-version" ]; then
	printf 'error: symlink archive replaced existing tunnel binary\n' >&2
	exit 1
fi

cp "$current_asset_path.good" "$current_asset_path"
cp "$checksums_file.good" "$checksums_file"
cp "$checksums_signature_file.good" "$checksums_signature_file"
replace_checksum_entry "$checksums_file" "$current_asset" "badbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadb"
sign_checksums_file "$checksums_file" "$checksums_signature_file"

if PATH="/usr/bin:/bin" HOME="$home_dir" \
	TUNNEL_INSTALL_BASE_URL="$base_url" \
	TUNNEL_RELEASE_BASE_URL="$release_base_url" \
	TUNNEL_RELEASE_SIGNING_PUBLIC_KEY="$release_signing_public_key" \
	TUNNEL_INSTALL_DIR="$home_dir/.local/bin" \
	"$script_dir/install-tunnel.sh" >/dev/null 2>"$tmpdir/checksum.err"
then
	printf 'error: checksum mismatch install unexpectedly succeeded\n' >&2
	exit 1
fi

if ! grep -q "checksum mismatch for $current_asset" "$tmpdir/checksum.err"; then
	printf 'error: checksum mismatch path did not explain failure\n' >&2
	exit 1
fi

if [ "$("$home_dir/.local/bin/tunnel")" != "tunnel old-version" ]; then
	printf 'error: failed install replaced existing tunnel binary\n' >&2
	exit 1
fi

cp "$checksums_file.good" "$checksums_file"
printf 'corrupt signature\n' >"$checksums_signature_file"

if PATH="/usr/bin:/bin" HOME="$home_dir" \
	TUNNEL_INSTALL_BASE_URL="$base_url" \
	TUNNEL_RELEASE_BASE_URL="$release_base_url" \
	TUNNEL_RELEASE_SIGNING_PUBLIC_KEY="$release_signing_public_key" \
	TUNNEL_INSTALL_DIR="$home_dir/.local/bin" \
	"$script_dir/install-tunnel.sh" >/dev/null 2>"$tmpdir/signature.err"
then
	printf 'error: invalid signature install unexpectedly succeeded\n' >&2
	exit 1
fi

if ! grep -q 'invalid signature for checksums.txt' "$tmpdir/signature.err"; then
	printf 'error: invalid signature path did not explain failure\n' >&2
	exit 1
fi

if [ "$("$home_dir/.local/bin/tunnel")" != "tunnel old-version" ]; then
	printf 'error: invalid signature install replaced existing tunnel binary\n' >&2
	exit 1
fi

ssh_keygen_wrapper_dir="$tmpdir/no-ssh-y/bin"
mkdir -p "$ssh_keygen_wrapper_dir"
cat >"$ssh_keygen_wrapper_dir/ssh-keygen" <<'EOF'
#!/bin/sh
if [ "${1:-}" = "-Y" ]; then
	printf 'unknown option -- Y\n' >&2
	exit 1
fi
exec /usr/bin/ssh-keygen "$@"
EOF
chmod 0755 "$ssh_keygen_wrapper_dir/ssh-keygen"

if PATH="$ssh_keygen_wrapper_dir:/usr/bin:/bin" HOME="$home_dir" \
	TUNNEL_INSTALL_BASE_URL="$base_url" \
	TUNNEL_RELEASE_BASE_URL="$release_base_url" \
	TUNNEL_RELEASE_SIGNING_PUBLIC_KEY="$release_signing_public_key" \
	TUNNEL_INSTALL_DIR="$home_dir/.local/bin" \
	"$script_dir/install-tunnel.sh" >/dev/null 2>"$tmpdir/ssh-keygen-cap.err"
then
	printf 'error: installer unexpectedly accepted ssh-keygen without -Y support\n' >&2
	exit 1
fi

if ! grep -q 'ssh-keygen with -Y verify support is required' "$tmpdir/ssh-keygen-cap.err"; then
	printf 'error: ssh-keygen capability path did not explain failure\n' >&2
	exit 1
fi

printf 'installer smoke tests passed\n'
