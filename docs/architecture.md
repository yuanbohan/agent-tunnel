# Agent Tunnel Architecture

This document describes the current system shape for this repository's implementation. Cross-repository protocol decisions live in [yuanbohan/agent-tunnel-protocols](https://github.com/yuanbohan/agent-tunnel-protocols); keep this architecture mirror aligned with that protocol source of truth.

## System Shape

`tunnel run` owns the real local agent process, its PTY, and the authoritative terminal mirror for that session. Local command launch happens only through `tunnel run <command>`. Every PATH-resolved launcher command follows the same path: one local PTY child, one session hub, one terminal mirror, one outbound `/agent/ws` registration connector, and one required local daemon broker registration.

`tunnel daemon` owns the machine-local background runtime. It has a local control socket, a long-lived local broker socket for `tunnel run` registrations, a `/device/ws` relay connector, a `/connectivity/computer/ws` relay connector, daemon-local trusted client state, direct UDP rendezvous support, and a dedicated tmux workspace for remote-launched `tunnel run <command>` sessions when tmux is available.

Relay is the authenticated control plane. It owns accounts, app sessions, agent tokens, online computer presence, launch request correlation, pairing response forwarding, rendezvous hint forwarding, fallback tunnel setup, and opaque fallback packet relay. Relay does not expose app-facing session list, stop, detail, or attach APIs. It does not retain transcript history, terminal state, daemon broker rosters, trusted client rosters, or account-wide session rows.

PostgreSQL is the durable authority for users, invite codes, app sessions, app-session device fingerprints, account subscription tiers, agent tokens, and operator audit records. Operator maintenance routes stay outside the public `/api/` namespace and are intended for host-local use only.

## Runtime Graph

```text
local machine
┌──────────────────────────────────────────────────────────────────────┐
│                              tunnel run                              │
│  PATH launcher → PTY child → session hub → terminal mirror            │
│         │              │                 │                           │
│         │              │                 └─ local broker snapshots    │
│         │              └─ local terminal                             │
│         └─ /agent/ws register + launch_ready                         │
└───────────────────────────────┬──────────────────────────────────────┘
                                │ local broker socket
┌───────────────────────────────▼──────────────────────────────────────┐
│                            tunnel daemon                             │
│  control socket, broker roster/cache, tmux workspace, pairing state   │
│  /device/ws, /connectivity/computer/ws, direct UDP, fallback QUIC      │
└───────────────┬───────────────────────────────┬──────────────────────┘
                │                               │
                ▼                               ▼
        relay control plane              mobile companion
        auth / presence / launch         daemon transport session UI
        rendezvous / fallback            roster / preview / input / bytes
```

## Major Responsibilities

### `tunnel`

`tunnel` is the PTY owner and the authority for current terminal state for one running session.

It owns:

- the top-level CLI contract, including `tunnel run`, `tunnel auth`, `tunnel daemon`, `tunnel pair`, `tunnel workspace`, and local `tunnel session` commands
- native binary lifecycle commands `tunnel update` and `tunnel rollback`
- terminal-native login that exchanges relay username/password for one locally saved agent token in `~/.tunnel/auth.json`
- runtime auth precedence: `TUNNEL_AUTH_TOKEN` first, then `~/.tunnel/auth.json`
- launcher resolution
- PTY lifecycle and local terminal raw mode
- startup relay wait and background reconnect policy
- local daemon compatibility checks before startup
- local broker registration before PTY child startup
- fanout of PTY output to the local terminal, terminal mirror, and daemon broker
- PTY input translation for daemon-transport `input_text` and `input_key`
- PTY resize authority, which follows the local terminal

### Daemon

The daemon is the machine-local authority for launch readiness, local session broker state, paired-client trust, and connectivity transport.

It owns:

- daemon lifecycle through `tunnel daemon start/status/stop/doctor`
- local control actions, including daemon-local session list/stop
- the broker socket and live local session roster/cache
- latest preview and coalesced latest terminal snapshot per live broker session
- live output fanout to trusted daemon transports
- machine-local device identity and connectivity identity
- pairing invitations, pending SAS confirmation, trusted client roster, and revocation
- direct UDP rendezvous listener and pinned QUIC/TLS acceptance
- Relay fallback QUIC-over-WebSocket packet tunnel endpoint handling
- dedicated tmux workspace creation for remote launch
- launch validation, busy state, launch health, and last local failure

It does not own:

- durable terminal transcript history
- Relay account/session sharing
- GUI terminal automation
- the user's default tmux socket
- terminal content interpretation beyond broker preview/snapshot handling already produced by `tunnel run`

### Relay

Relay is a live broker, not durable terminal storage and not a semantic interpreter of terminal content.

It owns:

- app-bearer auth, agent-token auth, invite-gated account registration, and account policy
- binding each live `/agent/ws` registration to the user who owns the authenticating agent token
- fixed-token local-only operator control routes
- currently online `/device/ws` computer routing
- `POST /api/computers/:computerID/sessions` request correlation through daemon acceptance and later `/agent/ws` `launch_ready`
- currently online `/connectivity/computer/ws` and `/api/connectivity/ws` peers
- paired-computer visibility derived from daemon-local trusted rosters
- REST-submitted pairing response routing
- short-lived direct rendezvous hint exchange and direct/fallback winner selection
- short-lived actor-specific fallback tunnel tokens
- forwarding fallback WebSocket binary messages as opaque encrypted QUIC packets
- synchronously disconnecting live sessions/devices/connectivity peers when users or tokens are revoked

Relay does not own:

- app-facing session list/detail/stop/attach endpoints
- terminal emulation
- transcript history
- snapshot generation
- preview rendering
- terminal input translation
- direct rendezvous candidate semantics
- fallback QUIC packet semantics
- daemon broker rosters/cache
- offline computer inventory
- daemon health, stop history, tmux workspace state, or trusted client rosters

## Launch Lifecycle

1. `tunnel daemon start` brings a computer online through `/device/ws` and `/connectivity/computer/ws`.
2. App clients discover currently online computers with `GET /api/computers`.
3. App clients launch with `POST /api/computers/:id/sessions`, supplying required `cwd`, required `command`, and optional `label`.
4. Relay routes the launch request to the selected online daemon and creates a request correlation.
5. The daemon validates allowlist, `cwd`, busy state, tmux availability, and `tunnel` availability.
6. If accepted, the daemon creates a tmux-backed session that runs `tunnel run --launch-source mobile --launch-request-id <id> <command>`.
7. The launched `tunnel run` validates the local daemon context, registers with the daemon broker, registers with Relay over `/agent/ws`, starts its PTY process, and sends `launch_ready`.
8. Relay completes the app request as `session_ready` when `launch_ready` matches the pending request.
9. The mobile companion waits for daemon connectivity transport `session_index` or `session_upsert` with that `session_id` before rendering or interacting with the session.

If launch readiness times out after daemon acceptance, Relay may send a best-effort daemon cleanup request for the tmux workspace session.

## Local Session Management

`tunnel session list` and `tunnel session stop <session-id>` are local-computer commands:

- `session list` reads the local daemon control socket and broker snapshot
- `session stop` asks the local daemon broker to route a stop request to the owning local `tunnel run`
- unknown sessions fail locally as session-not-found
- a stopped session on another computer is not reachable through Relay
- `tunnel workspace close` remains a view-detach operation, not destructive session shutdown

## Connectivity Lifecycle

The official mobile companion does not use Relay as the session data plane.

1. App and daemon authenticate to Relay connectivity websockets.
2. Pairing establishes daemon-local trusted client state, confirmed by SAS.
3. Relay derives live computer visibility from daemon `pair_completed` events and the trusted roster sent on reconnect.
4. App and daemon exchange rendezvous hints through Relay.
5. If direct UDP/QUIC succeeds, terminal/session transport runs directly.
6. If direct setup fails or times out, app and daemon redeem short-lived fallback tunnel tokens and run the same encrypted QUIC session over Relay WebSocket packets.
7. Session roster, previews, snapshots/live bytes, input, resize, and detail flow inside daemon connectivity transport.

## Startup And Continuity

Relay registration and daemon broker registration are startup gates for local session launch. If either fails during startup, `tunnel run` fails before terminal setup and child process startup.

After startup:

- Relay outages do not interrupt local terminal work.
- `/agent/ws` retries registration with backoff.
- daemon broker reconnects re-check Relay base URL and auth-context fingerprint before accepting local session data.
- daemon transport availability controls mobile visibility/interaction independently of Relay session APIs because those APIs do not exist.

## Package Map

- `cmd/tunnel`: local `tunnel` entrypoint
- `cmd/relay`: relay entrypoint
- `internal/tunnel/session/`: PTY ownership, local terminal handling, hub fanout, resize state, and terminal mirror
- `internal/tunnel/connector/`: outbound `/agent/ws` registration and launch-ready connector
- `internal/tunnel/daemon/`: local daemon control socket, broker socket and roster/cache, device/connectivity identity, pairing/trust state, tmux workspace management, status/doctor, relay device connector, and connectivity connector
- `internal/protocol/`: shared relay-facing agent, device, connectivity, and session metadata wire types
- `internal/config/`: relay process configuration loaded during startup
- `internal/logx/`: global structured logging setup and helpers
- `internal/relay/auth/`: invite code rules, username/password normalization, app-session flows, and agent-token flows
- `internal/relay/operator/`: operator invite and user-maintenance services
- `internal/relay/device/`: transient online-device routing, owner metadata, and launch-request coordination
- `internal/relay/session/`: live `/agent/ws` owner metadata and launch correlation
- `internal/relay/connectivity/`: live app/daemon connectivity peers, pairing response routing, rendezvous state, and fallback tunnel token/routing state
- `internal/relay/handler/`: Gin router assembly plus middleware, REST API, agent WebSocket, device WebSocket, connectivity WebSocket, shared request helpers, and HTTP DTOs
- `internal/migration/`: relay schema migration runner and `schema_migrations` tracking for legacy/local workflows
- `internal/relay/store/postgres/`: PostgreSQL-backed auth and operator persistence

Docker Compose deployments run PostgreSQL, Relay HTTP/WebSocket, and Binding-only STUN as separate services. Relay and STUN use the same release build artifact published as `ghcr.io/yuanbohan/agent-tunnel-relay` and `ghcr.io/yuanbohan/agent-tunnel-stun`, with independent `RELAY_IMAGE_TAG` and `STUN_IMAGE_TAG` pins. Compose initializes fresh PostgreSQL volumes from `deploy/postgres/latest.sql` and does not run automatic migrations against existing databases.

## Related Documents

- [docs/api.md](./api.md)
- [docs/protocol.md](./protocol.md)
- [docs/daemon.md](./daemon.md)
- [docs/connectivity/protocol/transport.md](./connectivity/protocol/transport.md)
