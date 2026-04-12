#!/bin/sh

set -eu

script_dir=$(CDPATH= cd -- "$(dirname "$0")" && pwd)

# shellcheck source=/dev/null
. "$script_dir/release-common.sh"

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

version="v0.1.2"
fixture_root="$tmpdir/fixture"
release_root="$fixture_root/releases/download"
mkdir -p "$release_root"
go_bin="${GO:-go}"

GO="$go_bin" RELEASE_DIR="$release_root" "$script_dir/release-package.sh" "$version" >/dev/null
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
checksums_file="$fixture_root/releases/download/$version/checksums.txt"
mv "$checksums_file" "$checksums_file.good"
awk -v asset="$current_asset" '
	$2 == asset {
		print "badbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadbadb  " asset
		next
	}
	{ print }
' "$checksums_file.good" >"$checksums_file"

if PATH="/usr/bin:/bin" HOME="$home_dir" \
	TUNNEL_INSTALL_BASE_URL="$base_url" \
	TUNNEL_RELEASE_BASE_URL="$release_base_url" \
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

printf 'installer smoke tests passed\n'
