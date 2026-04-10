# CLAUDE.md

This file provides guidance to coding agents when working in this repository.

During brainstorming and spec phases, avoid writing code whenever possible; implementation should happen in the subsequent implementation phase.

## Start Here

- The main product is the `tunnel` CLI with relay-first startup semantics and background reconnect after local session start.
- `cmd/agentunnel` builds the `tunnel` CLI. It launches `claude`, `codex`, or `gemini`, keeps the local terminal interactive, and maintains the authoritative headless terminal mirror for the current PTY session.
- `cmd/relay` is the standalone relay server. It exposes authenticated HTTP and WebSocket APIs for external clients, authenticates clients with Basic Auth, authenticates agents with a bearer token, and maintains a live in-memory session registry with reconnect grace state and session-scoped attach routing. It does not retain transcript history. Supports `--port` flag to override listen address.
- `session/` owns PTY lifecycle, Hub fanout, local terminal attach, resize/input forwarding, and the terminal mirror used for attach snapshots.
- `protocol/` defines attach-oriented wire types: agent registration, attach control, session info, structured input, and client-routed terminal-byte packets.
- `connector/` is the mandatory outbound connector from a local `tunnel` process to `/agent/ws` on the relay. It registers sessions, publishes activity and resize metadata, answers attach-open/attach-close control, and routes client-scoped terminal bytes.
- `relay/` owns relay auth, registry, reconnect grace lifecycle, session-scoped attach websockets, and agent/client routing handlers.
- `launcher/` is the supported-launcher registry and PATH resolution layer.
- `docs/architecture.md` describes how all Go packages and relay-facing protocols interact.

## Current Product Boundaries

- `session_id` identifies one running `tunnel` process. Relay reconnects for that same process keep the same `session_id`. A fresh agent launch gets a new `session_id`.
- `tunnel` is the PTY owner. It has no localhost HTTP server; all remote client access goes through the relay.
- `tunnel` requires `--relay-addr` (or `AGENTUNNEL_RELAY_ADDR`) and `AGENTUNNEL_RELAY_TOKEN` to start.
- On launch, `tunnel` gives relay registration a bounded first chance to succeed. If that startup window expires, the local terminal session still starts and the relay reconnect loop continues in the background.
- On macOS, after local session startup succeeds, `tunnel` attempts default-on idle sleep prevention for the lifetime of the `tunnel` process. If that helper cannot be started, startup still continues and the startup banner must say so explicitly.
- After the local terminal session has begun, relay unavailability must not interrupt local terminal work; it only affects remote visibility and interaction until reconnect succeeds.
- The agent is the authority for current terminal state. It maintains the headless terminal mirror and produces attach snapshots from that mirror.
- Remote viewing is session-scoped: clients discover sessions with `GET /api/sessions` and attach with `GET /api/sessions/:id/attach/ws`.
- Browser attach clients must be same-origin with the relay host; native clients that omit `Origin` remain supported.
- Remote recovery in this revision is current-screen recovery only. There is no transcript replay API and no global live-output websocket contract.
- The relay stores live session metadata, owner connection state, and active attach routing state. It must not be described as retaining transcript history or terminal state.
- The local terminal remains the most complete source of truth for session output in the current product revision.
- A successful attach yields `attached`, snapshot bytes, `snapshot_done`, then live PTY bytes on the same websocket.
- `reconnecting` sessions remain discoverable briefly, but attaches and remote input are unavailable until the owning agent reconnects.
- Relay state is live-only and in-memory. If the owning agent socket disappears and does not reconnect before the grace window expires, the relay removes the session.
- Protocol-facing timestamps such as `started_at` and `last_active_at` are Unix timestamps encoded as JSON integer seconds.
- `last_active_at` is agent-authored best-effort metadata for discovery, not a delivery guarantee.
- The relay is content-opaque. It may forward output bytes and attach control, but it must not emulate the terminal or derive previews or other message semantics from terminal content.
- PTY size remains local-terminal-owned in this phase. Remote clients follow forwarded resize events and do not become size authority.
- Structured remote input remains `input_text` and `input_key`, with PTY-byte translation owned by `tunnel`.
- The relay does not ship a bundled frontend. Any UI or client experience is owned by external clients such as the mobile app.
- Stronger delivery guarantees may be explored later, but do not document or imply them before they exist in code and protocol.

## Docs Expectations

- Keep `README.md`, `docs/protocol.md`, `docs/architecture.md`, `CLAUDE.md`, and `AGENTS.md` aligned with the active attach-based contract and current implementation status when behavior or scope changes.
- If you change relay auth, relay lifecycle, client-facing endpoints, or PTY/input behavior, update `docs/architecture.md`.
- If you change attach lifecycle semantics, session-state semantics, `/api/sessions/:id/attach/ws`, or `/agent/ws` attach-control messages, update `README.md`, `docs/protocol.md`, `docs/architecture.md`, `CLAUDE.md`, and `AGENTS.md`.
- If you change snapshot generation, live-byte delivery, resize ownership, or structured input semantics, update `README.md`, `docs/protocol.md`, `docs/architecture.md`, `CLAUDE.md`, and `AGENTS.md`.
- If you change operator-facing startup flow or environment variables, update `README.md`.

## Verification

- `go test ./...`
- `go test ./protocol ./relay`
- `make test`
- `make test-relay`
- `make build`
