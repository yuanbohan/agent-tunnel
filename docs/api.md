# Agent Tunnel Relay API

This document is the source of truth for the relay server's current API surface.

If any relay route, auth requirement, request body, success response, error status or reason, WebSocket message, or attach close reason changes, update this file in the same change.

This file covers:

- public app-facing HTTP endpoints under `/api/...`
- operator-only HTTP endpoints outside `/api/...`
- the client attach WebSocket
- the agent WebSocket

For lower-level wire details such as the binary attach packet format, also see [docs/protocol.md](./protocol.md).

## Conventions

### Base URL

Examples in this document assume a relay base URL such as:

```text
https://diaro.me
```

### Token Types

The relay currently uses three token classes:

| Token | Used By | Where It Goes |
|-------|---------|---------------|
| App access token | mobile/web/native app clients | `Authorization: Bearer <access-token>` on `/api/...` and `GET /api/sessions/:sessionID/attach/ws` |
| App refresh token | app clients | JSON body for `POST /api/auth/refresh` |
| Agent token | `tunnel` | `Authorization: Bearer <agent-token>` on `GET /agent/ws` |

### JSON Request Rules

JSON request bodies are strict:

- maximum body size is 1 MiB
- unknown fields are rejected
- trailing data after the first JSON value is rejected

The relay treats malformed JSON or schema mismatches as `400 {"reason":"invalid_request"}` on JSON endpoints that validate request bodies.

### Timestamps

All JSON timestamps are Unix timestamps in whole seconds.

### Error Body Shapes

The relay currently uses two error styles:

1. Structured JSON API errors:

```json
{
  "reason": "invalid_request"
}
```

2. Plain-text HTTP errors from middleware or generic operational failures, such as:

```text
unauthorized
```

Important current behavior:

- bearer-auth failures return `401 Unauthorized` with `WWW-Authenticate: Bearer realm="tunnel relay"`
- same-origin failures for browser attach dials return plain `403 Forbidden`
- generic unexpected failures currently return plain `500 Internal Server Error`
- missing service configuration currently returns plain `503 Service Unavailable`

### Session Model

- `GET /api/sessions` returns only live sessions whose owning agent socket is currently connected
- session discovery is user-scoped
- a missing session can mean "offline now", not just "never existed"
- a session can disappear and later reappear with the same `session_id` if the same running `tunnel` process reconnects

## Public HTTP API

### `GET /healthz`

Health check for direct relay access.

Auth: none

Success:

- `200 OK`
- body: `ok`

Example:

```text
ok
```

### `POST /api/auth/register`

Create a user account from an invite code.

Auth: none

Request:

```json
{
  "invite_code": "AB2C3D",
  "username": "alice",
  "password": "password123"
}
```

Validation and normalization:

- `username` is trimmed, lowercased, and must be at least 4 characters
- allowed username characters are `a-z`, `0-9`, `_`, `-`, `.`
- `password` must be at least 8 characters
- `invite_code` is trimmed, uppercased, and must be exactly 6 characters from `23456789ABCDEFGHJKMNPQRSTUVWXYZ`
- failed registration attempts are throttled per remote IP

Success:

- `201 Created`

Response:

```json
{
  "user_id": 1,
  "username": "alice"
}
```

Error responses:

| Status | Body | Meaning |
|--------|------|---------|
| `400` | `{"reason":"invalid_request"}` | malformed JSON or request shape |
| `400` | `{"reason":"registration_failed"}` | invalid username, invalid password, invalid invite code, invite not found, invite expired, invite disabled, invite already consumed, or username already taken |
| `429` | `{"reason":"rate_limited"}` | too many failed registration attempts from the same IP |
| `500` | plain text | unexpected server failure |
| `503` | plain text | auth service unavailable |

Headers:

- throttled responses include `Retry-After`

Current throttle behavior:

- default limit is 5 failed attempts per IP in a 10 minute window
- a successful registration resets that IP's failure window

### `POST /api/auth/login`

Exchange username and password for a new app session.

Auth: none

Request:

```json
{
  "username": "alice",
  "password": "password123"
}
```

Success:

- `200 OK`

Response:

```json
{
  "access_token": "<access-token>",
  "refresh_token": "<refresh-token>",
  "expires_in": 900,
  "token_type": "Bearer"
}
```

Notes:

- `expires_in` is currently 900 seconds
- `token_type` is always `Bearer`

Error responses:

| Status | Body | Meaning |
|--------|------|---------|
| `400` | `{"reason":"invalid_request"}` | malformed JSON or request shape |
| `401` | `{"reason":"invalid_credentials"}` | username or password is wrong |
| `500` | plain text | unexpected server failure |
| `503` | plain text | auth service unavailable |

### `POST /api/auth/refresh`

Rotate the current app session using a refresh token.

Auth: none

Request:

```json
{
  "refresh_token": "<refresh-token>"
}
```

Success:

- `200 OK`

Response:

```json
{
  "access_token": "<new-access-token>",
  "refresh_token": "<new-refresh-token>",
  "expires_in": 900,
  "token_type": "Bearer"
}
```

Notes:

- refresh rotates both tokens
- clients should replace both tokens atomically

Error responses:

| Status | Body | Meaning |
|--------|------|---------|
| `400` | `{"reason":"invalid_request"}` | malformed JSON or request shape |
| `401` | `{"reason":"invalid_session"}` | refresh token is unknown, expired, or revoked |
| `500` | plain text | unexpected server failure |
| `503` | plain text | auth service unavailable |

### `POST /api/auth/logout`

Revoke the current app session.

Auth: app access token

Headers:

```text
Authorization: Bearer <access-token>
```

Request body: none

Success:

- `204 No Content`

Notes:

- only the current app session is revoked
- attaches opened by that app session are closed with `closing { "reason": "logged_out" }`
- the owning agent session stays online

Error responses:

| Status | Body | Meaning |
|--------|------|---------|
| `401` | plain text | missing or invalid app bearer token |
| `401` | `{"reason":"invalid_session"}` | session became invalid during logout handling |
| `500` | plain text | unexpected server failure |
| `503` | plain text | auth service unavailable |

### `POST /api/auth/password/change`

Change the authenticated user's password and revoke all app sessions for that user.

Auth: app access token

Headers:

```text
Authorization: Bearer <access-token>
```

Request:

```json
{
  "current_password": "password123",
  "new_password": "betterpass456"
}
```

Success:

- `204 No Content`

Notes:

- `new_password` must meet the current password rules
- all app sessions for that user are revoked
- active attaches for that user are closed with `closing { "reason": "password_changed" }`
- the owning agent session stays online

Error responses:

| Status | Body | Meaning |
|--------|------|---------|
| `400` | `{"reason":"invalid_request"}` | malformed JSON, request shape mismatch, or invalid new password |
| `401` | plain text | missing or invalid app bearer token |
| `401` | `{"reason":"invalid_credentials"}` | `current_password` is wrong |
| `500` | plain text | unexpected server failure |

### `GET /api/agent-tokens`

List the authenticated user's agent tokens.

Auth: app access token

Headers:

```text
Authorization: Bearer <access-token>
```

Request body: none

Success:

- `200 OK`

Response:

```json
[
  {
    "id": "agt_123",
    "name": "MacBook",
    "created_at": 1775376000,
    "last_used_at": 1775377000,
    "revoked_at": 1775378000
  }
]
```

Notes:

- the list is newest-first by `created_at`
- revoked tokens remain listable and are marked with `revoked_at`
- `last_used_at` and `revoked_at` are omitted when unknown or unset

Error responses:

| Status | Body | Meaning |
|--------|------|---------|
| `401` | plain text | missing or invalid app bearer token |
| `500` | plain text | unexpected server failure |
| `503` | plain text | agent token service unavailable |

### `POST /api/agent-tokens`

Create a new agent token for later use by `tunnel`.

Auth: app access token

Headers:

```text
Authorization: Bearer <access-token>
```

Request:

```json
{
  "name": "MacBook"
}
```

Success:

- `201 Created`

Response:

```json
{
  "id": "agt_123",
  "name": "MacBook",
  "created_at": 1775376000,
  "token": "<plaintext-agent-token>"
}
```

Notes:

- `name` must be non-empty after trimming whitespace
- the plaintext `token` is returned only on creation
- store the plaintext token immediately

Error responses:

| Status | Body | Meaning |
|--------|------|---------|
| `400` | `{"reason":"invalid_request"}` | malformed JSON, request shape mismatch, or blank token name |
| `401` | plain text | missing or invalid app bearer token |
| `500` | plain text | unexpected server failure |
| `503` | plain text | agent token service unavailable |

### `DELETE /api/agent-tokens/:tokenID`

Revoke one agent token owned by the authenticated user.

Auth: app access token

Headers:

```text
Authorization: Bearer <access-token>
```

Request body: none

Success:

- `204 No Content`

Notes:

- the relay disconnects live sessions authenticated by that token immediately
- affected attaches are closed with `closing { "reason": "agent_token_revoked" }`

Error responses:

| Status | Body | Meaning |
|--------|------|---------|
| `401` | plain text | missing or invalid app bearer token |
| `404` | `{"reason":"agent_token_not_found"}` | token does not exist for this user or is already revoked |
| `500` | plain text | unexpected server failure |
| `503` | plain text | agent token service unavailable |

### `GET /api/sessions`

List the authenticated user's live sessions.

Auth: app access token

Headers:

```text
Authorization: Bearer <access-token>
```

Request body: none

Success:

- `200 OK`

Response:

```json
[
  {
    "session_id": "sess-1",
    "launcher": "codex",
    "label": "api-fix",
    "cwd": "/repo",
    "command_preview": "codex --profile prod",
    "started_at": 1775376000
  }
]
```

Notes:

- the list is sorted newest-first by `started_at`
- only sessions owned by the authenticated user are returned
- the list is live-only, not history

Error responses:

| Status | Body | Meaning |
|--------|------|---------|
| `401` | plain text | missing or invalid app bearer token |
| `500` | plain text | unexpected server failure |

## Client Attach WebSocket

### `GET /api/sessions/:sessionID/attach/ws`

Attach to one live session owned by the authenticated user.

Auth: app access token

Headers:

```text
Authorization: Bearer <access-token>
```

Browser attach requirement:

- browser clients must present a same-origin `Origin` header for the relay host
- native clients that omit `Origin` are supported

Pre-upgrade error responses:

| Status | Body | Meaning |
|--------|------|---------|
| `401` | plain text | missing or invalid app bearer token |
| `403` | plain text | browser cross-origin attach attempt |
| `404` | `{"reason":"session_not_found"}` | session is unknown, belongs to another user, or is currently offline |

Success:

- HTTP upgrades to WebSocket
- relay sends `attached`, then snapshot bytes, then `snapshot_done`, then live PTY bytes

Relay -> client control message examples:

`attached`

```json
{
  "type": "attached",
  "session_id": "sess-1",
  "cols": 120,
  "rows": 40
}
```

`snapshot_done`

```json
{
  "type": "snapshot_done"
}
```

`resize`

```json
{
  "type": "resize",
  "cols": 100,
  "rows": 30
}
```

`closing`

```json
{
  "type": "closing",
  "reason": "session_offline"
}
```

Current relay-emitted `closing.reason` values:

- `client_closed`
- `session_offline`
- `slow_client`
- `logged_out`
- `password_changed`
- `agent_token_revoked`
- `account_deleted`

Relay -> client binary frames:

- every binary frame carries raw terminal bytes
- bytes before `snapshot_done` are the current-screen snapshot
- bytes after `snapshot_done` are live PTY output
- frame boundaries are not semantic terminal boundaries

Client -> relay input messages:

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
  "key": "TAB"
}
```

Notes:

- input messages do not include `session_id`; the websocket path already scopes the session
- `input_text { "submit": true }` means "text, then one trailing carriage return" as one serialized PTY-owner operation
- `input_key` is only for non-text special keys

Currently supported `input_key` values:

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

For the full attach message contract, see [docs/protocol.md](./protocol.md) and [docs/tui-attach-flow.md](./tui-attach-flow.md).

## Operator HTTP API

Operator routes are intentionally outside `/api/...`.

Current operator constraints:

- request must come from a direct loopback address
- forwarded proxy headers such as `X-Forwarded-For`, `X-Real-IP`, or `Forwarded` cause rejection
- auth uses a fixed bearer token from relay configuration

Shared operator auth failure behavior:

| Status | Body | Meaning |
|--------|------|---------|
| `401` | plain text | missing or invalid operator bearer token |
| `403` | plain text | request is not direct loopback or includes forwarded proxy headers |
| `503` | plain text | operator token or service is unavailable |

### `POST /operator/invite-codes`

Create one or more invite codes.

Auth: operator bearer token, loopback-only

Headers:

```text
Authorization: Bearer <operator-token>
```

Request:

```json
{
  "count": 3,
  "expires_in_days": 7
}
```

Success:

- `201 Created`

Response:

```json
{
  "codes": ["AB2C3D", "EF4G5H", "JK7M8N"]
}
```

Error responses:

| Status | Body | Meaning |
|--------|------|---------|
| `400` | `{"reason":"invalid_request"}` | malformed JSON, request shape mismatch, or non-positive `count` / `expires_in_days` |
| `500` | plain text | unexpected server failure |
| `503` | plain text | operator service unavailable |

### `POST /operator/invite-codes/disable`

Disable one invite code.

Auth: operator bearer token, loopback-only

Request:

```json
{
  "code": "AB2C3D"
}
```

Success:

- `204 No Content`

Error responses:

| Status | Body | Meaning |
|--------|------|---------|
| `400` | `{"reason":"invalid_request"}` | malformed JSON, request shape mismatch, or invalid invite code format |
| `404` | `{"reason":"invite_code_not_found"}` | invite code does not exist |
| `409` | `{"reason":"invite_code_disabled"}` | invite was already disabled |
| `409` | `{"reason":"invite_code_consumed"}` | invite was already used |
| `409` | `{"reason":"invite_code_expired"}` | invite is already expired |
| `500` | plain text | unexpected server failure |
| `503` | plain text | operator service unavailable |

### `POST /operator/users/delete`

Delete one user account.

Auth: operator bearer token, loopback-only

Request:

```json
{
  "username": "alice"
}
```

Success:

- `204 No Content`

Notes:

- the relay disconnects that user's live sessions immediately
- affected attaches are closed with `closing { "reason": "account_deleted" }`

Error responses:

| Status | Body | Meaning |
|--------|------|---------|
| `400` | `{"reason":"invalid_request"}` | malformed JSON, request shape mismatch, or invalid username format |
| `404` | `{"reason":"user_not_found"}` | user does not exist |
| `500` | plain text | unexpected server failure |
| `503` | plain text | operator service unavailable |

## Agent WebSocket

### `GET /agent/ws`

Owning agent connection used by `tunnel`.

Auth: agent token

Headers:

```text
Authorization: Bearer <agent-token>
```

Pre-upgrade error responses:

| Status | Body | Meaning |
|--------|------|---------|
| `401` | plain text | missing or invalid agent bearer token |
| `503` | plain text | agent token service unavailable |

Success:

- HTTP upgrades to WebSocket
- the first agent text frame must be `register`

Initial agent -> relay `register` frame:

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

Current relay -> agent JSON control messages:

- `attach_open`
- `attach_close`
- `input_text`
- `input_key`

Examples:

```json
{
  "type": "attach_open",
  "client_id": "4d2c6ec8-787a-49c9-b9a0-5dbd8d31b7b1"
}
```

```json
{
  "type": "attach_close",
  "client_id": "4d2c6ec8-787a-49c9-b9a0-5dbd8d31b7b1",
  "reason": "client_closed"
}
```

```json
{
  "type": "input_text",
  "client_id": "4d2c6ec8-787a-49c9-b9a0-5dbd8d31b7b1",
  "text": "hello",
  "submit": true
}
```

```json
{
  "type": "input_key",
  "client_id": "4d2c6ec8-787a-49c9-b9a0-5dbd8d31b7b1",
  "key": "TAB"
}
```

Current agent -> relay JSON control messages:

- `resize`
- `attach_ready`
- `snapshot_done`
- `attach_close`

Examples:

```json
{
  "type": "attach_ready",
  "client_id": "4d2c6ec8-787a-49c9-b9a0-5dbd8d31b7b1",
  "cols": 120,
  "rows": 40
}
```

```json
{
  "type": "snapshot_done",
  "client_id": "4d2c6ec8-787a-49c9-b9a0-5dbd8d31b7b1"
}
```

Binary frames sent from agent to relay use the attach packet format:

- `1 byte` packet type
- `16 bytes` raw UUID for `client_id`
- remaining bytes are raw terminal payload

Current packet types:

- `0x01` = `terminal_bytes`

For full binary and frame semantics, see [docs/protocol.md](./protocol.md).

## Removed Endpoints

These older relay surfaces are not part of the current product contract:

- `GET /api/updates/ws`
- `GET /api/sessions/:id/frames`
