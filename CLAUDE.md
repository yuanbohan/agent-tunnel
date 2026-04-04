# CLAUDE.md

This file provides guidance to Coding Agent(Claude Code, Codex, Gemini) when working with code in this repository.

During the brainstorming, spec phases, avoid writing code whenever possible; implementation should happen in the subsequent implementation phase.

## Start Here

- The main product is `agentunnel` with a mandatory relay connection.
- `cmd/agentunnel` launches `claude`, `codex`, or `gemini`, keeps the local terminal interactive, and registers the PTY session with a remote relay server.
- `cmd/relay` is the standalone relay server. It exposes authenticated HTTP and WebSocket APIs for external clients, authenticates clients with Basic Auth, authenticates agents with a bearer token, and maintains a live in-memory session registry plus rolling live-session history and shared unread state. Supports `--port` flag to override listen address.
- `session/` owns PTY lifecycle, Hub fanout, local terminal attach, and resize/input forwarding.
- `protocol/` defines wire types: `Message` (input/output/resize, with `seq` on relay-to-client output), `SessionInfo`, and `AgentFrame`.
- `connector/` is the mandatory outbound connector from a local `agentunnel` process to `/agent/ws` on the relay.
- `relay/` owns relay auth, registry, rolling history retention, unread tracking, preview extraction, heartbeat cleanup, and relay HTTP/WebSocket handlers.
- `launcher/` is the supported-launcher registry and PATH resolution layer.
- `docs/architecture.md` describes how all Go packages and relay-facing protocols interact.
- `docs/solutions/` holds documented solutions and best practices, organized by category with searchable frontmatter like `module`, `tags`, and `problem_type`. Useful when implementing or debugging in areas that already have prior learnings.

## Current Product Boundaries

- `agentunnel` is the PTY owner. It has no localhost HTTP server; all remote client access goes through the relay.
- The relay connection is mandatory. `agentunnel` requires `--relay-addr` (or `AGENTUNNEL_RELAY_ADDR`) and `AGENTUNNEL_RELAY_TOKEN` to start.
- The relay is still live-only and monitoring-focused. It lists live sessions, retains a rolling in-memory history buffer per live session, tracks a shared read marker, and lets authenticated clients attach to one; it does not create or stop local sessions.
- Relay state is live-only and in-memory. If the owning agent socket disappears, the session disappears from the list along with its retained history and unread state.
- The relay does not ship a bundled frontend. Any UI or client experience is owned by external clients such as the mobile app.
- Client WebSocket attach on the relay is same-origin checked when `Origin` is present. Do not relax this casually.

## Docs Expectations

- Keep `README.md`, `docs/architecture.md`, and `CLAUDE.md` aligned with the current implementation when behavior or scope changes.
- If you change relay auth, relay lifecycle, client-facing endpoints, or PTY/input behavior, update `docs/architecture.md`.
- If you change relay history retention, unread semantics, or client replay behavior, update `docs/protocol.md` and `docs/architecture.md`.
- If you change operator-facing startup flow or environment variables, update `README.md`.

## Verification

- `go test ./...`
- `make test`
- `make build`
