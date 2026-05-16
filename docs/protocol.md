# Agent Tunnel Relay Protocol

This document describes this repository's current relay-facing implementation contract for clients, agents, and daemons. Cross-repository protocol decisions live in [yuanbohan/agent-tunnel-protocols](https://github.com/yuanbohan/agent-tunnel-protocols); keep this mirror aligned with that protocol source of truth. Connectivity protocol mirror provenance is tracked in [docs/protocols/connectivity.md](./protocols/connectivity.md).

For endpoint-level request and response examples, auth requirements, and error contracts, see [docs/api.md](./api.md).

## Core Model

- `session_id` identifies one running `tunnel` process. Relay reconnects for that process keep the same `session_id`; a fresh launch gets a fresh `session_id`.
- `device_id` / `computer_id` identifies one machine-local daemon identity. Daemon reconnects and restarts keep the same id for that daemon state.
- `tunnel run` owns the PTY and terminal mirror. It registers with the local daemon broker before startup and publishes metadata, latest preview, terminal snapshots, and live output bytes locally to the daemon.
- The daemon connectivity transport is the official mobile companion authority for session roster, preview, terminal snapshots/live bytes, input, resize, and session detail.
- Relay is the auth, account policy, pairing, computer presence, rendezvous, fallback setup, and launch-correlation control plane. It does not expose a session list, session stop API, session attach websocket, transcript replay API, or terminal byte stream.
- Relay keeps live owner/correlation state for online `/agent/ws`, `/device/ws`, `/connectivity/computer/ws`, and `/api/connectivity/ws` peers. It does not retain transcript history, terminal state, daemon broker rosters, trusted client rosters, or offline computer/session history.
- Relay fallback carries opaque encrypted QUIC packets only. Relay must not parse QUIC, terminal output, previews, snapshots, input, resize, path badges, or session protocol semantics from fallback payloads.

All protocol timestamps are Unix timestamps represented as JSON integers in seconds.

## Endpoint Inventory

| Endpoint | Role | Auth | Kind | Purpose |
|----------|------|------|------|---------|
| `GET /healthz` | Health probe | None | HTTP | Health check for relay reachability |
| `POST /api/auth/register` | App | None | HTTP | Register an invite-gated user |
| `POST /api/auth/login` | App | None | HTTP | Create an app session |
| `POST /api/auth/refresh` | App | Refresh token | HTTP | Rotate an app session |
| `POST /api/auth/logout` | App | App bearer | HTTP | Revoke one app session and close that app's connectivity peers |
| `POST /api/auth/password/change` | App | App bearer | HTTP | Change password and revoke app sessions |
| `GET /api/account/policy` | App | App bearer | HTTP | Return account tier/policy for trusted-computer limits |
| `GET /api/agent-tokens` | App | App bearer | HTTP | List agent tokens |
| `POST /api/agent-tokens` | App | App bearer | HTTP | Create an agent token |
| `DELETE /api/agent-tokens/:id` | App | App bearer | HTTP | Revoke an agent token and disconnect matching live peers |
| `GET /api/computers` | App | App bearer | HTTP | Current online computer snapshot for the authenticated user |
| `POST /api/computers/:computerID/sessions` | App | App bearer | HTTP | Ask one online daemon to launch `tunnel run <command>` and wait for `session_ready` |
| `POST /api/pairing/responses` | App | App bearer | HTTP | Submit a signed pairing response correlated to a reserved invitation |
| `GET /api/connectivity/ws` | App | App bearer | WebSocket | Fingerprint-bound app connectivity control plane |
| `GET /agent/ws` | Agent | Agent bearer | WebSocket | Session registration and launch readiness correlation |
| `GET /device/ws` | Daemon | Agent bearer | WebSocket | Computer/device registration plus launch request/result routing |
| `GET /connectivity/computer/ws` | Daemon | Agent bearer | WebSocket | Trusted roster registration, pairing response forwarding, rendezvous, and fallback setup |
| `GET /connectivity/tunnel/ws` | App/daemon | Fallback tunnel token | WebSocket | Opaque binary packet tunnel for Relay fallback QUIC transport |

Removed from the current product contract:

- the older global update websocket
- the older Relay session list, stop, attach, and frame routes
- the old `/api/devices` and `/api/devices/:deviceID/launch` compatibility aliases
- the old `/api/connectivity/app/ws` and `/connectivity/daemon/ws` realtime aliases

## Auth Headers

App-facing endpoints use an app access token:

```text
Authorization: Bearer <access-token>
```

Agent and daemon relay connections use a user-owned long-lived agent token:

```text
Authorization: Bearer <agent-token>
```

Fallback tunnel peers use a short-lived single-use tunnel token:

```text
Authorization: Bearer <single-use-token>
```

## Computer Launch

`POST /api/computers/:computerID/sessions` sends one launch request to a currently online daemon. The request requires `command` and `cwd`, and may include `label`.

Launch success means:

1. Relay routed the request to the selected online daemon.
2. The daemon accepted local validation, created a tmux-backed workspace session, and started `tunnel run`.
3. The launched `tunnel run` registered with the local broker.
4. The launched `tunnel run` registered with Relay over `/agent/ws`.
5. The PTY process started and `/agent/ws` sent matching `launch_ready`.

Relay then returns `status: "session_ready"` with the new `session_id`. For the official mobile companion, that `session_id` is only a control-plane correlation key; the visible session row/detail and interactive traffic must arrive from daemon transport `session_index` or `session_upsert`.

Known launch failure reasons:

- `device_offline`
- `busy`
- `command_not_allowed`
- `path_not_found`
- `tmux_not_found`
- `tunnel_not_found`
- `session_start_failed`
- `launch_timeout`

## Device WebSocket

`/device/ws` is a bidirectional websocket between Relay and one running `tunnel daemon` process.

The first frame is device registration:

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

Relay-to-daemon launch request:

```json
{
  "type": "launch_request",
  "request_id": "dev_abcd1234-150405.000000000",
  "command": "codex --profile prod",
  "cwd": "/repo",
  "label": "api-fix"
}
```

Daemon-to-Relay launch result:

```json
{
  "type": "launch_result",
  "request_id": "dev_abcd1234-150405.000000000",
  "status": "accepted",
  "workspace_session": "launch_abcd1234"
}
```

or:

```json
{
  "type": "launch_result",
  "request_id": "dev_abcd1234-150405.000000000",
  "status": "failed",
  "reason": "busy"
}
```

`accepted` does not complete the app request. Relay waits for the later `/agent/ws` `launch_ready` frame with matching launch context. If that readiness times out after daemon acceptance, Relay may send a best-effort `terminate_request` for the daemon-local `workspace_session`.

## Agent WebSocket

`/agent/ws` is a JSON websocket between Relay and one running `tunnel` process. It is kept for live session ownership and launch correlation only; terminal data, input, resize, previews, and session detail do not flow through this socket.

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

- `register` must be the first agent control frame.
- Relay binds the live owner state to the user behind the authenticating agent token.
- `launch_context` is optional and only present for daemon-created mobile launches.
- Relay stores the registered metadata only for live ownership/correlation. It no longer exposes an account-level session discovery API.
- Relay marks launch correlation as mobile only when a later `launch_ready` matches a pending request owned by the same user and token.

### `launch_ready`

```json
{
  "type": "launch_ready",
  "launch_context": {
    "source": "mobile",
    "request_id": "dev_abcd1234-150405.000000000"
  }
}
```

`launch_ready` is sent after local broker registration and PTY process startup. Matching frames complete pending computer launch requests as `session_ready`; unmatched, duplicate, or unknown frames are ignored.

Relay ignores unexpected legacy attach, input, resize, stop, and binary terminal-byte frames on `/agent/ws`.

## Connectivity WebSockets

`GET /api/connectivity/ws` is the app-side control plane. App sessions are bound to a client device fingerprint, and only trusted computers for that fingerprint become visible.

`GET /connectivity/computer/ws` is the daemon-side control plane. The daemon first sends:

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

Relay derives live app-visible computer presence from this trusted roster and `pair_completed` / `client_revoked` events. Relay does not persist the roster durably; daemon reconnect rebuilds visibility from daemon-local `pairing_state.json`.

Connectivity direct attempts use `rendezvous_open`, `rendezvous_hint`, `direct_session_open`, `direct_session_close`, and `rendezvous_close` over the app/daemon realtime sockets. Relay stores this state live-only and must not derive terminal/session semantics from candidate hints.

## Fallback Tunnel

`GET /connectivity/tunnel/ws` upgrades to a binary WebSocket after single-use fallback token authentication. Each binary message is one opaque encrypted QUIC packet. Relay pairs app and daemon endpoints by `attempt_id`, forwards packets unchanged, and invalidates tunnel state on expiry, disconnect, logout, token revocation, user deletion, or trusted-device revocation.

## Client Notes

- App clients should validate Relay availability with `GET /healthz` or the app endpoints they actually need.
- Official mobile clients should wait for daemon transport `session_index` / `session_upsert` after `session_ready`.
- `tunnel session list` and `tunnel session stop <session-id>` are local-computer commands backed by the local daemon control socket and broker, not Relay account-wide APIs.
- Browser clients do not have a Relay attach websocket in this product revision. Any UI/client terminal experience should use the daemon connectivity transport after pairing and path setup.

## Invariants

- Relay remains content-opaque for terminal/session data.
- Relay state is live-only except for persisted auth/operator data in PostgreSQL.
- The local `tunnel run` process remains the PTY owner.
- The daemon broker and daemon connectivity transport are the current session data plane.
- Computers do not share session rows through Relay.
