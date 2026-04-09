# CLAUDE.md

This file provides guidance to coding agents when working in this repository.

During brainstorming and spec phases, avoid writing code whenever possible; implementation should happen in the subsequent implementation phase.

## Start Here

- The main product is `agentunnel` with relay-first startup semantics and background reconnect after local session start.
- `cmd/agentunnel` launches `claude`, `codex`, or `gemini`, keeps the local terminal interactive, and maintains the in-memory replay history for the current PTY session.
- `cmd/relay` is the standalone relay server. It exposes authenticated HTTP and WebSocket APIs for external clients, authenticates clients with Basic Auth, authenticates agents with a bearer token, and maintains a live in-memory session registry with reconnect grace state and pending history-request bookkeeping. It does not retain the frame array for replay. Supports `--port` flag to override listen address.
- `session/` owns PTY lifecycle, Hub fanout, local terminal attach, resize/input forwarding, and the bounded history buffer used for replay.
- `protocol/` defines wire types: agent-side `Message`, replay `ReplayFrame`, client-facing `ClientUpdateMessage`, `SessionInfo`, and `AgentFrame`.
- `connector/` is the mandatory outbound connector from a local `agentunnel` process to `/agent/ws` on the relay. It authors `seq`, `ts`, and `latest_seq`, uploads live output, and answers proxied `history_request` messages from the relay.
- `relay/` owns relay auth, registry, reconnect grace lifecycle, `/frames` proxying, pending replay waiters, and relay HTTP/WebSocket handlers.
- `launcher/` is the supported-launcher registry and PATH resolution layer.
- `docs/architecture.md` describes how all Go packages and relay-facing protocols interact.

## Current Product Boundaries

- `session_id` identifies one running `agentunnel` process. Relay reconnects for that same process keep the same `session_id`. A fresh agent launch gets a new `session_id`.
- `agentunnel` is the PTY owner. It has no localhost HTTP server; all remote client access goes through the relay.
- `agentunnel` requires `--relay-addr` (or `AGENTUNNEL_RELAY_ADDR`) and `AGENTUNNEL_RELAY_TOKEN` to start.
- On launch, `agentunnel` gives relay registration a bounded first chance to succeed. If that startup window expires, the local terminal session still starts and the relay reconnect loop continues in the background.
- On macOS, after local session startup succeeds, `agentunnel` attempts default-on idle sleep prevention for the lifetime of the `agentunnel` process. If that helper cannot be started, startup still continues and the startup banner must say so explicitly.
- After the local terminal session has begun, relay unavailability must not interrupt local terminal work; it only affects remote visibility and interaction until reconnect succeeds.
- The agent is the authority for current-session output history. Replay lives in the agent-side bounded in-memory history buffer, not in the relay.
- The replay history is an output transcript, not an exact input log. Typed characters only appear in replay when the terminal application echoes them.
- The relay is live-only and monitoring-focused. It lists live sessions, exposes `connected` and `reconnecting` state, proxies `GET /api/sessions/:id/frames` to the connected owning agent, and forwards multiplexed live updates on one global client socket; it does not create or stop local sessions.
- The relay stores session metadata, owner connection state, and pending history-request bookkeeping. It must not be described as retaining or owning session frames.
- The preferred client foreground receive channel is `GET /api/updates/ws`, and it is a best-effort live channel rather than a guaranteed lossless transcript.
- The local terminal remains the most complete source of truth for session output in the current product revision.
- `GET /api/sessions/:id/frames` is the standard recovery path for agent-owned output while the session is `connected`.
- `reconnecting` sessions remain discoverable briefly, but `/frames`, live output, and remote input are unavailable until the owning agent reconnects.
- Relay state is live-only and in-memory. If the owning agent socket disappears and does not reconnect before the grace window expires, the relay removes the session.
- `seq`, `ts`, `last_active_at`, and `latest_seq` are agent-authored metadata, not relay-authored metadata.
- The relay is content-opaque. It may forward output bytes and proxy history reads, but it must not derive previews or other message semantics from terminal content.
- The relay does not ship a bundled frontend. Any UI or client experience is owned by external clients such as the mobile app.
- Stronger delivery guarantees may be explored later, but do not document or imply them before they exist in code and protocol.

## Docs Expectations

- Keep `README.md`, `docs/protocol.md`, `docs/architecture.md`, `CLAUDE.md`, and `AGENTS.md` aligned with the current implementation when behavior or scope changes.
- If you change relay auth, relay lifecycle, client-facing endpoints, or PTY/input behavior, update `docs/architecture.md`.
- If you change agent-owned replay semantics, `history_request` / `history_response`, output sequence semantics, session-state semantics, or the `/frames` proxy contract, update `README.md`, `docs/protocol.md`, `docs/architecture.md`, `CLAUDE.md`, and `AGENTS.md`.
- If you change connector buffering or any behavior that affects the best-effort remote-output contract, update `README.md`, `docs/protocol.md`, `docs/architecture.md`, `CLAUDE.md`, and `AGENTS.md`.
- If you change operator-facing startup flow or environment variables, update `README.md`.

## Verification

- `go test ./...`
- `go test ./protocol ./relay`
- `make test`
- `make test-relay`
- `make build`
