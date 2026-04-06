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
| `GET /api/sessions/:id/history?after=<seq>` | Client | Basic Auth | HTTP | Retained output replay for one session |
| `POST /api/sessions/:id/read` | Client | Basic Auth | HTTP | Advance shared read marker |
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
  "latest_seq": 42,
  "last_read_seq": 40,
  "unread_count": 2
}
```

## History Replay

`GET /api/sessions/:id/history?after=<seq>` returns:

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

## Mark Read

`POST /api/sessions/:id/read` accepts:

```json
{ "seq": 42 }
```

and returns the updated session snapshot.

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
