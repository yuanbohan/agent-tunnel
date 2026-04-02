# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Start Here

- The main product is `agentunnel`, not the legacy `agent`/`client` pair.
- `cmd/agentunnel` launches `claude`, `codex`, or `gemini`, keeps the local terminal interactive, and serves the same PTY session to a localhost browser client.
- `internal/session/` owns PTY lifecycle, fanout, local terminal attach, and resize/input forwarding.
- `internal/server/` serves the embedded web UI and the WebSocket bridge.
- `internal/launcher/` is the supported-launcher registry and PATH resolution layer.
- `web/` contains the browser client source. Rebuild tracked embedded assets in `internal/webui/dist/` with `make web-build` after web changes.
- `cmd/agent` and `cmd/client` are legacy shell-over-WebSocket mode. Do not break or remove them unless the task explicitly says so.

## Verification

- `make test`
- `make build`
