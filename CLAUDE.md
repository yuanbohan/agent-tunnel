# CLAUDE.md

This file provides guidance to Coding Agent(Claude Code, Codex, Gemini) when working with code in this repository.

During the brainstorming, spec phases, avoid writing code whenever possible; implementation should happen in the subsequent implementation phase.

## Start Here

- The main product is `agentunnel` with a mandatory relay connection.
- `cmd/agentunnel` launches `claude`, `codex`, or `gemini`, keeps the local terminal interactive, and registers the PTY session with a remote relay server.
- `cmd/relay` is the standalone relay server. It serves the dashboard/session UI, authenticates browsers with Basic Auth, authenticates agents with a bearer token, and maintains a live in-memory session registry plus rolling live-session history and shared unread state. Supports `--port` flag to override listen address.
- `session/` owns PTY lifecycle, Hub fanout, local terminal attach, and resize/input forwarding.
- `protocol/` defines wire types: `Message` (input/output/resize, with `seq` on relay-to-browser output), `SessionInfo`, and `AgentFrame`.
- `connector/` is the mandatory outbound connector from a local `agentunnel` process to `/agent/ws` on the relay.
- `relay/` owns relay auth, registry, rolling history retention, unread tracking, preview extraction, heartbeat cleanup, and relay HTTP/WebSocket handlers.
- `launcher/` is the supported-launcher registry and PATH resolution layer.
- `webui/` holds the embedded web assets built from `web/dist/`.
- `web/` contains a single browser entrypoint: `index.html` / `src/app.ts` for the relay dashboard and session detail view.
- `web/src/terminal.ts` is the shared xterm.js wrapper. `web/src/input_filter.ts` filters auto-response sequences in the session page before forwarding input. `web/src/session_runtime.ts` adapts xterm markers/decorations for history paging and unread jumps. `web/src/dashboard_view.ts` owns dashboard polling and preview remounts.
- Rebuild tracked embedded assets in `webui/dist/` with `make web-build` or `cd web && npm run build` after web changes.
- `docs/architecture.md` describes how all Go packages and web modules interact.

## Current Product Boundaries

- `agentunnel` is the PTY owner. It has no localhost HTTP server; browser access is exclusively through the relay.
- The relay connection is mandatory. `agentunnel` requires `--relay-addr` (or `AGENTUNNEL_RELAY_ADDR`) and `AGENTUNNEL_RELAY_TOKEN` to start.
- The relay is still live-only and monitoring-focused. It lists live sessions, retains a rolling in-memory history buffer per live session, tracks a shared read marker, and lets browsers attach to one; it does not create or stop local sessions.
- Relay state is live-only and in-memory. If the owning agent socket disappears, the session disappears from the list along with its retained history and unread state.
- Browser session pages start in `Read-only` mode and only send input after the state chip is toggled to `Input on`.
- Browser session pages replay recent retained history before live attach, lazy-load older history on upward scroll, and expose `Jump to N unread` when the relay reports unread output.
- Browser WebSocket attach on the relay is same-origin checked. Do not relax this casually.

## Docs Expectations

- Keep `README.md`, `docs/architecture.md`, and `CLAUDE.md` aligned with the current implementation when behavior or scope changes.
- If you change relay auth, relay lifecycle, browser entrypoints, or PTY/input behavior, update `docs/architecture.md`.
- If you change relay history retention, unread semantics, or browser session replay behavior, update `docs/protocol.md` and `docs/architecture.md`.
- If you change operator-facing startup flow or environment variables, update `README.md`.

## Verification

- `go test ./...`
- `cd web && npm test`
- `cd web && npm run build`
- `make test`
- `make build`
