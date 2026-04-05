# Agent Tunnel Relay Protocol

This document describes the relay-facing contract for native or web clients.

The key boundary is simple:

- the relay is content-opaque
- output bytes may be forwarded and retained for replay
- structured metadata such as `action_required` is tracked separately from terminal content

Clients should never depend on relay-generated previews or on terminal text parsing to infer session state.

## Relay API Reference

This section is the compact interface inventory for the current relay server. It is meant to answer:

- what endpoints exist
- which auth each one requires
- what parameters they accept
- what the success payload looks like

The later sections explain semantics and recommended client behavior in more detail.

### Endpoint Inventory

| Endpoint | Role | Auth | Kind | Purpose |
|----------|------|------|------|---------|
| `GET /healthz` | Any | None | HTTP | Health check |
| `GET /api/sessions` | Client | Basic Auth | HTTP | Current live session snapshot |
| `GET /api/sessions/:id/history?after=<seq>` | Client | Basic Auth | HTTP | Retained output replay for one session |
| `POST /api/sessions/:id/read` | Client | Basic Auth | HTTP | Advance shared read marker |
| `GET /api/updates/ws` | Client | Basic Auth | WebSocket | Global multiplexed live updates and client input |
| `GET /agent/ws` | Agent | Bearer | WebSocket | Agent registration plus relay forwarding |

### Auth Headers

Client endpoints use Basic Auth:

```text
Authorization: Basic base64(username:password)
```

Agent registration uses a bearer token:

```text
Authorization: Bearer <token>
```

### HTTP Endpoints

#### `GET /healthz`

Purpose: simple liveness probe.

Request parameters:

| Name | Location | Required | Type | Notes |
|------|----------|----------|------|-------|
| none | - | - | - | No auth and no request body |

Success response:

| Status | Content-Type | Body |
|--------|--------------|------|
| `200 OK` | `text/plain` | `ok` |

#### `GET /api/sessions`

Purpose: fetch the current snapshot of every live session.

Request parameters:

| Name | Location | Required | Type | Notes |
|------|----------|----------|------|-------|
| `Authorization` | header | yes | string | HTTP Basic Auth |

Success response:

| Status | Content-Type | Body |
|--------|--------------|------|
| `200 OK` | `application/json` | JSON array of `SessionInfo` |

`SessionInfo` shape:

```json
{
  "session_id": "sess-1",
  "launcher": "codex",
  "label": "api-fix",
  "cwd": "/repo",
  "command_preview": "codex --profile prod",
  "started_at": "2026-04-05T08:00:00Z",
  "last_active_at": "2026-04-05T08:03:00Z",
  "latest_seq": 42,
  "last_read_seq": 40,
  "unread_count": 2,
  "state": "action_required",
  "state_changed_at": "2026-04-05T08:02:00Z",
  "action_required_since": "2026-04-05T08:02:00Z"
}
```

| Field | Type | Meaning |
|-------|------|---------|
| `session_id` | string | Stable session identifier |
| `launcher` | string | `claude`, `codex`, or `gemini` |
| `label` | string | Optional operator label |
| `cwd` | string | Launch working directory |
| `command_preview` | string | Human-readable command summary |
| `started_at` | RFC3339 string | Session start time |
| `last_active_at` | RFC3339 string | Time of most recent output activity |
| `latest_seq` | integer | Latest retained output sequence |
| `last_read_seq` | integer | Shared read marker |
| `unread_count` | integer | `latest_seq - last_read_seq` |
| `state` | string | `normal` or `action_required` |
| `state_changed_at` | RFC3339 string | When current state was entered |
| `action_required_since` | RFC3339 string | Start of current unresolved waiting episode |

#### `GET /api/sessions/:id/history?after=<seq>`

Purpose: fetch retained output frames for one live session.

Request parameters:

| Name | Location | Required | Type | Notes |
|------|----------|----------|------|-------|
| `id` | path | yes | string | Session ID |
| `after` | query | no | integer | Return frames with `seq > after`; defaults to `0` |
| `Authorization` | header | yes | string | HTTP Basic Auth |

Success response:

| Status | Content-Type | Body |
|--------|--------------|------|
| `200 OK` | `application/json` | `HistoryPage` |

`HistoryPage` shape:

```json
{
  "messages": [
    { "seq": 41, "data_b64": "b25l", "cols": 120, "rows": 40 },
    { "seq": 42, "data_b64": "dHdv", "cols": 132, "rows": 43 }
  ],
  "latest_seq": 42,
  "last_read_seq": 40,
  "current_cols": 132,
  "current_rows": 43
}
```

| Field | Type | Meaning |
|-------|------|---------|
| `messages` | array | Chronological retained output frames |
| `messages[].seq` | integer | Output sequence number |
| `messages[].data_b64` | string | Base64-encoded opaque PTY bytes |
| `messages[].cols` | integer | Terminal width when that output was produced |
| `messages[].rows` | integer | Terminal height when that output was produced |
| `latest_seq` | integer | Latest live output sequence known to the relay |
| `last_read_seq` | integer | Current shared read marker |
| `current_cols` | integer | Latest terminal width reported by the agent |
| `current_rows` | integer | Latest terminal height reported by the agent |

#### `POST /api/sessions/:id/read`

Purpose: advance the shared read marker for one session.

Request parameters:

| Name | Location | Required | Type | Notes |
|------|----------|----------|------|-------|
| `id` | path | yes | string | Session ID |
| `Authorization` | header | yes | string | HTTP Basic Auth |
| `Content-Type` | header | yes | string | Must be `application/json` |
| `seq` | JSON body | yes | integer | Requested read watermark |

Request body:

```json
{ "seq": 42 }
```

Success response:

| Status | Content-Type | Body |
|--------|--------------|------|
| `200 OK` | `application/json` | Updated read-state snapshot |

Response shape:

```json
{
  "session_id": "sess-1",
  "latest_seq": 42,
  "last_read_seq": 42,
  "unread_count": 0
}
```

### WebSocket Endpoints

#### `GET /api/updates/ws`

Purpose: the primary client connection. One WebSocket carries live updates for all sessions and also accepts client input routed by `session_id`.

Handshake parameters:

| Name | Location | Required | Type | Notes |
|------|----------|----------|------|-------|
| `Authorization` | header | yes | string | HTTP Basic Auth |
| `Origin` | header | no | string | If present, must pass same-origin check |

Server -> client frame types:

`output`

```json
{
  "session_id": "sess-1",
  "type": "output",
  "seq": 42,
  "data": "SGVsbG8=",
  "cols": 132,
  "rows": 43
}
```

`session_state`

```json
{
  "session_id": "sess-1",
  "type": "session_state",
  "state": "action_required",
  "changed_at": "2026-04-05T08:02:00Z",
  "action_required_since": "2026-04-05T08:02:00Z"
}
```

`session_removed`

```json
{
  "session_id": "sess-1",
  "type": "session_removed",
  "reason": "agent_disconnected"
}
```

Client -> server frame type:

`input`

```json
{
  "session_id": "sess-1",
  "type": "input",
  "data": "bHMK"
}
```

Shared frame fields:

| Field | Present on | Meaning |
|-------|------------|---------|
| `session_id` | all | Session routing key |
| `type` | all | Frame discriminator |
| `seq` | `output` | Monotonic output sequence for that session |
| `data` | `output`, `input` | Base64-encoded opaque PTY bytes or stdin bytes |
| `cols` / `rows` | `output` | Terminal size when that output was produced |
| `state` | `session_state` | `normal` or `action_required` |
| `changed_at` | `session_state` | Transition timestamp |
| `action_required_since` | `session_state` | Present only while unresolved |
| `reason` | `session_removed` | Removal hint such as `agent_disconnected` |

#### `GET /agent/ws`

Purpose: agent registration plus bidirectional relay transport between `agentunnel` and the relay.

Handshake parameters:

| Name | Location | Required | Type | Notes |
|------|----------|----------|------|-------|
| `Authorization` | header | yes | string | Bearer token |

First client -> server frame must be agent registration:

```json
{
  "type": "register",
  "session": {
    "session_id": "sess-1",
    "launcher": "codex",
    "cwd": "/repo",
    "command_preview": "codex",
    "started_at": "2026-04-05T08:00:00Z"
  }
}
```

Later frame types:

| Type | Direction | Meaning |
|------|-----------|---------|
| `input` | relay -> agent | Forward client input bytes |
| `output` | agent -> relay | Forward PTY output bytes |
| `resize` | agent -> relay | Update terminal size metadata |
| `session_state` | agent -> relay | Update structured session state |

Representative `session_state` frame:

```json
{
  "type": "session_state",
  "state": "action_required",
  "changed_at": "2026-04-05T08:02:00Z",
  "action_required_since": "2026-04-05T08:02:00Z"
}
```

## Detailed Semantics

### Session Snapshot

- `GET /api/sessions` is the canonical source of truth for the current state of every live session.
- There is intentionally no preview field in the session snapshot.
- `action_required` is structured session metadata, not something clients should infer from PTY text.

### Global Live Stream

- `GET /api/updates/ws` is the preferred foreground client channel.
- One socket carries live updates for every session, distinguished by `session_id`.
- The same socket is also used for client `input` frames back to one session.
- `output` frames are opaque transport payloads. The relay forwards them but does not interpret them.
- The global stream is live-only. It does not replay old frames on connect.

### Per-Session History

- `GET /api/sessions/:id/history?after=<seq>` returns retained output frames with `seq > after`.
- `after=0` returns the currently retained in-memory buffer.
- History is live-only and disappears when the session disappears.
- History is output-only; it does not contain `session_state` transitions.

### Read Marker

- `POST /api/sessions/:id/read` advances a shared per-session read marker.
- The relay clamps the submitted `seq` to `latest_seq`.
- The marker is monotonic; posting a lower value does not move it backward.

### Agent Session State

- Agents send `session_state` over `GET /agent/ws`.
- The relay stores that as structured session metadata.
- The relay republishes it on the global client stream.
- Session-state transitions are not inserted into output history.

## Recommended Client Strategy

Foreground client flow:

1. Call `GET /api/sessions` on launch or foreground resume.
2. Open one global `GET /api/updates/ws` connection.
3. Route every frame by `session_id`.
4. If a session needs catch-up, compare local seq watermarks with `latest_seq` and fetch `GET /api/sessions/:id/history?after=<seq>`.
5. If the user sends input to one session, write an `input` frame on the same global WebSocket with that `session_id`.

Why this works:

- list freshness comes from the global stream
- terminal fidelity comes from global live output plus per-session history replay
- preview generation, if any, belongs to the client because the relay is content-opaque
