# Agent Tunnel Relay Protocol

This document describes the relay-facing contract for clients and agents.

## Core Model

The current protocol is built around these boundaries:

- `session_id` identifies one running `tunnel` process; relay reconnects keep the same `session_id`, while a fresh agent launch gets a new one
- the owning agent is the authority for the current session transcript and for `seq`, `ts`, and `latest_seq`
- the relay is a discovery, fanout, and proxy layer; it does not retain the session frame array
- `GET /api/updates/ws` is a best-effort live channel
- `GET /api/sessions/:id/frames` is the standard recovery path for agent-owned output while the session is `connected`
- transcript history is output-only; there is no exact input log in this protocol revision
- the local terminal remains the most complete view of the PTY session

## Endpoint Inventory

| Endpoint | Role | Auth | Kind | Purpose |
|----------|------|------|------|---------|
| `GET /healthz` | Any | None | HTTP | Health check |
| `GET /api/sessions` | Client | Basic Auth | HTTP | Current live session snapshot |
| `GET /api/sessions/:id/frames?from=<seq>&to=<seq>` | Client | Basic Auth | HTTP | Proxy a replay request to the connected owning agent |
| `GET /api/updates/ws` | Client | Basic Auth | WebSocket | Best-effort global live output updates and structured client input |
| `GET /agent/ws` | Agent | Bearer | WebSocket | Agent registration, live output upload, proxied history requests, and forwarded client input |

## Auth Headers

Client endpoints use Basic Auth:

```text
Authorization: Basic base64(username:password)
```

Agent registration uses a bearer token:

```text
Authorization: Bearer <token>
```

WebSocket attach notes:

- clients attach to `GET /api/updates/ws` with the same Basic Auth credentials as the HTTP endpoints
- agents attach to `GET /agent/ws` with the bearer token

## Session Snapshot

`GET /api/sessions` returns an array of `SessionInfo`:

```json
{
  "session_id": "sess-1",
  "launcher": "codex",
  "label": "api-fix",
  "cwd": "/repo",
  "command_preview": "codex --profile prod",
  "started_at": "2026-04-05T08:00:00Z",
  "last_active_at": "2026-04-05T08:03:00Z",
  "state": "connected",
  "latest_seq": 42
}
```

Notes:

- `label` may be omitted when empty
- `last_active_at` may be omitted when unknown
- `state` is `connected` when the relay currently has an owning agent websocket and `reconnecting` during the short post-disconnect grace window
- `latest_seq` is the highest output sequence the relay currently knows for that live session from agent-authored metadata
- `reconnecting` sessions remain discoverable briefly, but they do not serve `/frames` or accept remote input until the owning agent reconnects
- there is no pushed session-state update on `GET /api/updates/ws` in this protocol revision; clients should refetch `GET /api/sessions` to observe state changes

## History Ownership And Replay

The current replay contract is agent-owned.

- `tunnel` appends PTY output into a bounded in-memory history buffer for the lifetime of the running session
- each retained entry is a `ReplayFrame` with `seq`, `data_b64`, `cols`, `rows`, and `ts`
- the relay stores metadata, owner connection state, and pending history-request bookkeeping, but not the transcript itself
- when a client fetches `/api/sessions/:id/frames`, the relay issues a `history_request` to the connected agent and returns the agent's `history_response`

Replay flow:

1. the client calls `GET /api/sessions/:id/frames` with optional inclusive `from` and `to`
2. the relay authenticates the request, validates query bounds, and looks up the live session
3. if the session is `connected`, the relay allocates a `request_id`, records a pending waiter, and sends `history_request` over `/agent/ws`
4. the agent snapshots its local history buffer for the requested bounds and replies with `history_response`
5. the relay matches the response by `request_id` and returns the `frames` array as the HTTP response body

The history is a terminal output transcript, not an exact input log. Locally typed characters and remote input only appear in replay when the terminal application echoes them.

## Replay Frames

`GET /api/sessions/:id/frames?from=<seq>&to=<seq>` returns a JSON array. Both query parameters are optional and inclusive. The response body is the array itself, not an envelope object.

```json
[
  {
    "seq": 41,
    "data_b64": "b25l",
    "cols": 120,
    "rows": 40,
    "ts": "2026-04-06T02:10:01Z"
  },
  {
    "seq": 42,
    "data_b64": "dHdv",
    "cols": 132,
    "rows": 43,
    "ts": "2026-04-06T02:10:02Z"
  }
]
```

Field semantics:

- `seq`: strictly increasing output sequence number within the live session for frames the owning agent recorded
- `data_b64`: base64-encoded PTY output bytes
- `cols` / `rows`: terminal size metadata captured for that frame
- `ts`: agent-authored UTC timestamp recorded when the frame is appended to the agent-side history buffer

Query behavior:

- no `from` or `to`: return every currently retained frame from the owning agent
- only `from`: return frames with `seq >= from`
- only `to`: return frames with `seq <= to`
- both `from` and `to`: return frames in the closed range `[from, to]`
- if both are present and `from > to`: return `400 Bad Request`

Status behavior:

- `404 Not Found` with `{"reason":"session_not_found"}` when the session is unknown or the reconnect grace window has expired
- `409 Conflict` with `{"reason":"session_reconnecting"}` when the session is listed but currently has no connected owning agent
- `502 Bad Gateway` with `{"reason":"invalid_agent_response"}` when the agent returns a malformed history response
- `504 Gateway Timeout` with `{"reason":"upstream_timeout"}` when the relay does not receive a history response before timeout

Recovery notes:

- `/api/sessions/:id/frames` is the standard replay path after a client reconnects to `GET /api/updates/ws`
- replay is limited to the output frames the owning agent still retains in memory for that still-running session
- `seq` does not prove complete end-to-end delivery from the local PTY to a remote client; it orders agent-recorded frames only

Example requests:

```text
GET /api/sessions/sess-1/frames
GET /api/sessions/sess-1/frames?from=101
GET /api/sessions/sess-1/frames?from=101&to=120
```

## Frames On `/agent/ws`

`/agent/ws` is a bidirectional, session-scoped websocket between the relay and the owning `tunnel` process.

### Agent -> Relay

`register`

```json
{
  "type": "register",
  "session": {
    "session_id": "sess-1",
    "launcher": "codex",
    "cwd": "/repo",
    "command_preview": "codex --profile prod",
    "started_at": "2026-04-05T08:00:00Z",
    "last_active_at": "2026-04-05T08:03:00Z",
    "latest_seq": 42
  }
}
```

Notes:

- `register` must be the first agent frame on the websocket
- the relay treats that websocket as the owner of the live session
- on first connect, `last_active_at` may be omitted and `latest_seq` may be `0`
- on reconnect, the registering agent may advertise its current `last_active_at` and `latest_seq` so the relay can continue exposing the same live-session metadata

`output`

```json
{
  "type": "output",
  "seq": 42,
  "data_b64": "SGVsbG8=",
  "cols": 132,
  "rows": 43,
  "ts": "2026-04-06T02:10:02Z"
}
```

Notes:

- agent output carries agent-authored `seq` and `ts`
- every agent output frame must include `cols` and `rows`
- the relay forwards live output with the same `seq`, `ts`, `cols`, and `rows` it received from the agent
- there is no standalone `resize` event in this protocol revision

`history_response`

```json
{
  "type": "history_response",
  "request_id": "history-1",
  "frames": [
    {
      "seq": 101,
      "data_b64": "b25l",
      "cols": 120,
      "rows": 40,
      "ts": "2026-04-06T02:10:01Z"
    }
  ]
}
```

Notes:

- `request_id` must match a relay-issued `history_request`
- every replay frame in `frames` must include non-zero `seq`, non-empty `data_b64`, and non-zero `ts`
- the relay treats malformed or mismatched history responses as upstream protocol errors

### Relay -> Agent

`history_request`

```json
{
  "type": "history_request",
  "request_id": "history-1",
  "from": 101,
  "to": 120
}
```

Notes:

- `from` and `to` are optional and inclusive
- the relay sends this message only for a currently `connected` session
- the agent should answer with one `history_response` carrying the requested snapshot of its local in-memory history

`input_text`

```json
{
  "type": "input_text",
  "text": "hello",
  "submit": false
}
```

`input_key`

```json
{
  "type": "input_key",
  "key": "TAB",
  "ctrl": false,
  "alt": false,
  "shift": false
}
```

Once the relay chooses the target session, forwarded agent input is the same logical payload as client input, but without `session_id`.

## Frames On `GET /api/updates/ws`

`GET /api/updates/ws` is the client-facing multiplexed websocket. It carries best-effort live output for many sessions on one connection, plus client-to-relay input events.

### Relay -> Client

`output`

```json
{
  "session_id": "sess-1",
  "type": "output",
  "seq": 42,
  "data_b64": "SGVsbG8=",
  "cols": 132,
  "rows": 43,
  "ts": "2026-04-06T02:10:02Z"
}
```

Notes:

- relay-to-client output preserves the agent-authored `seq`, `ts`, `cols`, and `rows`
- this channel is best-effort; clients should not assume it contains a complete transcript

`session_removed`

```json
{
  "session_id": "sess-1",
  "type": "session_removed",
  "reason": "session_removed"
}
```

The relay emits `session_removed` when a live session expires from the registry, including after reconnect grace expiry.

### Client -> Relay

#### `input_text`

Use this for:

- normal typing
- pasted text
- IME-committed text
- local draft text that should not imply submit
- explicit submit actions when the client intends atomic `text + Enter`

```json
{
  "session_id": "sess-1",
  "type": "input_text",
  "text": "hello",
  "submit": false
}
```

Rules:

- `text` is UTF-8 text
- `submit` is optional and defaults to `false`
- plain character input belongs here, not in `input_key`
- if `submit` is `false`, `input_text` does not imply `Enter`
- if `submit` is `true`, the event is an atomic submit intent, not a best-effort client macro
- when `submit` is `true`, the relay and owning agent must preserve ordering so the PTY receives `text` first and then the submit carriage return as one serialized operation for that session
- when `submit` is `true`, the owning agent appends exactly one trailing carriage return (`\r`) beyond the provided text body
- the appended carriage return must match the existing `input_key` handling for `ENTER` in the owning agent

#### `input_key`

Use this for special keys and modified keys.

```json
{
  "session_id": "sess-1",
  "type": "input_key",
  "key": "TAB",
  "ctrl": false,
  "alt": false,
  "shift": false
}
```

Example:

```json
{
  "session_id": "sess-1",
  "type": "input_key",
  "key": "C",
  "ctrl": true,
  "alt": false,
  "shift": false
}
```

## Supported `input_key` Values

The current relay contract should support at least:

- `ENTER`
- `BACKSPACE`
- `TAB`
- `ESCAPE`
- `UP`
- `DOWN`
- `LEFT`
- `RIGHT`
- `HOME`
- `END`
- `PAGE_UP`
- `PAGE_DOWN`
- `DELETE`

Recommended in the same revision:

- single-character keys for control combinations, for example `key="C", ctrl=true`
- `Ctrl+A-Z`

Notes:

- plain text characters such as `"a"` or `"C"` should normally use `input_text`
- `input_key` is for non-text key semantics and modified shortcuts
- `alt` and `shift` are part of the wire format, but v1 behavior is only guaranteed for documented supported combinations

## Key Mapping Ownership

The relay does not translate `input_key` into PTY bytes.

Instead:

1. the client sends structured input
2. the relay validates and forwards it to the owning session
3. `tunnel` translates supported `input_key` events into PTY input bytes
4. `tunnel` writes those bytes into the local PTY stdin

This keeps terminal behavior close to the PTY owner and avoids embedding terminal-emulation logic in the relay.

## Size Metadata

- every replay frame carries `cols` and `rows`
- live `output` websocket events on `GET /api/updates/ws` carry `cols` and `rows`
- agent-uploaded `output` frames on `/agent/ws` carry `cols` and `rows`
- there is no separate resize stream; size is part of each output frame contract

## Client Notes

- clients should use `GET /api/sessions` to observe `connected` versus `reconnecting`
- clients should reconnect `GET /api/updates/ws` for live output and use `/api/sessions/:id/frames` to recover missed transcript
- the Android client expects a `baseUrl` with an explicit scheme such as `http://...`
- clients may validate relay availability with `GET /api/sessions` or fallback `GET /healthz`

## Invariants

- output replay remains live-only and in-memory on the owning agent
- output `seq` is strictly increasing within one live session
- replayed frames and live output fanout refer to the same output events
- replayed frames and live output fanout use the same agent-authored `ts` for the same output frame
