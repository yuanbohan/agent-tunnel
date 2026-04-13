# Agent Tunnel Public Relay API

This document is the source of truth for the relay server's current public app-facing API surface.

If any public relay route, app-facing auth requirement, request body, success response, error status or reason, or client attach WebSocket message changes, update this file in the same change.

This file covers:

- public app-facing HTTP endpoints under `/api/...`
- the client attach WebSocket

For lower-level wire details such as the binary attach packet format, also see [docs/protocol.md](./protocol.md).

This document intentionally does not cover operator-only routes or the `tunnel`-owned agent socket at `/agent/ws`.

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
| Agent token | created by app clients, used later by `tunnel` | returned by `POST /api/agent-tokens`; not used by mobile clients for their own relay session |

### JSON Request Rules

JSON request bodies are strict:

- maximum body size is 1 MiB
- unknown fields are rejected
- trailing data after the first JSON value is rejected

The relay treats malformed JSON or schema mismatches as `400` with `{"code":1001,"message":"The request is invalid.","body":null}` on JSON endpoints that validate request bodies.

### Timestamps

All JSON timestamps are Unix timestamps in whole seconds.

### Unified Response Envelope

All app-facing HTTP responses now use this JSON envelope:

```json
{
  "code": 0,
  "message": "success",
  "body": {}
}
```

- `code === 0`: request succeeded
- `code !== 0`: business failure
- on business failures, `body` is always `null`
- HTTP status code still indicates transport outcome (`400`, `401`, `403`, `500`, etc.)

Middleware failures also use this envelope, including auth and rate-limit checks.

Code map (excerpt):

| code | message |
|------|---------|
| `0` | `success` |
| `1001` | `The request is invalid.` |
| `1002` | `Too many requests. Please try again later.` |
| `1003` | `The username is already taken.` |
| `1004` | `The password must be at least 6 characters.` |
| `1005` | `Invalid access code.` |
| `1006` | `This access code is invalid.` |
| `1007` | `This access code has expired.` |
| `1008` | `This access code has been disabled.` |
| `1009` | `This access code has already been used.` |
| `1010` | `The username is invalid.` |
| `1011` | `The username or password is invalid.` |
| `1012` | `The session is invalid.` |
| `1013` | `This agent token was not found.` |
| `1014` | `The user was not found.` |
| `1015` | `The session was not found or is offline.` |
| `1016` | `The request is unauthorized.` |
| `1017` | `The request is forbidden.` |
| `1018` | `The requested endpoint was not found.` |
| `1019` | `The HTTP method is not allowed for this endpoint.` |
| `2001` | `The service is temporarily unavailable.` |
| `2002` | `An unexpected internal error occurred.` |

### Session Model

- `GET /api/sessions` returns only live sessions whose owning agent socket is currently connected
- session discovery is user-scoped
- user-scoped discovery and attach authorization are a hard multi-tenant guarantee for hosted relay deployments
- a missing session can mean "offline now", not just "never existed"
- a session can disappear and later reappear with the same `session_id` if the same running `tunnel` process reconnects

## Public HTTP API

### `GET /healthz`

Health check for direct relay access.

Auth: none

Success:

- `200 OK`
- body (envelope):

Example:

```json
{
  "code": 0,
  "message": "success",
  "body": {
    "status": "ok"
  }
}
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
- `password` must be at least 6 characters
- `invite_code` is trimmed, uppercased, and must be exactly 6 characters from `23456789ABCDEFGHJKMNPQRSTUVWXYZ`
- failed registration attempts are throttled per remote IP

Success:

- `201 Created`
- body (envelope):

Response:

```json
{
  "code": 0,
  "message": "success",
  "body": {
    "user_id": 1,
    "username": "alice"
  }
}
```

Error responses:

| Status | Body | Meaning |
|--------|------|---------|
| `400` | `{"code":1001,"message":"The request is invalid.","body":null}` | malformed JSON or request shape |
| `400` | `{"code":1010,"message":"The username is invalid.","body":null}` | username is missing, too short, or contains invalid characters |
| `400` | `{"code":1004,"message":"The password must be at least 6 characters.","body":null}` | password is missing or shorter than 6 characters |
| `400` | `{"code":1005,"message":"Invalid access code.","body":null}` | `invite_code` is malformed |
| `400` | `{"code":1006,"message":"This access code is invalid.","body":null}` | invite code does not exist |
| `400` | `{"code":1007,"message":"This access code has expired.","body":null}` | invite code is expired |
| `400` | `{"code":1008,"message":"This access code has been disabled.","body":null}` | invite code was disabled |
| `400` | `{"code":1009,"message":"This access code has already been used.","body":null}` | invite code was already used |
| `400` | `{"code":1003,"message":"The username is already taken.","body":null}` | username already exists |
| `429` | `{"code":1002,"message":"Too many requests. Please try again later.","body":null}` | too many failed registration attempts from the same IP |
| `500` | `{"code":2002,"message":"An unexpected internal error occurred.","body":null}` | unexpected server failure |
| `503` | `{"code":2001,"message":"The service is temporarily unavailable.","body":null}` | service unavailable |

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
- body (envelope):

Response:

```json
{
  "code": 0,
  "message": "success",
  "body": {
    "access_token": "<access-token>",
    "refresh_token": "<refresh-token>",
    "expires_in": 86400,
    "token_type": "Bearer"
  }
}
```

Notes:

- `expires_in` is currently up to 86400 seconds (24 hours)
- each app session also has a 90 day absolute lifetime measured from the original login
- `token_type` is always `Bearer`

Error responses:

| Status | Body | Meaning |
|--------|------|---------|
| `400` | `{"code":1001,"message":"The request is invalid.","body":null}` | malformed JSON or request shape |
| `401` | `{"code":1011,"message":"The username or password is invalid.","body":null}` | username or password is wrong |
| `500` | `{"code":2002,"message":"An unexpected internal error occurred.","body":null}` | unexpected server failure |
| `503` | `{"code":2001,"message":"The service is temporarily unavailable.","body":null}` | auth service unavailable |

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
- body (envelope):

Response:

```json
{
  "code": 0,
  "message": "success",
  "body": {
    "access_token": "<new-access-token>",
    "refresh_token": "<new-refresh-token>",
    "expires_in": 86400,
    "token_type": "Bearer"
  }
}
```

Notes:

- refresh rotates both tokens
- clients should replace both tokens atomically
- each successful refresh extends the refresh token for another 30 days from that refresh time, unless the session hits the 90 day absolute lifetime from the original login
- `expires_in` is currently up to 86400 seconds (24 hours) and may be shorter near the 90 day absolute session boundary

Error responses:

| Status | Body | Meaning |
|--------|------|---------|
| `400` | `{"code":1001,"message":"The request is invalid.","body":null}` | malformed JSON or request shape |
| `401` | `{"code":1012,"message":"The session is invalid.","body":null}` | refresh token is unknown, expired, revoked, or the session has reached its 90 day absolute lifetime |
| `500` | `{"code":2002,"message":"An unexpected internal error occurred.","body":null}` | unexpected server failure |
| `503` | `{"code":2001,"message":"The service is temporarily unavailable.","body":null}` | auth service unavailable |

### `POST /api/auth/logout`

Revoke the current app session.

Auth: app access token

Headers:

```text
Authorization: Bearer <access-token>
```

Request body: none

Success:

- `200 OK` (body is `null`)
- body (envelope):

```json
{
  "code": 0,
  "message": "success",
  "body": null
}
```

Notes:

- only the current app session is revoked
- attaches opened by that app session are closed with `closing { "reason": "logged_out" }`
- the owning agent session stays online

Error responses:

| Status | Body | Meaning |
|--------|------|---------|
| `401` | `{"code":1016,"message":"The request is unauthorized.","body":null}` | missing or invalid app bearer token |
| `401` | `{"code":1012,"message":"The session is invalid.","body":null}` | session became invalid during logout handling |
| `500` | `{"code":2002,"message":"An unexpected internal error occurred.","body":null}` | unexpected server failure |
| `503` | `{"code":2001,"message":"The service is temporarily unavailable.","body":null}` | auth service unavailable |

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

- `200 OK` (body is `null`)
- body (envelope):

```json
{
  "code": 0,
  "message": "success",
  "body": null
}
```

Notes:

- `new_password` must be at least 6 characters
- all app sessions for that user are revoked
- active attaches for that user are closed with `closing { "reason": "password_changed" }`
- the owning agent session stays online

Error responses:

| Status | Body | Meaning |
|--------|------|---------|
| `400` | `{"code":1001,"message":"The request is invalid.","body":null}` | malformed JSON or request shape mismatch |
| `400` | `{"code":1004,"message":"The password must be at least 6 characters.","body":null}` | new password is missing or shorter than 6 characters |
| `401` | `{"code":1016,"message":"The request is unauthorized.","body":null}` | missing or invalid app bearer token |
| `401` | `{"code":1011,"message":"The username or password is invalid.","body":null}` | `current_password` is wrong |
| `500` | `{"code":2002,"message":"An unexpected internal error occurred.","body":null}` | unexpected server failure |

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
- body (envelope):

Response:

```json
{
  "code": 0,
  "message": "success",
  "body": [
    {
      "id": "agt_123",
      "name": "MacBook",
      "created_at": 1775376000,
      "last_used_at": 1775377000,
      "revoked_at": 1775378000
    }
  ]
}
```

Notes:

- the list is newest-first by `created_at`
- revoked tokens remain listable and are marked with `revoked_at`
- `last_used_at` and `revoked_at` are omitted when unknown or unset

Error responses:

| Status | Body | Meaning |
|--------|------|---------|
| `401` | `{"code":1016,"message":"The request is unauthorized.","body":null}` | missing or invalid app bearer token |
| `500` | `{"code":2002,"message":"An unexpected internal error occurred.","body":null}` | unexpected server failure |
| `503` | `{"code":2001,"message":"The service is temporarily unavailable.","body":null}` | agent token service unavailable |

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
- body (envelope):

Response:

```json
{
  "code": 0,
  "message": "success",
  "body": {
    "id": "agt_123",
    "name": "MacBook",
    "created_at": 1775376000,
    "token": "<plaintext-agent-token>"
  }
}
```

Notes:

- `name` must be non-empty after trimming whitespace
- the plaintext `token` is returned only on creation
- store the plaintext token immediately

Error responses:

| Status | Body | Meaning |
|--------|------|---------|
| `400` | `{"code":1001,"message":"The request is invalid.","body":null}` | malformed JSON, request shape mismatch, or blank token name |
| `401` | `{"code":1016,"message":"The request is unauthorized.","body":null}` | missing or invalid app bearer token |
| `500` | `{"code":2002,"message":"An unexpected internal error occurred.","body":null}` | unexpected server failure |
| `503` | `{"code":2001,"message":"The service is temporarily unavailable.","body":null}` | agent token service unavailable |

### `DELETE /api/agent-tokens/:tokenID`

Revoke one agent token owned by the authenticated user.

Auth: app access token

Headers:

```text
Authorization: Bearer <access-token>
```

Request body: none

Success:

- `200 OK` (body is `null`)
- body (envelope):

```json
{
  "code": 0,
  "message": "success",
  "body": null
}
```

Notes:

- the relay disconnects live sessions authenticated by that token immediately
- affected attaches are closed with `closing { "reason": "agent_token_revoked" }`

Error responses:

| Status | Body | Meaning |
|--------|------|---------|
| `401` | `{"code":1016,"message":"The request is unauthorized.","body":null}` | missing or invalid app bearer token |
| `404` | `{"code":1013,"message":"This agent token was not found.","body":null}` | token does not exist for this user or is already revoked |
| `500` | `{"code":2002,"message":"An unexpected internal error occurred.","body":null}` | unexpected server failure |
| `503` | `{"code":2001,"message":"The service is temporarily unavailable.","body":null}` | agent token service unavailable |

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
- body (envelope):

Response:

```json
{
  "code": 0,
  "message": "success",
  "body": [
    {
      "session_id": "sess-1",
      "launcher": "codex",
      "label": "api-fix",
      "cwd": "/repo",
      "command_preview": "codex --profile prod",
      "started_at": 1775376000
    }
  ]
}
```

Notes:

- the list is sorted newest-first by `started_at`
- only sessions owned by the authenticated user are returned
- another user's live sessions must remain invisible even when both users have active `tunnel` connections
- the list is live-only, not history

Error responses:

| Status | Body | Meaning |
|--------|------|---------|
| `401` | `{"code":1016,"message":"The request is unauthorized.","body":null}` | missing or invalid app bearer token |
| `500` | `{"code":2002,"message":"An unexpected internal error occurred.","body":null}` | unexpected server failure |

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
| `401` | `{"code":1016,"message":"The request is unauthorized.","body":null}` | missing or invalid app bearer token |
| `403` | `{"code":1017,"message":"The request is forbidden.","body":null}` | browser cross-origin attach attempt |
| `404` | `{"code":1015,"message":"The session was not found or is offline.","body":null}` | session is unknown, belongs to another user, or is currently offline |

Multi-tenant rule:

- cross-user attach attempts must fail as `404` with `code=1015` and must not leak whether another user's session is online

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
- bytes before `snapshot_done` are the fresh terminal-state snapshot and may include bounded agent-local normal-buffer scrollback ahead of the current viewport
- bytes after `snapshot_done` are live PTY output
- frame boundaries are not semantic terminal boundaries
- clients should keep local terminal scrollback enabled if they want those replayed snapshot lines to remain available after restore

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

## Removed Endpoints

These older relay surfaces are not part of the current product contract:

- `GET /api/updates/ws`
- `GET /api/sessions/:id/frames`
