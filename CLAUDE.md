# CLAUDE.md

This file provides guidance to coding agents when working in this repository.

During brainstorming and spec phases, avoid writing code whenever possible; implementation should happen in the subsequent implementation phase.

## Start Here

- The main product is `agentunnel` with relay-first startup semantics and background reconnect after local session start.
- `cmd/agentunnel` launches `claude`, `codex`, or `gemini`, keeps the local terminal interactive, and registers the PTY session with a remote relay server.
- `cmd/relay` is the standalone relay server. It exposes authenticated HTTP and WebSocket APIs for external clients, authenticates clients with Basic Auth, authenticates agents with a bearer token, and maintains a live in-memory session registry plus rolling retained output frames. Supports `--port` flag to override listen address.
- `session/` owns PTY lifecycle, Hub fanout, local terminal attach, and resize/input forwarding.
- `protocol/` defines wire types: agent-side `Message`, client-facing `ClientUpdateMessage`, `SessionInfo`, and `AgentFrame`.
- `connector/` is the mandatory outbound connector from a local `agentunnel` process to `/agent/ws` on the relay.
- `relay/` owns relay auth, registry, retained output frame buffering, and relay HTTP/WebSocket handlers.
- `launcher/` is the supported-launcher registry and PATH resolution layer.
- `docs/architecture.md` describes how all Go packages and relay-facing protocols interact.

## Current Product Boundaries

- `agentunnel` is the PTY owner. It has no localhost HTTP server; all remote client access goes through the relay.
- `agentunnel` requires `--relay-addr` (or `AGENTUNNEL_RELAY_ADDR`) and `AGENTUNNEL_RELAY_TOKEN` to start.
- On launch, `agentunnel` gives relay registration a bounded first chance to succeed. If that startup window expires, the local terminal session still starts and the relay reconnect loop continues in the background.
- After the local terminal session has begun, relay unavailability must not interrupt local terminal work; it only affects remote visibility and interaction until reconnect succeeds.
- The relay is live-only and monitoring-focused. It lists live sessions, retains a rolling in-memory output frame buffer per live session, and forwards multiplexed live updates on one global client socket; it does not create or stop local sessions.
- The preferred client foreground receive channel is `GET /api/updates/ws`, and it is a best-effort live channel rather than a guaranteed lossless transcript.
- The local terminal remains the most complete source of truth for session output in the current product revision.
- The relay is content-opaque. It may forward or retain output bytes for replay, but it must not derive previews or other message semantics from terminal content.
- Relay `seq` values describe relay-recorded output ordering, not end-to-end proof that every PTY byte reached a remote client.
- `GET /api/sessions/:id/frames` is the standard relay-side recovery path for recently retained output after reconnect, but it is still bounded by live-only in-memory retention.
- Relay state is live-only and in-memory. If the owning agent socket disappears, the session disappears from the list along with its retained output frames.
- The relay does not ship a bundled frontend. Any UI or client experience is owned by external clients such as the mobile app.
- Client WebSocket attach on the relay is same-origin checked when `Origin` is present. Do not relax this casually.
- Stronger delivery guarantees may be explored later, but do not document or imply them before they exist in code and protocol.

## Docs Expectations

- Keep `README.md`, `docs/architecture.md`, `CLAUDE.md`, and `AGENTS.md` aligned with the current implementation when behavior or scope changes.
- If you change relay auth, relay lifecycle, client-facing endpoints, or PTY/input behavior, update `docs/architecture.md`.
- If you change retained output frame semantics, output sequence semantics, client replay behavior, or the global live-update stream, update `docs/protocol.md` and `docs/architecture.md`.
- If you change connector buffering or any behavior that affects the best-effort remote-output contract, update `README.md`, `docs/protocol.md`, `docs/architecture.md`, and `CLAUDE.md`.
- If you change operator-facing startup flow or environment variables, update `README.md`.

## Verification

- `go test ./...`
- `go test ./protocol ./relay`
- `make test`
- `make test-relay`
- `make build`
