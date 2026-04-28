# Agent Tunnel Relay Protocol

This document describes the current relay-facing contract for clients and agents.

It supersedes the older replay/frame model. Clients should build against this attach-based protocol, not against `/api/updates/ws`, `/api/sessions/:id/frames`, `ReplayFrame`, or `latest_seq`.

For endpoint-level request and response examples, auth requirements, and error contracts, see [docs/api.md](./api.md).

## Core Model

The current protocol is built around these boundaries:

- `session_id` identifies one running `tunnel` process. Relay reconnects for that process keep the same `session_id`. A fresh agent launch gets a fresh `session_id`.
- `device_id` identifies one machine-local daemon identity. Device reconnects and daemon restarts keep the same `device_id` for that machine-local daemon state.
- The owning agent is the authority for the current terminal state of that session.
- The owning device daemon is the authority for whether that machine is currently launchable and for how new remote-launch sessions are created inside its dedicated tmux workspace.
- The relay is a discovery, auth, and routing layer. It does not retain transcript history and does not emulate the terminal.
- Hosted relay deployments rely on strict multi-tenant isolation: sessions are owned by the user behind the authenticating agent token, and other users must not discover or attach to them.
- Sessions are discoverable only while the owning agent websocket is connected. If the agent disconnects, the session disappears from discovery immediately and reappears when the agent re-registers with the same `session_id`.
- Remote viewing is session-scoped: a client attaches to one session, receives a fresh terminal-state snapshot, and then receives subsequent live PTY bytes on that same attach.
- Remote recovery in this revision is fresh snapshot recovery only. Snapshot bytes may include up to 10,000 lines of bounded agent-local normal-buffer scrollback, but there is no transcript replay API.
- The local terminal remains the most complete and authoritative foreground view of the PTY session.

All protocol timestamps are Unix timestamps represented as JSON integers in seconds.

## Endpoint Inventory

| Endpoint | Role | Auth | Kind | Purpose |
|----------|------|------|------|---------|
| `GET /healthz` | Health probe | None | HTTP | Health check for relay reachability through direct access or the public nginx front door |
| `GET /api/devices` | Client | Bearer | HTTP | Current live device snapshot for the authenticated user |
| `POST /api/devices/:deviceID/launch` | Client | Bearer | HTTP | Ask one currently online device daemon to launch `tunnel run <command>` and wait for the resulting session to become `session_ready` |
| `GET /api/sessions` | Client | Bearer | HTTP | Current live session snapshot for the authenticated user |
| `POST /api/sessions/:id/stop` | Client | Bearer | HTTP | Ask the owning online agent to stop one live session |
| `GET /api/sessions/:id/attach/ws` | Client | Bearer | WebSocket | Attach to one live session owned by the authenticated user for snapshot, live bytes, resize events, and session-scoped structured input |
| `GET /agent/ws` | Agent | Bearer | WebSocket | Agent registration, attach control, resize metadata, structured input forwarding, and client-routed terminal byte delivery |
| `GET /device/ws` | Device daemon | Bearer | WebSocket | Device registration plus launch request/result routing for one online machine |

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
  "device_id": "dev_abcd1234",
  "launcher": "codex",
  "label": "api-fix",
  "cwd": "/repo",
  "command_preview": "codex --profile prod",
  "git_branch": "main",
  "started_at": 1775376000,
  "platform_family": "linux",
  "platform_id": "ubuntu",
  "computer_name": "Office Linux",
  "launch_source": "mobile"
}
```

Notes:

- `label` may be omitted when empty
- `device_id` is copied from the registering session when the local `tunnel run` can read an existing daemon identity; otherwise it is an empty string
- `started_at` is a Unix timestamp in seconds
- `git_branch` is the best-effort Git branch for `cwd`; when the startup directory is not on a symbolic branch it is returned as an empty string
- `platform_family`, `platform_id`, and `computer_name` are stable keys in the session payload; when metadata is unavailable they are returned as empty strings rather than omitted
- `platform_family` is the coarse fallback field for session device identity, currently `macos` or `linux`
- `platform_id` is a best-effort specific identifier for client-side icon mapping
- `computer_name` is the user-facing machine name chosen by the agent: display name first, hostname as fallback
- `launch_source` is `local` or `mobile`; the relay sets `mobile` only when session registration is correlated with a mobile/device launch request
- clients should treat missing, empty, or unknown `launch_source` values as `local`
- every returned session currently has an owning agent websocket and is attachable

`GET /api/devices` returns the standard API envelope whose `body` is an array of `DeviceInfo` objects:

```json
{
  "code": 0,
  "message": "success",
  "body": [
    {
      "device_id": "dev_abcd1234",
      "display_name": "Yuanbo's MacBook Pro",
      "platform_family": "macos",
      "platform_id": "macos",
      "launch_health": "healthy"
    }
  ]
}
```

Notes:

- `platform_family` is the stable coarse UI fallback field, currently `macos` or `linux`
- `platform_id` is a best-effort specific identifier for exact icon mapping
- `launch_health` is the daemon-reported live readiness for remote launch, currently `healthy` or `degraded`
- device presence is live-only; if the device daemon disconnects, the device disappears from discovery immediately

## Attach Lifecycle

`GET /api/sessions/:id/attach/ws` is the client-facing session attach websocket.

The attach contract is:

1. the client authenticates and opens `GET /api/sessions/:id/attach/ws`
2. the relay verifies that the session currently exists in discovery for the authenticated user
3. the relay allocates a relay-scoped `client_id` and sends `attach_open` to the owning agent
4. the agent atomically:
   - captures the current terminal size
   - serializes the current terminal state into snapshot bytes, including up to 10,000 lines of bounded normal-buffer scrollback when the mirror still has it
   - maps any still-valid submit anchors into the serialized snapshot buffer coordinates
   - registers that attached client for subsequent live-byte and live submit-anchor delivery
5. the agent sends `attach_ready`
6. the agent sends snapshot bytes
7. the agent sends `snapshot_done`, optionally carrying bounded submit anchors
8. subsequent terminal byte packets on that attach are live PTY bytes
9. subsequent `submit_anchor` controls on that attach are live submit-anchor updates from the same Tunnel-owned anchor index

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
  "type": "snapshot_done",
  "submit_anchors": [
    {
      "id": "submit-1",
      "line": 42,
      "submitted_at": 1775131200
    }
  ]
}
```

Notes:

- this marks the end of the initial current-state snapshot
- the snapshot phase may include up to 10,000 lines of bounded agent-local normal-buffer scrollback ahead of the current viewport
- `submit_anchors` is optional and omitted when no valid anchors are available
- each submit anchor is a local or remote `ENTER` submit-position hint outside a bracketed-paste region, not a guarantee of the exact Codex-rendered user-message block
- `id` is an opaque session-local identifier that is stable only while the running agent retains that anchor; clients must not treat it as durable across process exit, a new `session_id`, or anchor expiry
- `line` is a 0-based row in the terminal buffer after the client applies the snapshot bytes that preceded this `snapshot_done`
- `submitted_at` is a Unix timestamp encoded as JSON integer seconds
- submit anchors are agent-local, bounded to at most 256 valid entries, non-durable, and omitted when they no longer map into the retained snapshot context
- after this point, all subsequent binary frames are live PTY bytes
- binary bytes before `snapshot_done` and after `snapshot_done` should both be fed into the same terminal emulator in arrival order
- clients should ignore unknown control-message fields for forward compatibility

#### `submit_anchor`

```json
{
  "type": "submit_anchor",
  "submit_anchor": {
    "id": "submit-2",
    "line": 45,
    "submitted_at": 1775131300
  }
}
```

Notes:

- this is a live incremental event for already attached clients after a local or remote input write sends an `ENTER` carriage return outside a bracketed-paste region
- `submit_anchor` uses the same content-free shape, opaque session-local id, and non-durable Tunnel-owned retention model as `snapshot_done.submit_anchors`
- `line` is a 0-based row in the attached client's current terminal buffer at the point this control is received, not a snapshot-relative row from a past attach
- clients should reconcile any missed live events or local provisional dots from the next fresh `snapshot_done.submit_anchors` after reconnect

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
- `agent_token_revoked`
- `account_deleted`

Notes:

- the relay should send `closing` before it closes the websocket when a reason is known
- clients should key off the `reason` value, not off a particular websocket close code
- `logged_out` means the current app session was explicitly logged out; the agent session may still be online
- `password_changed` means the user's app sessions were revoked after a password change; the agent session may still be online
- `agent_token_revoked` means the owning agent token was revoked and the session was disconnected
- `account_deleted` means the user account was deleted by an operator

### Relay -> Client Binary Frames

Each binary websocket frame carries raw terminal bytes.

Rules:

- the first binary frames after `attached` are the serialized snapshot bytes and may include up to 10,000 lines of bounded agent-local normal-buffer scrollback
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
- the trailing carriage return can create a bounded submit anchor for future fresh attaches and live `submit_anchor` controls for currently attached clients
- if the provided `text` itself contains a carriage return (`\r`) outside a bracketed-paste region, that byte is also Enter-bearing PTY input and can create a bounded submit anchor even when `submit` is `false`

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
- `input_key { "key": "ENTER" }` sends the same carriage return as `input_text { "submit": true }` and can create a bounded submit anchor for snapshots and live attached clients
- modifier combinations such as Ctrl/Alt/Shift shortcuts are out of scope for this protocol revision

## HTTP Error Behavior For Attach

Status behavior for `GET /api/sessions/:id/attach/ws` before websocket upgrade:

- `404 Not Found` with `{"reason":"session_not_found"}` when the session is unknown, belongs to another user, or is currently offline
- `401 Unauthorized` when bearer auth is missing or invalid

## Device WebSocket

`/device/ws` is a bidirectional websocket between the relay and one running `tunnel daemon` process.

The first frame must be:

```json
{
  "type": "register",
  "device": {
    "device_id": "dev_abcd1234",
    "display_name": "Yuanbo's MacBook Pro",
    "platform_family": "macos",
    "platform_id": "macos"
  }
}
```

Launch request relay-to-device:

```json
{
  "type": "launch_request",
  "request_id": "dev_abcd1234-150405.000000000",
  "command": "codex --profile prod",
  "cwd": "/repo",
  "label": "api-fix"
}
```

Notes:

- `cwd` is required on every launch request
- `label` is optional

Launch result device-to-relay after immediate daemon-side validation:

```json
{
  "type": "launch_result",
  "request_id": "dev_abcd1234-150405.000000000",
  "status": "failed",
  "reason": "busy"
}
```

or

```json
{
  "type": "launch_result",
  "request_id": "dev_abcd1234-150405.000000000",
  "status": "accepted",
  "workspace_session": "launch_abcd1234"
}
```

Notes:

- `request_id` is relay-scoped and correlates one launch request with one result
- `status: "accepted"` means the daemon validated the request and successfully created the local tmux-backed launch session
- accepted launch results may include `workspace_session` as daemon-local metadata, but session stop is routed to the owning agent through `/agent/ws`
- `status: "accepted"` still does not complete the client-visible launch flow; the relay waits for a later `/agent/ws` registration carrying a matching launch context
- the relay keeps only transient online-device routing state; device health, last failure, and tmux workspace details remain local to the daemon

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
  "launch_context": {
    "source": "mobile",
    "request_id": "dev_abcd1234-150405.000000000"
  },
  "session": {
    "session_id": "sess-1",
    "device_id": "dev_abcd1234",
    "launcher": "codex",
    "label": "api-fix",
    "cwd": "/repo",
    "command_preview": "codex --profile prod",
    "git_branch": "main",
    "started_at": 1775376000,
    "platform_family": "linux",
    "platform_id": "ubuntu",
    "computer_name": "Office Linux"
  }
}
```

Notes:

- `register` must be the first agent control frame on the websocket
- the relay treats that websocket as the owner of the live session
- `launch_context` is optional; it is present only when this session was created from `POST /api/devices/:deviceID/launch`
- `launch_context.source` describes the launch source claimed by the registering `tunnel run`; currently the only non-local value is `mobile`
- `launch_context.request_id` is the relay-issued launch correlation id, not user-visible source metadata
- the relay stores `session.device_id` from the registration payload without launch-request validation; agents send an empty string when local daemon identity is unavailable
- the relay marks a live session with `launch_source: "mobile"` only when `launch_context.source` is `mobile` and `launch_context.request_id` matches a pending launch request owned by the same user and agent token
- missing, empty, unknown, or unmatched launch context values are treated as local launch source
- session metadata is self-contained on registration; clients should not infer session platform identity by correlating later launch state or online device listings

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
  "client_id": "4d2c6ec8-787a-49c9-b9a0-5dbd8d31b7b1",
  "submit_anchors": [
    {
      "id": "submit-1",
      "line": 42,
      "submitted_at": 1775131200
    }
  ]
}
```

Notes:

- this marks the boundary between snapshot bytes and live bytes for that attached client
- `submit_anchors` is optional, content-free metadata generated by the owning agent from local or remote input that sends an `ENTER` carriage return outside a bracketed-paste region
- the relay forwards valid anchors to the attached client without inspecting terminal bytes or storing transcript history, omitting invalid entries and capping the list at 256 entries

### `submit_anchor`

```json
{
  "type": "submit_anchor",
  "client_id": "4d2c6ec8-787a-49c9-b9a0-5dbd8d31b7b1",
  "submit_anchor": {
    "id": "submit-2",
    "line": 45,
    "submitted_at": 1775131300
  }
}
```

Notes:

- this routes one live submit anchor to one already attached client
- the relay forwards a valid anchor only when the websocket still owns the session and the target `client_id` is currently attached
- invalid anchors are dropped rather than forwarded

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
- known reasons include `client_closed`, `slow_client`, `logged_out`, `password_changed`, `agent_token_revoked`, and `account_deleted`

### `stop_session`

```json
{
  "type": "stop_session"
}
```

Notes:

- the relay sends this to the owning `/agent/ws` connection after `POST /api/sessions/:id/stop` is accepted
- the agent should stop the local `tunnel run` session and exit
- the relay removes the session from discovery and closes active attaches with `session_stopped`

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
- clients should interpret any `snapshot_done.submit_anchors[].line` only after applying the preceding snapshot bytes
- clients should interpret any live `submit_anchor.line` against the terminal buffer state current when that control is received, then reconcile from the next fresh snapshot after reconnect
- the Android client expects a `baseUrl` with an explicit scheme such as `http://...`
- clients may validate relay availability with `GET /api/sessions` or fallback `GET /healthz`

## Invariants

- there is no output-history API in this protocol revision
- reconnect recovery restores the current terminal state plus up to 10,000 lines of bounded agent-local normal-buffer scrollback when available, not missed transcript history
- submit anchors are bounded agent-local navigation metadata, not transcript history or TUI semantic parsing
- the relay remains content-opaque with respect to PTY output
- the local terminal remains the most complete live session view
- attached clients for the same session observe the same PTY and therefore the same session-wide terminal size

## Related Documents

- [docs/api.md](./api.md)
- [docs/architecture.md](./architecture.md)
- [docs/tui-attach-flow.md](./tui-attach-flow.md)
