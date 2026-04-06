# Agent Tunnel Relay Protocol

This document describes the relay-facing contract for clients and agents.

The key boundary is simple:

- the relay is content-opaque
- output bytes may be forwarded and retained for replay
- clients should not infer semantics from PTY text

## Endpoint Inventory

| Endpoint | Role | Auth | Kind | Purpose |
|----------|------|------|------|---------|
| `GET /healthz` | Any | None | HTTP | Health check |
| `GET /api/sessions` | Client | Basic Auth | HTTP | Current live session snapshot |
| `GET /api/sessions/:id/frames?from=<seq>&to=<seq>` | Client | Basic Auth | HTTP | Retained output replay for one session |
| `GET /api/updates/ws` | Client | Basic Auth | WebSocket | Global multiplexed live output updates and client input |
| `GET /agent/ws` | Agent | Bearer | WebSocket | Agent registration plus relay forwarding |

## Auth Headers

Client endpoints use Basic Auth:

```text
Authorization: Basic base64(username:password)
```

Agent registration uses a bearer token:

```text
Authorization: Bearer <token>
```

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

## Output Frames

`GET /api/sessions/:id/frames?from=<seq>&to=<seq>` returns a JSON array. Both query parameters are optional and inclusive.

```json
[
  { "seq": 41, "data_b64": "b25l", "cols": 120, "rows": 40 },
  { "seq": 42, "data_b64": "dHdv", "cols": 132, "rows": 43 }
]
```

Query behavior:

- no `from` or `to`: return every retained output frame
- only `from`: return frames with `seq >= from`
- only `to`: return frames with `seq <= to`
- both `from` and `to`: return frames in the closed range `[from, to]`

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
  "data": "SGVsbG8="
}
```

`resize`

```json
{
  "type": "resize",
  "cols": 132,
  "rows": 43
}
```

### Client -> Relay

`input`

```json
{
  "session_id": "sess-1",
  "type": "input",
  "data": "bHMK"
}
```

### Relay -> Client

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

`session_removed`

```json
{
  "session_id": "sess-1",
  "type": "session_removed",
  "reason": "agent_disconnected"
}
```
