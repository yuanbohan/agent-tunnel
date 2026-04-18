# Agent Tunnel Architecture

This document describes the current system shape for the attach-based protocol.

## System Shape

`tunnel` owns the real local agent process, its PTY, and the authoritative current terminal state for that session. Built-in CLI commands own the top-level namespace, and local command launch happens only through `tunnel run <command>`. Every PATH-resolved launcher command follows the same path: one local PTY child, one session hub, one headless terminal mirror, one outbound relay connector, and no launcher-specific sidecar.

Separately, `tunnel daemon` owns one explicit background device-launch runtime on a machine with local `tmux`. That daemon has its own local control socket, its own live relay connector on `/device/ws`, and its own dedicated tmux workspace used to create future `tunnel run <command>` sessions. The daemon is the state authority for device launch behavior; the relay only brokers currently online daemon connections plus the short-lived correlation needed to turn one launch request into one later `session_ready` result.

The relay exposes authenticated APIs so external clients can register accounts, log in, manage agent tokens, discover live sessions, discover live devices, attach to one online session, and request that one online device daemon create a new session. Operator maintenance routes stay outside the public `/api/` namespace and are intended for host-local use only. PostgreSQL is the durable source of truth for users, invite codes, app sessions, agent tokens, and operator audit records. App auth uses opaque bearer access tokens with a nominal 24 hour lifetime, rotating refresh tokens with a 30 day sliding lifetime, and a 90 day absolute session lifetime anchored at the original login. The relay is not the terminal-state authority and it does not retain transcript history.

For hosted deployments, the security invariant is strict user scoping: the user who owns the agent token also owns the live session, `GET /api/sessions` returns only that user's sessions, and cross-user attach attempts resolve as not found.

See [docs/api.md](./api.md) for the current endpoint inventory, auth requirements, request and response examples, and error contracts.

Protocol-facing timestamps such as `started_at` are Unix timestamps encoded as JSON integers in seconds.

The local terminal is still the primary and most complete view of the PTY session. Remote access is session-scoped: a client attaches to one session, receives a fresh terminal-state snapshot that may include bounded agent-local normal-buffer scrollback, and then receives subsequent live PTY bytes on that same attach.

`tunnel` enforces strict startup gating and runtime reconnect:

- startup gating: relay registration must succeed during the startup wait window
- runtime behavior: if relay outages occur, local terminal work continues; the connector retries registration with backoff

```text
local machine
┌──────────────────────────────────────────────────────────────────┐
│                              tunnel                              │
│                                                                  │
│  launcher resolve                                                │
│        │                                                         │
│        ▼                                                         │
│  local runtime                                                   │
│  - PATH-resolved CLI agent PTY child                             │
│        │                                                         │
│        ▼                                                         │
│     session hub                                                  │
│  - PTY output fanout                                             │
│  - PTY input routing                                             │
│  - PTY size tracking                                             │
│        │                    │                     │              │
│        ▼                    ▼                     ▼              │
│  local terminal sink   terminal mirror      relay connector      │
│                         - current screen     - register           │
│                         - snapshot bytes     - resize             │
│                         - live attach fanout - attach routing     │
└────────────────────────────────┬─────────────────────────────────┘
                                 │
                                 ▼
                    ┌───────────────────────────────────┐
                    │           relay server            │
                    │  - auth                           │
                    │  - live session registry          │
                    │  - live device routing            │
                    │  - session attach websocket       │
                    │  - device launch websocket        │
                    │  - agent/client/device routing    │
                    └────────────────┬──────────────────┘
                                     │
                   ┌─────────────────┴─────────────────┐
                   ▼                                   ▼
            mobile / web client A               mobile / web client B
```

## Major Responsibilities

### `tunnel`

`tunnel` is the PTY owner and the authority for current terminal state for one running session.

It owns:

- the top-level CLI contract, including `tunnel run` and `tunnel auth`
- terminal-native login that exchanges relay username/password for one locally saved agent token in `~/.tunnel/auth.json`
- runtime auth precedence for `tunnel run`: `TUNNEL_AUTH_TOKEN` first, then `~/.tunnel/auth.json`
- runtime auth precedence for `tunnel daemon start`: `TUNNEL_AUTH_TOKEN` first, then `~/.tunnel/auth.json`
- launcher resolution
- PTY lifecycle and local terminal raw mode
- startup relay wait and background reconnect policy
- fanout of PTY output to the local terminal, terminal mirror, and relay connector
- the authoritative headless terminal mirror for the currently visible screen
- session-scoped attach snapshot creation
- forwarding remote input back into the PTY
- translating structured remote key input into PTY bytes
- session-wide resize authority, which continues to follow the local terminal in this phase

### Relay

The relay is a live broker, not durable storage and not a semantic interpreter of terminal content.

It owns:

- app-bearer auth, agent-token auth, and invite-gated account registration
- binding each live session to the user who owns the authenticating agent token
- fixed-token local-only operator control routes for invite creation, invite disable, and account deletion
- current live-session snapshots for discovery
- current online-session discovery and immediate offline removal when the owning agent disconnects
- the owner websocket for each live session
- client attach websockets for online sessions
- enforcing user-scoped discovery and attach authorization so one user's sessions stay invisible to other users
- routing JSON control messages and client-scoped binary terminal bytes between clients and the owning agent
- closing active attaches promptly when the owning agent disappears
- synchronously evicting live sessions when a user is deleted or an agent token is revoked
- closing affected app-side attaches when an app session logs out or a password change revokes app sessions, without disconnecting the owning agent
- tracking only currently online `/device/ws` connections and the transient request-correlation state needed to turn one launch request into one `session_ready` result or timeout

The relay does not own:

- session creation beyond registration by an agent
- transcript history
- terminal emulation
- snapshot generation
- preview rendering
- content interpretation of terminal output
- end-to-end guarantees that a remote client observed every PTY byte
- creation or ownership of local tmux workspace sessions on device daemons
- offline device inventory
- daemon health, last launch failure, or tmux-workspace state

### Client

The client is responsible for rendering a session-scoped attach correctly.

It should:

- use `GET /api/sessions` to discover currently online sessions
- use `GET /api/sessions/:id/attach/ws` to attach to one online session
- when running in a browser, open the attach websocket from the same origin as the relay; native clients may omit `Origin`
- size its terminal emulator from the initial `attached` control message before feeding subsequent binary bytes
- treat binary bytes before `snapshot_done` as snapshot bytes and binary bytes after it as live PTY bytes
- rebuild terminal state from a fresh attach after disconnect instead of assuming transcript replay

## Attach Flow

The remote attach path is:

```text
client opens /api/sessions/:id/attach/ws
→ relay authenticates and checks that the session is online
→ relay allocates relay-scoped client_id
→ relay sends attach_open to the owning agent
→ agent terminal mirror atomically:
     - captures current cols / rows
     - serializes the current terminal state
     - registers the attached client for subsequent live bytes
→ relay sends attached { session_id, cols, rows }
→ relay forwards snapshot bytes as binary frames
→ relay sends snapshot_done
→ relay forwards subsequent live PTY bytes as binary frames
```

The critical invariant is gap-free handoff: there must be no byte gap between the snapshot point and the first later live bytes for that attached client.

## Terminal Mirror

The terminal mirror exists to make fresh snapshot recovery precise without transcript replay.

- it is fed from the same PTY output stream seen by the local terminal
- it preserves the current terminal state and a bounded amount of in-memory normal-buffer scrollback, not durable transcript history
- it is the source of snapshot bytes on attach
- it fans out subsequent live bytes to attached clients after the snapshot boundary
- it follows PTY resize updates owned by the local terminal session

The current implementation uses `github.com/gitpod-io/xterm-go`, an xterm-compatible headless engine with serialization support, so the snapshot path can restore alternate screen state, colors, cursor state, and other modern TUI behavior without a hand-written ANSI screen walker.

## Remote Input Flow

Remote input still flows through the relay, but translation into PTY bytes remains agent-owned.

```text
client input message
→ relay attach websocket
→ owning agent websocket
→ tunnel connector
→ structured input translation:
     - input_text { submit: false } -> UTF-8 text bytes
     - input_text { submit: true } -> UTF-8 text bytes, then trailing \r, as one serialized submit operation
     - supported input_key events -> PTY key bytes
→ PTY stdin
```

This keeps terminal behavior close to the PTY owner and avoids embedding terminal emulation inside the relay.

## Resize Flow

PTY size remains local-terminal-owned in this phase.

```text
local terminal resize
→ session hub updates cols / rows
→ local PTY resize
→ terminal mirror updates size
→ connector sends resize metadata to relay
→ relay forwards resize control message to each attached client
→ remote clients resize their terminal emulator
```

Remote clients follow the PTY size. They do not compete to become size authority in this revision.

## Startup And Relay Continuity

Relay registration is a startup gate for local session launch; if it fails, launch fails.

```text
tunnel launch
→ connector starts trying /agent/ws
→ if registration succeeds during the startup wait window:
     local session starts in connected mode
→ if registration fails during startup:
     launch fails and no local session starts
→ if a later relay disconnect happens:
     local PTY session continues uninterrupted
     connector retries with backoff until registration recovers
```

## Reconnect Lifecycle

The session lifecycle is centered on one running agent process.

1. The agent registers over `/agent/ws`; the session becomes discoverable.
2. Clients may attach only while the session is online.
3. If the agent websocket drops, the relay closes active attaches and removes the session from `GET /api/sessions` immediately.
4. While the agent is offline, attaches and remote input are unavailable because the session is no longer discoverable.
5. If the same running agent reconnects after a relay drop, it re-registers with the same `session_id`.

Closing the agent process ends the session. A later agent launch starts a different session with a different `session_id`.

## Device Launch Lifecycle

The device-launch lifecycle is separate from session attach:

1. `tunnel daemon start` creates a background runtime on one desktop machine.
2. That runtime ensures local `tmux` is available, persists a stable `device_id`, and connects to `/device/ws`.
3. `GET /api/devices` lists only currently connected devices for the owning user.
4. `POST /api/devices/:id/launch` routes one request to that live device daemon and assigns a relay-scoped `request_id`.
5. The daemon decides locally whether the request is allowed, whether it is already busy, whether the requested `cwd` is valid, and whether a new tmux-backed session can be created.
6. If the daemon accepts the launch locally, it starts a new tmux session running `tunnel run <command>` with the requested cwd and optional label, and it passes the launch correlation forward.
7. The later `tunnel run <command>` process registers a normal session on `/agent/ws` and includes that launch correlation.
8. The relay completes the pending mobile launch request as `session_ready` when it sees the matching session registration, or returns a structured timeout failure if that registration does not arrive in time.
9. Session discovery and attach then proceed through the unchanged session APIs.

## Package Map

- `cmd/tunnel`: local `tunnel` entrypoint
- `internal/tunnel/daemon/`: local daemon control socket, persisted device identity, tmux workspace management, doctor/status reporting, and relay device connector
- `internal/tunnel/session/`: PTY ownership, local terminal handling, hub fanout, resize state, and terminal mirror
- `internal/tunnel/connector/`: outbound relay connection, session registration, attach routing, and resize signaling
- `cmd/relay`: relay entrypoint
- `internal/config/`: relay process configuration loaded during startup
- `internal/logx/`: global structured logging setup and helpers
- `internal/relay/auth/`: invite code rules, username/password normalization, app-session flows, and agent-token flows
- `internal/relay/operator/`: operator invite and user-maintenance services
- `internal/relay/device/`: transient online-device routing, owner metadata, and launch-request coordination
- `internal/relay/session/`: live session registry, owner metadata, and attach-session indexing
- `internal/relay/handler/`: Gin router assembly plus subpackages for middleware, REST API, agent WebSocket flows, device WebSocket flows, attach WebSocket flows, shared request helpers, and HTTP DTOs
- `internal/migration/`: relay schema migration runner and `schema_migrations` tracking
- `internal/relay/store/postgres/`: PostgreSQL-backed auth and operator persistence
- `internal/protocol/`: shared session-attach and device-launch wire types

## Related Documents

- [docs/api.md](./api.md)
- [docs/protocol.md](./protocol.md)
- [docs/tui-attach-flow.md](./tui-attach-flow.md)
