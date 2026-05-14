#!/usr/bin/env bash
set -euo pipefail

target="${1:-stun.agentunnel.cn:3478}"
timeout="${STUN_CHECK_TIMEOUT:-1s}"
retries="${STUN_CHECK_RETRIES:-3}"

go run ./cmd/stuncheck --timeout "$timeout" --retries "$retries" "$target"
