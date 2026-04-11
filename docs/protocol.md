# Agent Tunnel Relay Protocol

This document describes the current relay-facing contract for clients and agents.

It supersedes the older replay/frame model. Clients should build against this attach-based protocol, not against `/api/updates/ws`, `/api/sessions/:id/frames`, `ReplayFrame`, or `latest_seq`.

## Core Model

The current protocol is built around these boundaries:

- `session_id` identifies one running `tunnel` process. Relay reconnects for that process keep the same `session_id`. A fresh agent launch gets a fresh `session_id`.
- The owning agent is the authority for the current terminal state of that session.
- The relay is a discovery, auth, and routing layer. It does not retain transcript history and does not emulate the terminal.
- Sessions are discoverable only while the owning agent websocket is connected. If the agent disconnects, the session disappears from discovery immediately and reappears when the agent re-registers with the same `session_id`.
- Remote viewing is session-scoped: a client attaches to one session, receives a current-state snapshot, and then receives subsequent live PTY bytes on that same attach.
- Remote recovery in this revision is current-state recovery only. There is no transcript replay API.
- The local terminal remains the most complete and authoritative foreground view of the PTY session.

All protocol timestamps are Unix timestamps represented as JSON integers in seconds.

## Endpoint Inventory

| Endpoint | Role | Auth | Kind | Purpose |
|----------|------|------|------|---------|
| `GET /healthz` | Host-local | None | HTTP | Health check for direct relay access; production nginx can keep this off the public surface |
| `GET /api/sessions` | Client | Bearer | HTTP | Current live session snapshot for the authenticated user |
| `GET /api/sessions/:id/attach/ws` | Client | Bearer | WebSocket | Attach to one live session owned by the authenticated user for snapshot, live bytes, resize events, and session-scoped structured input |
| `GET /agent/ws` | Agent | Bearer | WebSocket | Agent registration, attach control, resize metadata, structured input forwarding, and client-routed terminal byte delivery |

Removed from the product contract:

- `GET /api/updates/ws`
- `GET /api/sessions/:id/frames`

## Auth Headers

App-facing client endpoints use a bearer access token:

```text
Authorization: Bearer <access-token>
```

Agent registration also uses a bearer token, but it is a user-owned long-lived agent token:

```text
Authorization: Bearer <agent-token>
```

WebSocket attach notes:

- clients attach to `GET /api/sessions/:id/attach/ws` with the same bearer access token as the HTTP endpoints
- browser clients must present a same-origin `Origin` header for the relay host; cross-origin browser attaches are rejected
- native clients that omit `Origin` are allowed
- agents attach to `GET /agent/ws` with the bearer token

## Session Snapshot

`GET /api/sessions` returns an array of `SessionInfo` objects:

```json
{
  "session_id": "sess-1",
  "launcher": "codex",
  "label": "api-fix",
  "cwd": "/repo",
  "command_preview": "codex --profile prod",
  "started_at": 1775376000
}
```

Notes:

- `label` may be omitted when empty
- `started_at` is a Unix timestamp in seconds
- every returned session currently has an owning agent websocket and is attachable

## Attach Lifecycle

`GET /api/sessions/:id/attach/ws` is the client-facing session attach websocket.

The attach contract is:

1. the client authenticates and opens `GET /api/sessions/:id/attach/ws`
2. the relay verifies that the session currently exists in discovery for the authenticated user
3. the relay allocates a relay-scoped `client_id` and sends `attach_open` to the owning agent
4. the agent atomically:
   - captures the current terminal size
   - serializes the current visible terminal state into snapshot bytes
   - registers that attached client for subsequent live-byte delivery
5. the agent sends `attach_ready`
6. the agent sends snapshot bytes
7. the agent sends `snapshot_done`
8. subsequent terminal byte packets on that attach are live PTY bytes

The no-gap rule:

- there must be no byte gap between the serialized snapshot point and the first subsequent live bytes for that attached client

Client recovery rule:

- when an attach drops or closes, the client should create a fresh terminal emulator state and open a fresh attach
- clients must not treat reconnect as transcript replay

## Client Attach WebSocket

`GET /api/sessions/:id/attach/ws` is a mixed websocket:

- relay-to-client control messages are JSON text frames
- relay-to-client terminal data is binary websocket frames carrying raw terminal bytes
- client-to-relay structured input is JSON text frames

### Relay -> Client Control Messages

#### `attached`

This is always the first message on a successful attach.

```json
{
  "type": "attached",
  "session_id": "sess-1",
  "cols": 132,
  "rows": 43
}
```

Notes:

- `cols` and `rows` define the terminal size the client must apply before feeding subsequent binary bytes into its terminal emulator
- after `attached`, the client should expect snapshot bytes, then `snapshot_done`

#### `snapshot_done`

```json
{
  "type": "snapshot_done"
}
```

Notes:

- this marks the end of the initial current-state snapshot
- after this point, all subsequent binary frames are live PTY bytes
- binary bytes before `snapshot_done` and after `snapshot_done` should both be fed into the same terminal emulator in arrival order

#### `resize`

```json
{
  "type": "resize",
  "cols": 120,
  "rows": 40
}
```

Notes:

- resize is session-wide and follows the PTY owner
- remote clients must resize their terminal emulator to match
- remote clients do not become the PTY size authority in this protocol revision

#### `closing`

```json
{
  "type": "closing",
  "reason": "session_offline"
}
```

Known reasons:

- `client_closed`
- `session_offline`
- `slow_client`
- `logged_out`
- `password_changed`

Notes:

- the relay should send `closing` before it closes the websocket when a reason is known
- clients should key off the `reason` value, not off a particular websocket close code
- `logged_out` means the current app session was explicitly logged out; the agent session may still be online
- `password_changed` means the user's app sessions were revoked after a password change; the agent session may still be online

### Relay -> Client Binary Frames

Each binary websocket frame carries raw terminal bytes.

Rules:

- the first binary frames after `attached` are the serialized snapshot bytes
- after `snapshot_done`, binary frames are live PTY bytes
- binary frames may split escape sequences arbitrarily; clients must feed bytes into a real terminal emulator rather than parse frame boundaries semantically
- binary frames may be empty in theory but should be ignored in practice

## Client -> Relay Structured Input

Client input is session-scoped by the websocket path, so attach input messages do not include `session_id`.

### `input_text`

Use this for:

- normal typing
- pasted text
- IME-committed text
- local draft text that should not imply submit
- explicit submit actions when the client intends atomic `text + Enter`

```json
{
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
- when `submit` is `true`, the PTY owner must preserve ordering so the PTY receives `text` first and then one trailing carriage return as one serialized operation for that session
- when `submit` is `true`, the PTY owner appends exactly one trailing carriage return (`\r`) beyond the provided text body
- the appended carriage return must match the PTY-owner handling for `input_key("ENTER")`

### `input_key`

Use this for non-text special keys only.

```json
{
  "type": "input_key",
  "key": "TAB"
}
```

### Supported `input_key` Values

The current protocol should support at least:

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

Notes:

- plain text characters such as `"a"` or `"C"` should use `input_text`
- `input_key` is for non-text key semantics only
- modifier combinations such as Ctrl/Alt/Shift shortcuts are out of scope for this protocol revision

## HTTP Error Behavior For Attach

Status behavior for `GET /api/sessions/:id/attach/ws` before websocket upgrade:

- `404 Not Found` with `{"reason":"session_not_found"}` when the session is unknown, belongs to another user, or is currently offline
- `401 Unauthorized` when bearer auth is missing or invalid

## Agent WebSocket

`/agent/ws` is a bidirectional, session-scoped websocket between the relay and the owning `tunnel` process.

It is a mixed websocket:

- control messages are JSON text frames
- client-routed terminal bytes are binary websocket frames

## Agent -> Relay JSON Control Messages

### `register`

```json
{
  "type": "register",
  "session": {
    "session_id": "sess-1",
    "launcher": "codex",
    "cwd": "/repo",
    "command_preview": "codex --profile prod",
    "started_at": 1775376000
  }
}
```

Notes:

- `register` must be the first agent control frame on the websocket
- the relay treats that websocket as the owner of the live session

### `resize`

```json
{
  "type": "resize",
  "cols": 132,
  "rows": 43
}
```

Notes:

- this is session-wide
- the relay forwards it to every currently attached client for that session

### `attach_ready`

```json
{
  "type": "attach_ready",
  "client_id": "4d2c6ec8-787a-49c9-b9a0-5dbd8d31b7b1",
  "cols": 132,
  "rows": 43
}
```

Notes:

- `client_id` is a relay-scoped identifier issued by the relay in `attach_open`
- the relay translates this into the client-facing `attached` control message
- after `attach_ready`, the agent may begin sending snapshot bytes for that `client_id`

### `snapshot_done`

```json
{
  "type": "snapshot_done",
  "client_id": "4d2c6ec8-787a-49c9-b9a0-5dbd8d31b7b1"
}
```

Notes:

- this marks the boundary between snapshot bytes and live bytes for that attached client

### `attach_close`

```json
{
  "type": "attach_close",
  "client_id": "4d2c6ec8-787a-49c9-b9a0-5dbd8d31b7b1",
  "reason": "slow_client"
}
```

Notes:

- the agent uses this when it wants the relay to close one attached client
- known reasons include `slow_client`

## Relay -> Agent JSON Control Messages

### `attach_open`

```json
{
  "type": "attach_open",
  "client_id": "4d2c6ec8-787a-49c9-b9a0-5dbd8d31b7b1"
}
```

Notes:

- the relay sends this after a client successfully upgrades `GET /api/sessions/:id/attach/ws`
- `client_id` is a relay-scoped lowercase UUID string
- the agent should treat `client_id` as opaque

### `attach_close`

```json
{
  "type": "attach_close",
  "client_id": "4d2c6ec8-787a-49c9-b9a0-5dbd8d31b7b1",
  "reason": "client_closed"
}
```

Notes:

- the relay sends this when the attached client socket closes or is no longer usable
- known reasons include `client_closed`

### `input_text`

```json
{
  "type": "input_text",
  "client_id": "4d2c6ec8-787a-49c9-b9a0-5dbd8d31b7b1",
  "text": "hello",
  "submit": false
}
```

### `input_key`

```json
{
  "type": "input_key",
  "client_id": "4d2c6ec8-787a-49c9-b9a0-5dbd8d31b7b1",
  "key": "TAB"
}
```

Notes:

- once the relay chooses the target session, forwarded agent input is the same logical payload as client input, with the addition of relay-issued `client_id`
- the PTY owner may ignore `client_id` for PTY behavior, but it is included for source attribution and future policy control

## Agent -> Relay Binary Packet Format

Binary websocket frames sent from agent to relay carry client-routed terminal bytes.

Packet layout:

```text
+------------+------------------+-------------------+
| 1 byte     | 16 bytes         | remaining bytes   |
| type       | client_id (UUID) | payload           |
+------------+------------------+-------------------+
```

Field semantics:

- `type` is currently:
  - `0x01` = `terminal_bytes`
- `client_id` is the raw 16-byte UUID value corresponding to the lowercase UUID string used in JSON control frames
- `payload` is raw terminal bytes for that attached client

Rules:

- the relay routes the payload to the matching attached client websocket as a binary frame
- the relay must not inspect or transform terminal bytes
- zero-length payloads should be ignored
- binary packets for unknown `client_id` values should be ignored or dropped safely

## Key Mapping Ownership

The relay does not translate `input_key` into PTY bytes.

Instead:

1. the client sends structured input on the session attach websocket
2. the relay validates and forwards it to the owning agent session
3. `tunnel` translates supported `input_key` events into PTY input bytes
4. `tunnel` writes those bytes into the local PTY stdin

This keeps terminal behavior close to the PTY owner and avoids embedding terminal-emulation logic in the relay.

## Size Ownership

- the PTY size follows the local terminal in this revision
- size is communicated to remote clients through `attached` and `resize` control messages
- there is no transcript/frame metadata carrying `cols` and `rows` on every terminal-byte event in this protocol revision
- remote clients should follow resize events rather than attempt to become the PTY size authority

## Client Notes

- clients should use `GET /api/sessions` to discover currently online sessions
- clients should use `GET /api/sessions/:id/attach/ws` as the foreground receive and input channel for one session
- clients should create a fresh terminal emulator state when opening a fresh attach
- clients should size the terminal emulator on `attached` before feeding binary bytes
- clients should treat `snapshot_done` as the boundary after which binary bytes are live PTY output
- the Android client expects a `baseUrl` with an explicit scheme such as `http://...`
- clients may validate relay availability with `GET /api/sessions` or fallback `GET /healthz`

## Invariants

- there is no output-history API in this protocol revision
- reconnect recovery restores the current terminal state, not missed transcript history
- the relay remains content-opaque with respect to PTY output
- the local terminal remains the most complete live session view
- attached clients for the same session observe the same PTY and therefore the same session-wide terminal size

## Related Documents

- [docs/architecture.md](./architecture.md)
- [docs/tui-attach-flow.md](./tui-attach-flow.md)
