# Agent Tunnel Relay Protocol

This document describes the relay-facing contract for clients and agents.

The key boundary is simple:

- the relay is content-opaque
- `GET /api/updates/ws` is a best-effort live channel
- output bytes may be forwarded and retained for replay
- clients should not infer semantics from PTY text
- clients send structured input events
- `agentunnel`, as the PTY owner, translates supported key events into real PTY input bytes
- the local terminal remains the most complete view of session output in the current revision

## Endpoint Inventory

| Endpoint | Role | Auth | Kind | Purpose |
|----------|------|------|------|---------|
| `GET /healthz` | Any | None | HTTP | Health check |
| `GET /api/sessions` | Client | Basic Auth | HTTP | Current live session snapshot |
| `GET /api/sessions/:id/frames?from=<seq>&to=<seq>` | Client | Basic Auth | HTTP | Retained output replay and standard relay-side recovery for one live session |
| `GET /api/updates/ws` | Client | Basic Auth | WebSocket | Best-effort global live output updates and structured client input |
| `GET /agent/ws` | Agent | Bearer | WebSocket | Agent registration, output upload, and forwarded client input |

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

- browser client attach on `GET /api/updates/ws` is same-origin checked when `Origin` is present
- non-browser clients that do not send `Origin` are allowed by this protocol revision

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
  "latest_seq": 42
}
```

Notes:

- `label` may be omitted when empty
- `last_active_at` may be omitted when unknown
- `latest_seq` is the highest output sequence the relay has recorded for that live session
- this protocol revision does not define `state` or `session_state`

## Output Frames

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

- `seq`: strictly increasing output sequence number within the live session for frames the relay has recorded
- `data_b64`: base64-encoded PTY output bytes
- `cols` / `rows`: terminal size metadata captured for that frame
- `ts`: relay-assigned UTC timestamp recorded when the frame is appended to retained history

Query behavior:

- no `from` or `to`: return every retained output frame
- only `from`: return frames with `seq >= from`
- only `to`: return frames with `seq <= to`
- both `from` and `to`: return frames in the closed range `[from, to]`
- if both are present and `from > to`: return `400 Bad Request`

Recovery notes:

- `/api/sessions/:id/frames` is the standard relay-side recovery path after a client reconnects to `GET /api/updates/ws`
- replay is limited to the frames the relay still retains in memory for that still-live session
- `seq` does not prove complete end-to-end delivery from the local PTY to a remote client; it orders relay-recorded frames only

Example requests:

```text
GET /api/sessions/sess-1/frames
GET /api/sessions/sess-1/frames?from=101
GET /api/sessions/sess-1/frames?from=101&to=120
```

## WebSocket Frames

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
    "started_at": "2026-04-05T08:00:00Z"
  }
}
```

`output`

```json
{
  "type": "output",
  "data": "SGVsbG8=",
  "cols": 132,
  "rows": 43
}
```

Notes:

- agent output does not carry `seq` or `ts`
- every agent output frame must include `cols` and `rows`
- the relay assigns `seq` and `ts` when it records the output frame
- there is no standalone `resize` event in this protocol revision
- because the relay assigns `seq` only after recording a frame, relay sequence metadata begins at the relay boundary rather than the PTY boundary

### Client -> Relay

#### `input_text`

Use this for:

- normal typing
- pasted text
- IME-committed text

```json
{
  "session_id": "sess-1",
  "type": "input_text",
  "text": "hello"
}
```

Rules:

- `text` is UTF-8 text
- plain character input belongs here, not in `input_key`

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

Rules:

- `key` is a symbolic key identifier, not a terminal escape sequence
- clients must not manufacture terminal byte sequences for special keys
- unsupported keys may be ignored safely

### Relay -> Agent

The relay forwards structured client input to the owning `agentunnel` session over `/agent/ws`.

#### `input_text`

```json
{
  "type": "input_text",
  "text": "hello"
}
```

#### `input_key`

```json
{
  "type": "input_key",
  "key": "TAB",
  "ctrl": false,
  "alt": false,
  "shift": false
}
```

Compatibility note:

- the legacy raw-byte `input` message may remain temporarily during migration
- new clients should target `input_text` and `input_key`

### Relay -> Client

`output`

```json
{
  "session_id": "sess-1",
  "type": "output",
  "seq": 42,
  "data": "SGVsbG8=",
  "cols": 132,
  "rows": 43,
  "ts": "2026-04-06T02:10:02Z"
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

Notes:

- `data` is base64-encoded PTY output bytes
- `cols` and `rows` are carried on every live output frame
- `ts` is the same relay-assigned timestamp stored in retained frame history for the same output frame
- this live stream is best-effort; clients should reconnect and use `/api/sessions/:id/frames` to recover recent relay-retained output when needed

## Current Boundary

- the current protocol supports real remote observation and interaction
- the live remote output path is intentionally best-effort in this revision
- stronger delivery guarantees may be added in a future revision, but they are not part of the current contract

## Supported `input_key` Values

The first implementation should support at least:

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
3. `agentunnel` translates supported `input_key` events into PTY input bytes
4. `agentunnel` writes those bytes into the local PTY stdin

This keeps terminal behavior close to the PTY owner and avoids embedding terminal-emulation logic in the relay.

## Size Metadata

- every output frame carries `cols` and `rows`
- retained frame replay includes `cols` and `rows`
- live `output` websocket events include `cols` and `rows`
- agent-uploaded `output` frames on `/agent/ws` include `cols` and `rows`
- there is no separate resize stream; size is part of each output frame contract

## Error Behavior

- invalid client credentials return `401`
- unknown session ids return `404` for frame replay requests
- malformed websocket input payloads are ignored or rejected safely
- websocket disconnects do not alter retained output history for still-live sessions
- if the owning agent disconnects, the session disappears along with its retained frames

## Compatibility Notes

- the Android client expects a `baseUrl` with an explicit scheme such as `http://...`
- clients may validate relay availability with:
  - `GET /api/sessions`
  - fallback `GET /healthz`

## Invariants

- output replay remains live-only and in-memory
- output `seq` is strictly increasing within one live session
- retained frames and live output fanout refer to the same output events
- retained frames and live output fanout use the same `ts` for the same output frame
