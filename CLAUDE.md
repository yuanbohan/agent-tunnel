# CLAUDE.md

This file provides guidance to Coding Agent(Claude Code, Codex, Gemini) when working with code in this repository.

During the brainstorming, spec, and planning phases, avoid writing code whenever possible; implementation should happen in the subsequent implementation phase.

## Start Here

- The main product is `agentunnel`, not the legacy `agent`/`client` pair.
- `cmd/agentunnel` launches `claude`, `codex`, or `gemini`, keeps the local terminal interactive, always serves the same PTY session to a localhost browser client, and can optionally register that session with a remote relay.
- `cmd/relay` is the standalone relay server. It serves the relay dashboard/session UI, authenticates browsers with Basic Auth, authenticates local agents with a bearer token, and maintains a live in-memory session registry.
- `internal/session/` owns PTY lifecycle, fanout, local terminal attach, and resize/input forwarding.
- `internal/server/` serves the embedded web UI and the WebSocket bridge.
- `internal/relayapi/` defines shared relay payloads such as `SessionInfo` and `AgentFrame`.
- `internal/relayclient/` is the outbound connector from a local `agentunnel` process to `/agent/ws` on the relay.
- `internal/relayserver/` owns relay auth, registry, preview extraction, heartbeat cleanup, and relay HTTP/WebSocket handlers.
- `internal/launcher/` is the supported-launcher registry and PATH resolution layer.
- `web/` contains two browser entrypoints:
  - `index.html` / `src/main.ts` for localhost single-session mode
  - `relay.html` / `src/relay_app.ts` for relay dashboard + mobile session detail mode
- `web/src/terminal.ts` is shared by both UIs. Localhost mode also uses `web/src/input_filter.ts` to avoid forwarding xterm auto-response sequences back into the PTY.
- Rebuild tracked embedded assets in `internal/webui/dist/` with `make web-build` or `cd web && npm run build` after web changes.
- `docs/architecture.md` describes how all Go packages and web modules interact.
- `cmd/agent` and `cmd/client` are legacy shell-over-WebSocket mode. Do not break or remove them unless the task explicitly says so.

## Current Product Boundaries

- `agentunnel` remains the PTY owner in all modes. Relay mode is additive; it does not replace localhost mode.
- The relay is `attach-only`. It lists live sessions and lets a browser attach to one; it does not create or stop local sessions.
- Relay state is live-only and in-memory. If the owning agent socket disappears, the session disappears from the list.
- Browser relay session pages start in `Read-only` mode and only send input after the state chip is toggled to `Input on`.
- Browser WebSocket attach on the relay is same-origin checked. Do not relax this casually.

## Docs Expectations

- Keep `README.md`, `docs/architecture.md`, and `CLAUDE.md` aligned with the current implementation when behavior or scope changes.
- If you change relay auth, relay lifecycle, browser entrypoints, or PTY/input behavior, update `docs/architecture.md`.
- If you change operator-facing startup flow or environment variables, update `README.md`.
- Avoid leaving docs in a “pre-relay” state; this repo now has both localhost and relay paths.

## Verification

- `go test ./...`
- `cd web && npm test`
- `cd web && npm run build`
- `make test`
- `make build`
