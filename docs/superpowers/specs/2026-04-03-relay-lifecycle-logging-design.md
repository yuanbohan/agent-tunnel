# Relay Lifecycle Logging Design

## Summary

Add low-noise operator-facing logs to the relay server so initial deployments can answer a small set of operational questions:

- Did the relay start correctly?
- Did an agent connect and successfully register a session?
- Did a browser or mobile client attach to a session?
- When a connection closed, what closed and why?
- Did authentication, WebSocket upgrade, or sink backpressure fail?

This design intentionally avoids frame-level data logs, payload logging, and throughput reporting. The goal is deployment-time observability for connection lifecycle events, not deep transport tracing.

## Goals

- Emit concise structured logs for relay lifecycle events.
- Keep default log volume proportional to connection churn, not to frame volume.
- Make `session_id` the primary correlation key for session-scoped events.
- Capture enough metadata to debug common deployment issues without exposing terminal content.
- Keep the implementation thin enough that the relay can start with basic structured logs now and add richer logging later without breaking the event model.

## Non-Goals

- Logging every `input`, `output`, or `resize` frame.
- Logging terminal payload content or sampled payload snippets.
- Calculating or logging throughput summaries.
- Building a metrics system.
- Distinguishing web, iOS, and Android clients in the event model beyond `user_agent`.

## Approaches Considered

### 1. Full frame logging

Log connect, disconnect, and every message flowing through the relay.

Pros:

- Maximum visibility during development.

Cons:

- Log volume scales with PTY output and quickly becomes unusable.
- Buries lifecycle failures under noisy data logs.
- Risks leaking terminal output into operator logs.

Rejected because it directly conflicts with the low-noise deployment goal.

### 2. Lifecycle-only logs

Log only startup, connect, register, disconnect, and a few hard failures.

Pros:

- Very low noise.
- Easy to scan manually.

Cons:

- May miss a few important transport-health signals, especially slow client sinks and failed upgrade attempts.

Good baseline, but slightly too sparse.

### 3. Lifecycle logs plus sparse anomaly logs

Log normal connection lifecycle events and a small set of actionable anomalies. Do not log frame traffic.

Pros:

- Low noise.
- Covers the first deployment debugging cases.
- Preserves room for future debug-mode expansion.

Cons:

- Does not answer traffic-volume questions.

Recommended because it matches the current operator need.

## Recommended Design

The relay should emit structured JSON logs for a fixed lifecycle event catalog. The default log stream should contain:

- startup events
- authentication failures
- WebSocket upgrade failures
- agent connect, register, disconnect
- client connect, disconnect
- session replacement by a new agent owner
- sink backpressure that forces a client disconnect

The relay should not emit logs when ordinary `input`, `output`, or `resize` frames are forwarded successfully.

## Event Catalog

### `relay_started`

Level: `INFO`

Emitted once after configuration is loaded and before `ListenAndServe`.

Fields:

- `listen_addr`

### `auth_failed`

Level: `WARN`

Emitted when HTTP or WebSocket access is rejected before upgrade due to invalid credentials.

Fields:

- `path`
- `remote_addr`
- `auth_type`

Field notes:

- `auth_type` is `basic` for browser-facing routes and `bearer` for `/agent/ws`.

### `ws_upgrade_failed`

Level: `WARN`

Emitted when a WebSocket upgrade attempt fails after authentication passes.

Fields:

- `path`
- `remote_addr`
- `role`

Field notes:

- `role` is `agent` or `client`.

### `agent_ws_connected`

Level: `INFO`

Emitted after `/agent/ws` successfully upgrades to a WebSocket and before the register frame is processed.

Fields:

- `remote_addr`

### `agent_registered`

Level: `INFO`

Emitted after the relay accepts a valid register frame and places the session into the registry.

Fields:

- `session_id`
- `launcher`
- `label`
- `cwd`

### `session_replaced`

Level: `WARN`

Emitted when an agent registers a session ID that already exists and the relay replaces the old session owner with the new peer.

Fields:

- `session_id`

### `agent_disconnected`

Level: `INFO`

Emitted when the owning agent connection closes and the live session is removed.

Fields:

- `session_id`
- `duration_ms`
- `reason`

Field notes:

- `duration_ms` is measured from successful registration to disconnect cleanup.
- `reason` should be a short machine-friendly string, not a long error dump.

### `client_ws_connected`

Level: `INFO`

Emitted when a browser or mobile client attaches to `/api/sessions/:id/ws`.

Fields:

- `session_id`
- `client_id`
- `remote_addr`
- `user_agent`

Field notes:

- `client_id` should reuse the sink identifier already generated for browser clients.
- `user_agent` may be empty.

### `client_disconnected`

Level: `INFO`

Emitted when a browser or mobile client disconnects or is detached.

Fields:

- `session_id`
- `client_id`
- `duration_ms`
- `reason`

### `sink_backpressure`

Level: `WARN`

Emitted when a client sink cannot keep up, its outbound queue fills, and the relay closes that sink.

Fields:

- `session_id`
- `client_id`

Field notes:

- A subsequent `client_disconnected` event should still be emitted with `reason=backpressure` so the lifecycle remains complete.

## Common Field Conventions

All log records should include:

- `ts`
- `level`
- `event`

When applicable, records should also include:

- `session_id`
- `client_id`
- `remote_addr`
- `path`
- `role`
- `user_agent`
- `duration_ms`
- `reason`

Conventions:

- Use snake_case field names consistently.
- Omit fields that do not apply to a given event instead of emitting empty placeholders.
- Treat `session_id` as mandatory for any event after registration.
- Keep `reason` short and enumerable.

## Allowed Disconnect Reasons

Initial reasons should come from a small controlled vocabulary:

- `client_closed`
- `read_error`
- `write_error`
- `bad_register_frame`
- `auth_failed`
- `server_shutdown`
- `session_not_found`
- `backpressure`

The implementation may add more reasons later, but it should preserve the same short-string style.

## Privacy and Noise Rules

- Do not log `Message.Data`.
- Do not log decoded PTY output or browser input.
- Do not log every successful frame forwarding operation.
- Do not derive previews from payloads for logs.
- Prefer one lifecycle log at connect time and one at disconnect time over repetitive progress logs.

## Code Touchpoints

### [cmd/relay/main.go](/Users/yuanbo/workspace/github.com/agent-tunnel/cmd/relay/main.go)

Emit `relay_started` after the main config is loaded and before the HTTP server begins serving.

### [relay/server.go](/Users/yuanbo/workspace/github.com/agent-tunnel/relay/server.go)

Emit:

- `auth_failed` on failed Basic Auth or Bearer auth
- `ws_upgrade_failed` when either upgrader returns an error
- `agent_ws_connected` on successful agent WebSocket upgrade
- `agent_registered` after a valid register frame is accepted
- `agent_disconnected` when the agent loop exits after successful registration
- `client_ws_connected` after a client sink is attached
- `client_disconnected` when the client loop exits after attach
- `sink_backpressure` near sink enqueue failure handling

This file owns most lifecycle transitions, so it should remain the main logging surface.

### [relay/registry.go](/Users/yuanbo/workspace/github.com/agent-tunnel/relay/registry.go)

Emit `session_replaced` when `Register` swaps an existing owner for a new owner of the same `session_id`.

## Logging API Shape

Introduce a thin relay-scoped structured logger wrapper instead of scattering raw `log.Printf` calls across handlers.

The wrapper only needs three methods:

- `Info(event string, fields ...Field)`
- `Warn(event string, fields ...Field)`
- `Error(event string, fields ...Field)`

This keeps the first implementation small while preserving the option to switch from the standard library to `log/slog` or another backend later without rewriting event call sites.

## Testing Expectations

Tests should verify:

- startup log emission shape for `relay_started`
- authentication failures emit `auth_failed`
- successful agent registration emits `agent_registered`
- replacing an existing session owner emits `session_replaced`
- successful client attach emits `client_ws_connected`
- disconnect paths emit `agent_disconnected` and `client_disconnected`
- sink queue overflow emits `sink_backpressure`
- payload contents never appear in log output

The tests do not need to assert timestamp formatting exactly, but they should assert event names and critical fields.

## Rollout Notes

Phase 1 should ship only the lifecycle and sparse anomaly logs defined in this document.

Future extensions may add:

- debug-only frame tracing
- per-session periodic summaries
- metrics export

Those additions should be optional and must not change the default low-noise behavior defined here.
