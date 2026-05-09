# Agent Tunnel Architecture

This document describes the current system shape for the attach-based protocol.

## System Shape

`tunnel` owns the real local agent process, its PTY, and the authoritative current terminal state for that session. Built-in CLI commands own the top-level namespace, and local command launch happens only through `tunnel run <command>`. Every PATH-resolved launcher command follows the same path: one local PTY child, one session hub, one headless terminal mirror, one outbound relay connector, and no launcher-specific sidecar. In the connectivity path, `tunnel run` also best-effort registers metadata, a throttled bounded latest preview, coalesced terminal snapshots, and live output bytes with the local daemon broker after verifying the daemon Relay base URL and auth-context fingerprint, but it remains the PTY and mirror owner.

Separately, `tunnel daemon` owns one background machine runtime. That daemon has its own local control socket, a separate long-lived local broker socket for `tunnel run` registrations, its own live relay connector on `/device/ws`, a connectivity connector on `/connectivity/computer/ws`, and its own dedicated tmux workspace used to create future remote-launched `tunnel run <command>` sessions when tmux is available. The daemon is the state authority for device launch behavior, paired client trust, and live local broker roster/cache. The relay only brokers currently online daemon connections, live connectivity visibility derived from daemon-local trusted rosters, and short-lived correlations needed to turn one launch request into one later `session_ready` result.

`docs/daemon.md` is the daemon-specific implementation contract. Changes to daemon lifecycle, tmux workspace ownership, launch validation, health reporting, or failure reasons must keep that contract aligned with this architecture document and the public API/protocol docs.

The relay exposes authenticated APIs so external clients can register accounts, log in, manage agent tokens, discover live sessions, discover live computers with `GET /api/computers`, attach to one online session, request that one online computer daemon create a new session with `POST /api/computers/:computerID/sessions`, stop any owned live session, and use the connectivity control-plane WebSockets for pairing, paired-daemon visibility, direct rendezvous hints, and Relay fallback tunnel setup. Legacy device-named aliases remain available in this revision for compatibility. `relay serve` also owns the Binding-only STUN UDP listener used for direct candidate discovery when enabled. Operator maintenance routes stay outside the public `/api/` namespace and are intended for host-local use only. PostgreSQL is the durable source of truth for users, invite codes, app sessions, app-session device fingerprints, account subscription tiers, agent tokens, and operator audit records. App auth uses opaque bearer access tokens with a nominal 24-hour lifetime, rotating refresh tokens with a 30-day sliding lifetime, and a 90-day absolute session lifetime anchored at the original login. The relay is not the terminal-state authority and it does not retain transcript history. Live session discovery includes best-effort Git branch metadata for the startup `cwd`, optional local daemon identity through `device_id`, relay-controlled `launch_source`, and device identity metadata such as `platform_family`, `platform_id`, and normalized `computer_name`.

For hosted deployments, the security invariant is strict user scoping: the user who owns the agent token also owns the live session, `GET /api/sessions` returns only that user's sessions, and cross-user attach attempts resolve as not found.

See [docs/api.md](./api.md) for the current endpoint inventory, auth requirements, request and response examples, and error contracts.

Protocol-facing timestamps such as `started_at` are Unix timestamps encoded as JSON integers in seconds.

The local terminal is still the primary and most complete view of the PTY session. Remote access is session-scoped: a client attaches to one session, receives a fresh terminal-state snapshot that may include up to 10,000 lines of bounded agent-local normal-buffer scrollback, and then receives subsequent live PTY bytes on that same attach.

`tunnel` enforces strict startup gating and runtime reconnect:

- startup gating: relay registration must succeed during the startup wait window
- runtime behavior: if relay outages occur, local terminal work continues; the connector retries registration with backoff
- before interactive `tunnel run`, Tunnel may perform one native binary update check at most once per 24-hour interval, prompt in English, and re-exec the same command under a newly installed binary

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
- native binary lifecycle commands `tunnel update` and `tunnel rollback`
- terminal-native login that exchanges relay username/password for one locally saved agent token in `~/.tunnel/auth.json`
- persistent CLI state under `~/.tunnel/`, with `settings.json` as the user-editable settings file and `updater.json` as internal updater state
- runtime auth precedence for `tunnel run`: `TUNNEL_AUTH_TOKEN` first, then `~/.tunnel/auth.json`
- runtime auth precedence for daemon startup, whether explicit through `tunnel daemon start` or best-effort from `tunnel run`: `TUNNEL_AUTH_TOKEN` first, then `~/.tunnel/auth.json`
- automatic startup-update disable through `TUNNEL_UPDATE_DISABLED` or `~/.tunnel/settings.json` `env` overrides
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
- fixed-token local-only operator control routes for invite creation, invite disable, account deletion, and account tier changes
- current live-session snapshots for discovery
- preserving session metadata for discovery, including startup-directory Git branch, optional local daemon `device_id`, and normalized computer identity
- current online-session discovery and immediate offline removal when the owning agent disconnects
- the owner websocket for each live session
- client attach websockets for online sessions
- enforcing user-scoped discovery and attach authorization so one user's sessions stay invisible to other users
- routing JSON control messages and client-scoped binary terminal bytes between clients and the owning agent
- closing active attaches promptly when the owning agent disappears
- synchronously evicting live sessions when a user is deleted or an agent token is revoked
- closing affected app-side attaches when an app session logs out or a password change revokes app sessions, without disconnecting the owning agent
- tracking only currently online `/device/ws` connections and the transient request-correlation state needed to turn one launch request into one `session_ready` result or timeout
- tracking only currently online `/connectivity/computer/ws` and `/api/connectivity/ws` peers for paired-daemon visibility, REST-submitted pairing response routing, short-lived direct rendezvous hint exchange, accepted direct-session close routing, and fallback/direct winner selection; trusted client rosters are daemon-local and are rebuilt into Relay visibility when the daemon reconnects
- issuing short-lived, actor-specific fallback tunnel tokens and forwarding fallback WebSocket binary messages as opaque encrypted QUIC packets

The relay does not own:

- session creation beyond registration by an agent
- transcript history
- terminal emulation
- snapshot generation
- preview rendering
- content interpretation of terminal output
- content interpretation of direct rendezvous hints or fallback QUIC packets
- end-to-end guarantees that a remote client observed every PTY byte
- creation or ownership of local tmux workspace sessions on device daemons
- offline device inventory
- daemon health, last launch failure, stop history, or tmux-workspace state

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
     - maps any still-valid submit anchors into the snapshot buffer coordinates
     - registers the attached client for subsequent live bytes
→ relay sends attached { session_id, cols, rows }
→ relay forwards snapshot bytes as binary frames
→ relay sends snapshot_done, optionally with submit_anchors
→ relay forwards subsequent live PTY bytes as binary frames
→ relay forwards live submit_anchor controls for newly recorded submit Enter events while clients remain attached
```

The critical invariant is gap-free handoff: there must be no byte gap between the snapshot point and the first later live bytes for that attached client.

## Terminal Mirror

The terminal mirror exists to make fresh snapshot recovery precise without transcript replay.

- it is fed from the same PTY output stream seen by the local terminal
- it preserves the current terminal state and up to 10,000 lines of in-memory normal-buffer scrollback, not durable transcript history
- it records bounded local and remote `ENTER` submit anchors as content-free navigation metadata when they occur outside bracketed-paste regions and still map into retained terminal context
- it is the source of snapshot bytes on attach
- it fans out subsequent live bytes and live submit-anchor controls to attached clients after the snapshot boundary
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

Local-terminal input and remote attach input share the same submit-anchor boundary: each `ENTER` carriage return written to the PTY outside a bracketed-paste region may create a bounded agent-local submit anchor for later fresh attaches and live attached clients. These anchors are capped at 256 valid entries and are not prompt text, transcript records, or exact TUI-rendered message markers. This keeps terminal behavior and navigation metadata close to the PTY owner and avoids embedding terminal emulation inside the relay.

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

1. `tunnel run` may best-effort auto-start the background daemon for local broker registration; `tunnel daemon start` remains available for explicit lifecycle management. Broker reconnects are local-only and must continue to verify the daemon Relay base URL and auth-context fingerprint before sending session metadata, previews, snapshots, or live output.
2. That runtime persists a stable `device_id`, connects to `/device/ws`, connects to `/connectivity/computer/ws`, and serves local control plus broker sockets.
3. `GET /api/computers` lists only currently connected devices for the owning user.
4. `POST /api/computers/:id/sessions` routes one request to that live device daemon and assigns a relay-scoped `request_id`.
5. The daemon decides locally whether the request is allowed, whether it is already busy, whether local `tmux` is available, whether the requested `cwd` is valid, and whether a new tmux-backed session can be created.
6. If the daemon accepts the launch locally, it starts a new tmux session running `tunnel run <command>` with the requested cwd and optional label, and it passes the launch correlation forward.
7. The later `tunnel run <command>` process registers a normal session on `/agent/ws`, includes that launch correlation, and supplies its own platform and computer identity metadata as part of the session registration.
8. The relay completes the pending mobile launch request as `session_ready` when it sees the matching session registration, marks the live session with `launch_source: "mobile"`, or returns a structured timeout failure if that registration does not arrive in time.
9. Session discovery and attach then proceed through the unchanged session APIs, with device identity coming from the session itself rather than from launch correlation.
10. If a client later calls `DELETE /api/sessions/:id`, the relay sends `stop_session` to the owning agent, removes the live session from discovery, and closes active attaches with `session_stopped`.

## Package Map

- `cmd/tunnel`: local `tunnel` entrypoint
- `internal/tunnel/daemon/`: local daemon control socket, local broker socket and live roster/cache, persisted device identity, persisted connectivity identity, pairing/trusted client state, tmux workspace management, doctor/status reporting, relay device connector, and connectivity connector
- `internal/tunnel/session/`: PTY ownership, local terminal handling, hub fanout, resize state, and terminal mirror
- `internal/tunnel/connector/`: outbound relay connection, session registration, attach routing, and resize signaling
- `cmd/relay`: relay entrypoint
- `internal/config/`: relay process configuration loaded during startup
- `internal/logx/`: global structured logging setup and helpers
- `internal/relay/auth/`: invite code rules, username/password normalization, app-session flows, and agent-token flows
- `internal/relay/operator/`: operator invite and user-maintenance services
- `internal/relay/device/`: transient online-device routing, owner metadata, and launch-request coordination
- `internal/relay/session/`: live session registry, owner metadata, stop control routing, and attach-session indexing
- `internal/relay/handler/`: Gin router assembly plus subpackages for middleware, REST API, agent WebSocket flows, device WebSocket flows, attach WebSocket flows, shared request helpers, and HTTP DTOs
- `internal/migration/`: relay schema migration runner and `schema_migrations` tracking for legacy/local workflows
- `internal/relay/store/postgres/`: PostgreSQL-backed auth and operator persistence

Docker Compose relay deployments initialize fresh PostgreSQL volumes from `deploy/postgres/latest.sql` and do not run automatic migrations against existing databases. Existing deployed databases require operator-run SQL for schema changes, and the full snapshot must stay current whenever the durable schema changes.
- `internal/protocol/`: shared session-attach, device-launch, and session-stop wire types

## Related Documents

- [docs/api.md](./api.md)
- [docs/protocol.md](./protocol.md)
- [docs/tui-attach-flow.md](./tui-attach-flow.md)
