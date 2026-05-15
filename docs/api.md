# Agent Tunnel Public Relay API

This document is the repo-local implementation reference for the relay server's current public app-facing API surface. Cross-repository protocol decisions live in [yuanbohan/agent-tunnel-protocols](https://github.com/yuanbohan/agent-tunnel-protocols); keep this file aligned with that protocol source of truth.

If any public relay route, app-facing auth requirement, request body, success response, error status or reason, or client attach WebSocket message changes, update this file in the same change.

This file covers:

- public app-facing HTTP endpoints under `/api/...`
- the client attach WebSocket
- the connectivity app/daemon WebSockets and fallback tunnel endpoint

For lower-level wire details such as the binary attach packet format, also see [docs/protocol.md](./protocol.md).

This document intentionally does not cover operator-only routes or the legacy `tunnel`-owned agent sockets at `/agent/ws` and `/device/ws`.

## Official Mobile Companion Boundary

The official mobile companion uses Relay for auth, account policy, pairing, computer presence, rendezvous, fallback tunnel setup, and computer launch. After a launch returns `session_ready`, the companion treats `session_id` as a correlation key and waits for the daemon connectivity transport to report that session through `session_index` or `session_upsert`.

The retained Relay session APIs (`GET /api/sessions`, `DELETE /api/sessions/:sessionID`, and `GET /api/sessions/:sessionID/attach/ws`) remain documented below for classic Relay attach clients and account-level live-session operations. They are not the official mobile companion authority for session roster, previews, terminal snapshots/live bytes, input, resize, or mobile session detail after launch.

## Conventions

### Base URL

Examples in this document assume a relay base URL such as:

```text
https://agentunnel.cn
```

### Token Types

The relay currently uses three token classes:

| Token | Used By | Where It Goes |
|-------|---------|---------------|
| App access token | mobile/web/native app clients, including retained classic attach clients | `Authorization: Bearer <access-token>` on app-facing `/api/...` routes, retained `GET /api/sessions/:sessionID/attach/ws`, and `GET /api/connectivity/ws` |
| App refresh token | app clients | JSON body for `POST /api/auth/refresh` |
| Agent token | created by app clients, used later by `tunnel` | returned by `POST /api/agent-tokens`; used by `/agent/ws`, `/device/ws`, `/connectivity/computer/ws`, `GET /api/sessions`, and `DELETE /api/sessions/:sessionID` |
| Fallback tunnel token | app and daemon connectivity peers | one-time `Authorization: Bearer <single-use-token>` on `GET /connectivity/tunnel/ws`; issued through `relay_tunnel_ready` and bound to one actor and attempt |

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
| `1020` | `The client fingerprint is invalid.` |
| `2001` | `The service is temporarily unavailable.` |
| `2002` | `An unexpected internal error occurred.` |

### Session Model

This section describes the retained Relay session API model for classic attach clients and account-level live-session operations. The official mobile companion uses daemon connectivity transport session metadata after launch.

- `GET /api/sessions` returns only live sessions whose owning agent socket is currently connected
- session discovery is user-scoped
- user-scoped discovery and attach authorization are a hard multi-tenant guarantee for hosted relay deployments
- a missing session can mean "offline now", not just "never existed"
- a session can disappear and later reappear with the same `session_id` if the same running `tunnel` process reconnects
- session metadata now includes best-effort Git branch for the startup `cwd`, optional daemon identity through `device_id`, relay-controlled `launch_source`, and best-effort machine identity fields for UI display: `platform_family`, `platform_id`, and `computer_name`
- `launch_source` is `local` for direct local `tunnel run` sessions and `mobile` for sessions whose registration is correlated with a mobile/device launch request
- clients should treat missing, empty, or unknown `launch_source` values as `local`
- `git_branch` is the Git branch for the registered startup `cwd` when that directory is on a symbolic branch; otherwise it is an empty string
- `device_id` is copied from the registering session when that local `tunnel run` can read an existing daemon identity; otherwise it is an empty string
- `computer_name` is already normalized by the agent before registration: prefer local display name when available, otherwise fall back to hostname
- `platform_id` is a raw best-effort identifier intended for client-side icon mapping; clients should keep their own whitelist and fall back gracefully for unknown values

### Computer Model

- `GET /api/computers` returns only currently connected computers whose owning daemon socket is online now
- computer discovery is user-scoped, just like session discovery
- computer identity is stable through `computer_id`; display metadata such as `display_name`, `platform_family`, and `platform_id` are refreshed when the daemon re-registers
- computer presence is live-only; there is no offline or historical computer list in this API revision
- `POST /api/computers/:computerID/sessions` reports `session_ready` or a structured launch failure and does not auto-attach to the later session
- for the official mobile companion, `session_ready.session_id` is followed by daemon connectivity transport convergence; Relay does not become the mobile session roster/detail/interactive authority
- `DELETE /api/sessions/:sessionID` can stop any live session owned by the authenticated user as a retained Relay/account-level operation

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

### `GET /api/computers`

List currently online computers for the authenticated user.

Auth: app bearer token

Compatibility note: `GET /api/devices` remains available as a legacy alias in this revision and returns the same live computers using the legacy `device_id` response key.

Success:

- `200 OK`

Response:

```json
{
  "code": 0,
  "message": "success",
  "body": [
    {
      "computer_id": "dev_abcd1234",
      "display_name": "Yuanbo's MacBook Pro",
      "platform_family": "macos",
      "platform_id": "macos",
      "launch_health": "healthy"
    }
  ]
}
```

Notes:

- `computer_id` is the stable daemon identity; sessions whose `device_id` reports the same value in `GET /api/sessions` are associated with that computer
- `platform_family` is the stable UI fallback field for device-class icons, currently `macos` or `linux`
- `platform_id` is the best-effort specific platform identifier for more exact icon selection, for example `macos`, `ubuntu`, `arch`, `debian`, `fedora`, or `unknown`
- `launch_health` is the daemon-reported live readiness for remote launch, currently `healthy` or `degraded`
- the relay returns only computers whose `/device/ws` connection is online right now

### `POST /api/computers/:computerID/sessions`

Ask one currently online device to create a new session inside its daemon-managed tmux workspace and run `tunnel run <command>`.

Auth: app bearer token

Compatibility note: `POST /api/devices/:deviceID/launch` remains available as a legacy alias in this revision.

Request:

```json
{
  "command": "codex --profile prod",
  "cwd": "/repo",
  "label": "api-fix"
}
```

Request fields:

- `command` is required
- `cwd` is required and is the working directory used on the target machine before `tunnel run <command>` starts
- `label` is optional and, when present, is forwarded to the created session's normal session metadata

Success response always uses the standard success envelope. The launch result is carried in `body`.

When the launch completes successfully, success means `session_ready`: the newly started `tunnel` process has registered with the relay, its local daemon broker registration has succeeded, its PTY process has started, and it now has a concrete `session_id`.

Successful body:

```json
{
  "code": 0,
  "message": "success",
  "body": {
    "request_id": "dev_abcd1234-150405.000000000",
    "status": "session_ready",
    "session_id": "sess-1"
  }
}
```

Failure body:

```json
{
  "code": 0,
  "message": "success",
  "body": {
    "request_id": "dev_abcd1234-150405.000000000",
    "status": "failed",
    "reason": "launch_timeout"
  }
}
```

Known `reason` values in this revision:

- `device_offline`
- `busy`
- `command_not_allowed`
- `tmux_not_found`
- `session_start_failed`
- `tunnel_not_found`
- `path_not_found`
- `launch_timeout`

Notes:

- the relay may hold this request open for roughly 20-30 seconds while waiting for the launched session to become ready
- `status: "session_ready"` is the only success state in this contract
- a successful daemon-launched session still registers with Relay for retained classic/account-level live-session APIs; its `device_id` is whatever the launched `tunnel run` reports from local daemon state
- official mobile companion clients should treat `session_id` from the launch response as a launch correlation key and wait for the daemon connectivity transport to report the matching session through `session_index` or `session_upsert`
- retained classic Relay attach clients may use `session_id` with the Relay session discovery and attach flow
- the launch flow still does not auto-attach to the new session

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
  "password": "password123",
  "client_fingerprint": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
}
```

`client_fingerprint` is optional for legacy clients. Connectivity-capable app clients send a lower- or upper-case 64 character hex SHA-256 fingerprint of their client public key. The legacy `device_fingerprint` request key is still accepted as an alias in this revision. Fingerprint-bound sessions must refresh with the same fingerprint.

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
    "token_type": "Bearer",
    "account_id": "123"
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
| `400` | `{"code":1020,"message":"The client fingerprint is invalid.","body":null}` | `client_fingerprint` or legacy `device_fingerprint` is present but not 64 hex characters |
| `401` | `{"code":1011,"message":"The username or password is invalid.","body":null}` | username or password is wrong |
| `500` | `{"code":2002,"message":"An unexpected internal error occurred.","body":null}` | unexpected server failure |
| `503` | `{"code":2001,"message":"The service is temporarily unavailable.","body":null}` | auth service unavailable |

### `POST /api/auth/refresh`

Rotate the current app session using a refresh token.

Auth: none

Request:

```json
{
  "refresh_token": "<refresh-token>",
  "client_fingerprint": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
}
```

`client_fingerprint` is required when the original login bound the app session to a fingerprint. The legacy `device_fingerprint` request key is still accepted as an alias in this revision. Refresh with a different fingerprint is rejected as an invalid session.

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
    "token_type": "Bearer",
    "account_id": "123"
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
| `400` | `{"code":1020,"message":"The client fingerprint is invalid.","body":null}` | `client_fingerprint` or legacy `device_fingerprint` is present but not 64 hex characters |
| `401` | `{"code":1012,"message":"The session is invalid.","body":null}` | refresh token is unknown, expired, revoked, fingerprint-mismatched, or the session has reached its 90-day absolute lifetime |
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

### `GET /api/account/policy`

Return the authenticated account's policy tier.

Auth: app access token

Success:

```json
{
  "code": 0,
  "message": "success",
  "body": {
    "account_id": "123",
    "tier": "free"
  }
}
```

`tier` is currently `free` or `pro`.

Official app clients use this tier only for trusted-computer limits:

- `free`: at most 1 active trusted computer.
- `pro`: at most 10 trusted computers.

The response intentionally does not include active computer selection, selected session rows, preview permission, or per-session entitlement state. Relay does not store those values, and daemon/session transport remains tier-unaware in this revision. The tier is still operator-managed placeholder state; there is no payment provider in this revision.

Error responses:

| Status | Body | Notes |
| --- | --- | --- |
| `401` | `{"code":1016,"message":"The request is unauthorized.","body":null}` | missing or invalid app bearer token |
| `500` | `{"code":2002,"message":"An unexpected internal error occurred.","body":null}` | unexpected account policy failure |

## Connectivity WebSockets

These routes carry connectivity control-plane state. They do not carry terminal snapshots, live terminal bytes, structured input, fallback QUIC packets, session previews, or direct UDP packet data. Rendezvous frames may carry UDP candidate addresses only.

### `POST /api/pairing/responses`

Submit one signed pairing response over HTTP.

Auth: fingerprint-bound app access token.

Request body shape matches connectivity pairing payload fields:

```json
{
  "account_id": "123",
  "invitation_id": "pair_abcd1234",
  "correlation_id": "corr_abcd1234",
  "client_public_key": "<hex-ed25519-public-key>",
  "client_fingerprint": "<hex-sha256-public-key>",
  "client_display_name": "Pixel",
  "signature": "<hex-ed25519-signature>"
}
```

`signature` is required by the computer-side verifier. Relay does not verify it;
Relay only checks the authenticated account and client fingerprint before
forwarding the response to the reserved computer correlation.

Success:

- `200 OK`
- standard success envelope with `{"status":"forwarded"}` in `body`

Error responses:

| Status | Body | Meaning |
|--------|------|---------|
| `400` | `{"code":1001,"message":"The request is invalid.","body":null}` | malformed JSON or request shape |
| `401` | `{"code":1016,"message":"The request is unauthorized.","body":null}` | missing or invalid app bearer token |
| `403` | `{"code":1017,"message":"The request is forbidden.","body":null}` | `client_fingerprint` or `account_id` does not match authenticated app session |
| `404` | `{"code":1018,"message":"The requested resource was not found.","body":null}` | correlation is unknown, expired, or unavailable |

### `GET /api/connectivity/ws`

Auth: fingerprint-bound app access token. Legacy app sessions without a stored client fingerprint are rejected with `403`.

Compatibility note: `GET /api/connectivity/app/ws` remains available as a legacy alias in this revision.

Client first sends:

```json
{
  "type": "app_register",
  "protocol_version": 2
}
```

Relay replies with:

```json
{
  "type": "computer_snapshot",
  "computers": []
}
```

Relay may later send `computer_visible`, `client_revoked`, or `computer_removed`. The primary realtime socket does not accept one-shot pairing response submissions; clients submit signed pairing responses through `POST /api/pairing/responses`. The legacy `/api/connectivity/app/ws` alias still accepts `pair_response_submit` during this compatibility revision. The response must include the authenticated app account id and the app session's client fingerprint; Relay forwards only through a live computer-owned reserved correlation.

After a paired daemon is visible, app peers may open a direct-attempt rendezvous:

```json
{
  "type": "rendezvous_open",
  "request_id": "req-1",
  "attempt_id": "attempt-uuid",
  "computer_id": "dev_abcd1234",
  "public_udp_addr": "203.0.113.10:50000",
  "private_udp_addrs": ["10.0.0.5:50000"]
}
```

Relay forwards this to the daemon as `rendezvous_hint` with `actor: "client"` (wire literal for client-side actor),
`client_fingerprint`, and `expires_at`. The daemon sends its own
`rendezvous_hint` with the same `attempt_id` and `client_fingerprint`; Relay
forwards it to that app with `actor: "daemon"`. Either side may send
`rendezvous_close` with `attempt_id` to remove live attempt state; daemon-origin
closes also include `client_fingerprint` for disambiguation. After the daemon
accepts a direct QUIC/TLS connection, it reports `direct_session_open` with
`attempt_id`, `computer_id`, and `client_fingerprint`. Relay then treats direct
as the winning path for that attempt and can later send `direct_session_close`
to cancel the accepted daemon-local direct transport after logout, token
revocation, trusted-device revocation, or account deletion. Relay stores
rendezvous and accepted-direct state only in memory and expires or removes it
through those lifecycle events. Daemon-local direct transports are tied to the
current daemon Relay connectivity socket and close locally if that socket
disconnects.

After a paired daemon is visible, app peers may request fallback tunnel setup:

```json
{
  "type": "relay_tunnel_request",
  "request_id": "req-1",
  "attempt_id": "attempt-uuid",
  "computer_id": "dev_abcd1234",
  "fallback_reason": "direct_timeout",
  "direct_setup_latency_ms": 3000,
  "relay_setup_latency_ms": 120
}
```

Relay replies to the app and sends a matching frame to the daemon:

```json
{
  "type": "relay_tunnel_ready",
  "request_id": "req-1",
  "attempt_id": "attempt-uuid",
  "computer_id": "dev_abcd1234",
  "client_fingerprint": "<client-device-fingerprint>",
  "actor": "client",
  "tunnel_token": "<single-use-token>",
  "fallback_reason": "direct_timeout",
  "direct_setup_latency_ms": 3000,
  "relay_setup_latency_ms": 120
}
```

The daemon-side frame has `actor: "daemon"` and a different single-use token.
Both frames include `client_fingerprint` so the daemon can select the trusted
trusted client public key needed to pin the inner QUIC/TLS handshake. Diagnostic
fields are optional, app-supplied values that let the daemon annotate the
subsequent `path_state`; Relay forwards them without interpreting terminal or
transport semantics.
If fallback is accepted for an attempt while direct rendezvous is still pending,
Relay removes that rendezvous and sends `rendezvous_close` to the daemon so only
one path can win. If direct has already won and reported `direct_session_open`,
Relay rejects fallback setup for that same app session, daemon, and `attempt_id`.
Relay rejects tunnel setup for unpaired apps, wrong accounts, wrong device
fingerprints, offline daemons, expired attempts, and rate-limited request bursts
with an `error` frame.

Connectivity WebSocket errors use the same JSON frame shape on app and daemon
sockets:

```json
{
  "type": "error",
  "request_id": "req-1",
  "reason": "relay_tunnel_unavailable"
}
```

Rate-limited fallback requests include a retry hint:

```json
{
  "type": "error",
  "request_id": "req-1",
  "reason": "relay_rate_limited",
  "retry_after_seconds": 60
}
```

App-side reasons are `invalid_register`, `invalid_pairing_response`,
`client_fingerprint_mismatch`, `pairing_account_mismatch`,
`pairing_correlation_not_found`, `rendezvous_unavailable`,
`relay_tunnel_unavailable`,
`relay_rate_limited`, and `unsupported_event`. Daemon-side reasons are
`invalid_register`, `invalid_client_fingerprint`,
`invalid_pairing_correlation`, `rendezvous_unavailable`,
`direct_session_unavailable`, and
`unsupported_event`.

### `GET /connectivity/computer/ws`

Auth: agent bearer token.

Compatibility note: `GET /connectivity/daemon/ws` remains available as a legacy alias in this revision.

Daemon first sends:

```json
{
  "type": "computer_register",
  "protocol_version": 2,
  "computer": {
    "computer_id": "dev_abcd1234",
    "display_name": "Yuanbo's MacBook Pro",
    "platform_family": "macos",
    "platform_id": "macos",
    "computer_public_key": "<hex-ed25519-public-key>",
    "computer_fingerprint": "<hex-sha256-public-key>",
    "tunnel_version": "v0.1.0"
  },
  "trusted_clients": [
    {
      "fingerprint": "<client-device-fingerprint>",
      "display_name": "Pixel"
    }
  ]
}
```

Relay derives app-visible computer presence from this live trusted roster and the authenticated app session fingerprint. Relay does not persist the roster durably; computer reconnect rebuilds visibility. The legacy `/connectivity/daemon/ws` alias may still send `daemon_register` with `daemon` during this compatibility revision.

Computer peers reserve pairing invitations with `pair_invitation_reserve` using the desired `correlation_id` as `request_id`. Relay replies with `pair_invitation_reserved` and an `account_id`; the computer signs that account id into the invitation before returning it locally. Clients submit signed responses through `POST /api/pairing/responses`; Relay forwards accepted REST submissions as `pair_response_forward`. After local SAS confirmation stores client trust, the computer sends `pair_completed` with `client_fingerprint`, and Relay emits `computer_visible` to any matching online app peer. The legacy app websocket alias may still accept `pair_response_submit` during this compatibility revision.

Daemon peers receive `rendezvous_hint` when a paired app opens a direct attempt.
Daemon peers may respond with their own `rendezvous_hint` containing
`attempt_id`, `client_fingerprint`, `public_udp_addr`, and optional
`private_udp_addrs`; Relay forwards only when the attempt is live and still
belongs to that app/daemon pair.

Daemon peers send `direct_session_open` after accepting pinned QUIC/TLS for a
live rendezvous attempt. Relay rejects stale, superseded, logged-out, or
fallback-won direct opens by sending `direct_session_close`. Daemon peers should
also send `direct_session_close` when an accepted direct transport ends locally.

Daemon peers receive `relay_tunnel_ready` when a paired app requests fallback
tunnel setup. The daemon redeems its daemon-scoped `tunnel_token` at the tunnel
endpoint below and then runs QUIC/TLS over the resulting packet tunnel.

### `GET /connectivity/tunnel/ws`

Auth: one-time fallback tunnel token in `Authorization: Bearer <single-use-token>`.

Example:

```text
GET /connectivity/tunnel/ws
Authorization: Bearer <single-use-token>
```

This endpoint upgrades to WebSocket and accepts binary WebSocket messages only.
Each binary message is one opaque encrypted QUIC packet. Relay pairs the app
and daemon endpoints by `attempt_id`, forwards binary packets unchanged, and
does not parse QUIC, connectivity frames, terminal bytes, input, resize, preview
text, or session metadata. Tokens expire quickly, may be redeemed once, and are
invalidated when the owning app session, daemon connection, agent token, user, or
paired-device trust is revoked.

### `GET /api/sessions`

List the authenticated user's live sessions for retained Relay/classic attach clients and account-level live-session operations.

Official mobile companion note: this endpoint is not the companion's post-launch session roster or detail authority. The companion waits for daemon connectivity transport `session_index` or `session_upsert`.

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
  ]
}
```

Notes:

- the list is sorted newest-first by `started_at`
- only sessions owned by the authenticated user are returned
- another user's live sessions must remain invisible even when both users have active `tunnel` connections
- the list is live-only, not history
- `git_branch` is the best-effort Git branch for `cwd`; when the startup directory is not on a symbolic branch it is returned as an empty string
- `device_id` is copied from the registering session when the local `tunnel run` can read an existing daemon identity; otherwise it is an empty string
- when `device_id` is non-empty and the daemon is currently online, clients can use it to correlate with `GET /api/computers[].computer_id`; the relay does not validate that relationship during session registration
- `launch_source` is controlled by relay launch correlation; clients should not infer mobile launch from `device_id` alone
- missing, empty, or unknown `launch_source` values should be treated as `local` for display
- `platform_family`, `platform_id`, and `computer_name` are stable keys in the session payload; when metadata is unavailable they are returned as empty strings rather than omitted
- `platform_family` is the coarse fallback field for session device identity, currently `macos` or `linux`
- `platform_id` is the best-effort specific platform identifier for client icon mapping, for example `macos`, `ubuntu`, `debian`, `arch`, or `fedora`
- `computer_name` is the user-facing machine name chosen by the agent: display name first, hostname as fallback

Error responses:

| Status | Body | Meaning |
|--------|------|---------|
| `401` | `{"code":1016,"message":"The request is unauthorized.","body":null}` | missing or invalid app or agent bearer token |
| `500` | `{"code":2002,"message":"An unexpected internal error occurred.","body":null}` | unexpected server failure |

### `DELETE /api/sessions/:sessionID`

Ask the owning online `tunnel run` process to stop one live session.

Auth: app access token or agent token

Compatibility note: `POST /api/sessions/:sessionID/stop` remains available as a legacy alias in this revision. This retained Relay stop path is account-level/classic behavior, not the official mobile companion session authority.

Headers:

```text
Authorization: Bearer <access-token-or-agent-token>
```

Request body: none

Success response:

```json
{
  "code": 0,
  "message": "success",
  "body": {
    "session_id": "sess-1",
    "status": "stopped"
  }
}
```

Notes:

- this is destructive session shutdown, not the local `tunnel workspace close` workspace-view action
- local-launched and mobile-launched sessions use the same stop path
- the relay sends `stop_session` to the owning `/agent/ws` connection, removes the live session from discovery, and closes active attaches with `session_stopped`
- the owning `tunnel run` process exits after receiving `stop_session`

Error responses:

| Status | Body | Meaning |
|--------|------|---------|
| `401` | `{"code":1016,"message":"The request is unauthorized.","body":null}` | missing or invalid app or agent bearer token |
| `404` | `{"code":1015,"message":"The session was not found or is offline.","body":null}` | session is unknown, belongs to another user, or is currently offline |
| `500` | `{"code":2002,"message":"An unexpected internal error occurred.","body":null}` | unexpected server failure |
| `503` | `{"code":2001,"message":"The service is temporarily unavailable.","body":null}` | session registry unavailable |

## Client Attach WebSocket

### `GET /api/sessions/:sessionID/attach/ws`

Attach to one live session owned by the authenticated user for retained Relay/classic clients.

Official mobile companion note: mobile session detail, terminal snapshots/live bytes, input, and resize use daemon connectivity transport instead of this Relay attach websocket after launch.

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
- `snapshot_done` may include bounded submit anchors that map into the just-restored snapshot buffer
- after `snapshot_done`, relay may send live `submit_anchor` controls for new submit Enter events while the client remains attached

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

`submit_anchors` is optional and omitted when no valid anchors are available. Each anchor is a content-free navigation hint for a local or remote input event that sent an `ENTER` carriage return to the PTY outside a bracketed-paste region. `id` is an opaque session-local identifier that is stable only while the running agent retains that anchor; clients must not treat it as durable across process exit, a new `session_id`, or anchor expiry. `line` is a 0-based row in the terminal buffer after applying the preceding snapshot bytes, and `submitted_at` is a Unix timestamp in integer seconds. Anchors are bounded agent-local metadata; at most 256 valid anchors are forwarded, invalid anchors are omitted, and anchors are not durable transcript history or exact Codex-rendered message-block positions.

`submit_anchor`

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

`submit_anchor` is a live incremental control message for clients that are already attached. It uses the same content-free anchor shape and same Tunnel-owned retention/id model as `snapshot_done.submit_anchors`, but `line` is interpreted against the client's current terminal buffer when the live event is received. Fresh `snapshot_done.submit_anchors` remains the reconciliation point after reconnect, missed live events, or local provisional dots.

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
- bytes before `snapshot_done` are the fresh terminal-state snapshot and may include up to 10,000 lines of bounded agent-local normal-buffer scrollback ahead of the current viewport
- bytes after `snapshot_done` are live PTY output
- frame boundaries are not semantic terminal boundaries
- clients should keep local terminal scrollback enabled if they want those replayed snapshot lines to remain available after restore
- clients should interpret `snapshot_done.submit_anchors` only after applying the preceding snapshot bytes, and should ignore unknown control fields for forward compatibility
- clients should interpret live `submit_anchor.line` against the current attached terminal state when the control is received, and reconcile from the next fresh `snapshot_done.submit_anchors` after reconnect

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
- `input_text { "submit": true }` means "text, then one trailing carriage return" as one serialized PTY-owner operation; the trailing carriage return may create a bounded agent-local submit anchor for future fresh attaches and currently attached clients
- `input_text` with `submit: false` does not append Enter, but any carriage return already present in `text` outside a bracketed-paste region is still Enter-bearing PTY input and may create an anchor
- `input_key { "key": "ENTER" }` sends the same carriage return and may also create a bounded agent-local submit anchor for snapshots and live attached clients
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
