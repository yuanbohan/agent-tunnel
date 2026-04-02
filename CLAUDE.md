# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Vision

The end goal is remote agent control from mobile devices over the internet. See `docs/vision.md` for the full picture. The current codebase (v1) is the localhost foundation; the next milestone (v2) adds a VPS relay server, authentication, and session routing.

## Start Here

- The main product is `agentunnel`, not the legacy `agent`/`client` pair.
- `cmd/agentunnel` launches `claude`, `codex`, or `gemini`, keeps the local terminal interactive, and serves the same PTY session to a localhost browser client.
- `internal/session/` owns PTY lifecycle, fanout, local terminal attach, and resize/input forwarding.
- `internal/server/` serves the embedded web UI and the WebSocket bridge.
- `internal/launcher/` is the supported-launcher registry and PATH resolution layer.
- `web/` contains the browser client source. Rebuild tracked embedded assets in `internal/webui/dist/` with `make web-build` after web changes.
- `docs/architecture.md` describes how all Go packages and web modules interact.
- `docs/vision.md` describes the end goal and the evolution path from v1 to v2.
- `cmd/agent` and `cmd/client` are legacy shell-over-WebSocket mode. Do not break or remove them unless the task explicitly says so.

## Verification

- `make test`
- `make build`
