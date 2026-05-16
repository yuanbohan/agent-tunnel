# CLAUDE.md

This file provides guidance to coding agents when working in this repository.

During brainstorming and spec phases, avoid writing code whenever possible; implementation should happen in the subsequent implementation phase.

## Start Here

- Cross-repository protocol decisions live in `https://github.com/yuanbohan/agent-tunnel-protocols`. This repository keeps implementation-specific mirrors, public Relay API docs, daemon behavior docs, and operational guidance aligned with that protocol source of truth.
- The main product is the `tunnel` CLI with strict relay startup requirement and automatic reconnect with backoff after startup.
- `cmd/tunnel` builds the `tunnel` CLI. It launches a PATH-resolved CLI command, keeps the local terminal interactive, and maintains the authoritative headless terminal mirror for the current PTY session.
- `cmd/relay` is the standalone relay server. It exposes authenticated HTTP and WebSocket APIs for external clients, authenticates app clients with bearer app sessions, authenticates agents with user-owned bearer agent tokens, keeps operator maintenance routes local-only outside the public `/api/` namespace, persists accounts and auth state in PostgreSQL, and maintains live in-memory routing only for online sessions and online device daemons. It does not retain transcript history. It starts via explicit subcommands such as `serve`, `invite create`, `invite disable`, and `user delete`.
- `cmd/migrate` builds the standalone relay schema migrator used by legacy/local PostgreSQL schema workflows. Docker Compose relay deployments do not run it automatically.
- `internal/tunnel/session/` owns PTY lifecycle, Hub fanout, local terminal attach, resize/input forwarding, and the terminal mirror used for attach snapshots.
- `internal/tunnel/daemon/` owns the background daemon started by `tunnel daemon start` or required from `tunnel run`, including the local control socket, local broker socket and live roster/cache, persisted device identity, persisted connectivity identity, trusted client pairing state, dedicated tmux workspace, doctor/status behavior, device-side relay connector, connectivity relay connector, and daemon-side direct UDP rendezvous listener.
- `internal/protocol/` defines relay-facing wire types for agent registration, launch readiness, session metadata, device registration, and launch request/result routing.
- `internal/protocol/` also defines device-oriented wire types for device registration and launch request/result routing.
- `internal/tunnel/connector/` is the mandatory outbound connector from a local `tunnel` process to `/agent/ws` on the relay. It registers sessions and emits mobile launch readiness after local broker and PTY startup. It does not carry terminal bytes, attach control, input, or resize as a Relay data plane.
- `internal/relay/auth/` owns invite codes, usernames/passwords, app sessions, and agent token services.
- `internal/relay/operator/` owns operator-only invite and user maintenance services.
- `internal/relay/device/` owns live in-memory online-device routing, device listing, and launch-request coordination.
- `internal/relay/connectivity/` owns live in-memory connectivity app/daemon peers, paired-daemon visibility derived from daemon-local trusted rosters, short-lived pairing response correlations, short-lived direct rendezvous attempts, and fallback tunnel token state.
- `internal/relay/session/` owns live session ownership and launch-correlation state for online `/agent/ws` connections.
- `internal/config/` owns relay process configuration loaded during relay startup.
- `internal/logx/` owns the global structured logger setup used across the relay.
- `internal/relay/handler/` owns the Gin router, HTTP middleware, REST handlers, and WebSocket transport split by API, agent, device, and connectivity concerns.
- `internal/migration/` owns the relay schema migration runner and migration tracking logic for legacy/local workflows.
- `internal/relay/store/postgres/` owns PostgreSQL persistence for relay auth and operator state.
- `internal/tunnel/launcher/` is the thin PATH resolution layer for the user-provided launcher command.
- `internal/buildinfo/` owns shared tunnel/relay version metadata used by release builds and version reporting.
- `docs/api.md` is this repository's current public app-facing relay API implementation reference, including auth, request and response shapes, and error contracts.
- `docs/daemon.md` is the daemon-specific implementation contract for lifecycle, tmux workspace ownership, workspace close behavior, launch validation, launch health, and failure reasons.
- `docs/architecture.md` describes how all Go packages and relay-facing protocols interact.
- `docs/release-distribution.md` describes the private-source/public-distribution release workflow for `tunnel`.

## Current Product Boundaries

- `session_id` identifies one running `tunnel` process. Relay reconnects for that same process keep the same `session_id`. A fresh agent launch gets a new `session_id`.
- `tunnel` is the PTY owner. It has no localhost HTTP server; all remote client access goes through the relay.
- `tunnel run` uses `--base-url` (or `TUNNEL_BASE_URL`) plus runtime auth resolved as `TUNNEL_AUTH_TOKEN` first and `~/.tunnel/auth.json` second. `TUNNEL_BASE_URL` is optional and defaults to `https://agentunnel.cn`. It requires a same-base-URL and same-auth-context local daemon, waits for the daemon broker to accept the session id before Relay startup gating and user command startup, verifies that base URL and non-secret auth-context fingerprint before broker reconnects, and registers session metadata, latest preview, coalesced terminal snapshots, and live output bytes over the daemon broker socket.
- Interactive `tunnel run` also performs one native binary update check at most once per 24-hour interval unless `TUNNEL_UPDATE_DISABLED=1` is set directly or through `~/.tunnel/settings.json`.
- `tunnel update` installs the latest official release in place, and `tunnel rollback` re-downloads the previously recorded official release after one successful official-to-official upgrade.
- On launch, `tunnel` must complete relay registration within the startup wait window and daemon broker registration before terminal prep. If either startup registration does not succeed, the local terminal session does not start.
- After startup, relay unavailability must not interrupt local terminal work. The connector keeps retrying relay registration with backoff, and local sessions continue running unchanged while remote visibility and input are unavailable.
- The agent is the authority for current terminal state. It maintains the headless terminal mirror and publishes daemon-local broker snapshots/output for trusted daemon transports.
- Relay session list, stop, and attach APIs are removed from the current product contract. Computers do not share session data through Relay.
- Official mobile companion session authority after launch is daemon-transport-owned: Relay supplies auth, account policy, pairing, computer presence, rendezvous, fallback tunnel setup, and computer launch; daemon connectivity transport supplies session roster, previews, terminal snapshots/live bytes, input, resize, and mobile session detail.
- Connectivity pairing visibility is device-fingerprint scoped: fingerprint-bound app sessions connect to `GET /api/connectivity/ws`, computer daemons connect to `GET /connectivity/computer/ws`, and Relay derives live visibility from daemon `pair_completed` events plus the daemon-reported trusted client roster on reconnect. Legacy aliases `GET /api/connectivity/app/ws` and `GET /connectivity/daemon/ws` are removed.
- Connectivity direct attempts use `rendezvous_open`, `rendezvous_hint`, and `rendezvous_close` over the existing app/daemon realtime sockets. Relay stores this state live-only and must not derive terminal/session semantics from candidate hints.
- Session discovery includes metadata such as `git_branch`, optional local daemon `device_id`, relay-controlled `launch_source`, `platform_family`, `platform_id`, and normalized `computer_name`.
- Remote launch is computer-scoped: clients discover computers with `GET /api/computers` and request new session creation with `POST /api/computers/:id/sessions`, which requires per-launch `cwd`, may include optional `label`, and succeeds only when the new session registers with the local broker, registers with Relay, starts its PTY process, and sends `launch_ready`. For the official mobile companion, `session_ready.session_id` is a control-plane correlation key; the visible session row/detail and interactive traffic must come from daemon transport `session_index` or `session_upsert`, not Relay session APIs. Session shutdown is local-computer scoped through `tunnel session stop <session-id>` and the daemon broker. Legacy `GET /api/devices` and `POST /api/devices/:id/launch` aliases are removed.
- Remote recovery and interactive terminal traffic for the official mobile companion are daemon-transport-owned. Relay has no global live-output websocket contract and no transcript replay API.
- The relay stores live owner/correlation state for online agent sessions. It must not be described as retaining transcript history, terminal state, active attach routing state, or account-wide session rows.
- The relay only keeps transient routing state for currently connected `/device/ws` daemons and the in-flight correlation needed to turn one launch request plus matching agent `launch_ready` into one `session_ready` result or timeout. Device health, tmux workspace details, broker roster/cache, stop history, and last failure remain daemon-local.
- `tunnel run` includes `device_id` in session registration when it can read an existing daemon identity from local daemon state; otherwise the field is an empty string. The relay stores the registered `device_id` without launch-request validation.
- The agent-side mirror may retain bounded in-memory terminal state for daemon-local snapshots. That is agent-local state, not relay-owned or durable history.
- The local terminal remains the most complete authority for session output in the current product revision.
- If the owning agent disconnects, the relay removes that session from discovery immediately. If the same running agent reconnects later with the same `session_id`, the session becomes discoverable again.
- If an app session logs out or a password change revokes app sessions, the relay closes the affected app connectivity peers but does not disconnect the owning agent session.
- Relay state is live-only and in-memory. If the owning agent socket disappears, the relay removes the session immediately. Connectivity rendezvous attempts also disappear on expiry, superseding attempts, disconnect, logout, token revocation, or trusted-device revocation.
- Connectivity trust remains daemon-local. Relay does not persist trusted client rosters; daemon reconnect rebuilds live visibility from `pairing_state.json`.
- Protocol-facing timestamps such as `started_at` are Unix timestamps encoded as JSON integer seconds.
- The relay is content-opaque. It may forward direct rendezvous candidate hints and encrypted fallback QUIC packets, but it must not emulate the terminal or derive previews, session protocol, path badges, or other message semantics from terminal content. Broker previews are local-only between `tunnel run` and the daemon.
- PTY size remains local-terminal-owned in this phase. Remote clients follow daemon-transport resize/session state and do not become size authority.
- Structured remote input remains `input_text` and `input_key` on daemon transport, with PTY-byte translation owned by `tunnel`.
- The relay does not ship a bundled frontend. Any UI or client experience is owned by external clients such as the mobile app.
- Public `tunnel` binary distribution lives in `yuanbohan/tunnel`, which is a distribution-only repo with stable `install.sh`, `latest.json`, and GitHub Releases assets, including `checksums.txt` for native self-update integrity checks.
- Persistent CLI-owned local state for Tunnel lives under `~/.tunnel/`: `auth.json` for saved auth fallback, `settings.json` for user-editable settings env overrides, and internal `updater.json` for cadence and rollback bookkeeping.
- Official Tunnel and Relay releases are dispatched through the private repo `Release` workflow with an explicit product selection. Source tags are product-prefixed (`tunnel-vX.Y.Z` or `relay-vX.Y.Z`), but published Tunnel versions and Relay image tags remain plain `vX.Y.Z`.
- Local `make install` builds and installs development binaries only; it must not create, increment, or push release tags and must not mark binaries as official releases.
- Docker Compose relay deployment runs PostgreSQL, Relay HTTP/WebSocket, and Binding-only STUN as separate services. Relay and STUN use the same release build artifact published as `ghcr.io/yuanbohan/agent-tunnel-relay` and `ghcr.io/yuanbohan/agent-tunnel-stun`, with independent `RELAY_IMAGE_TAG` and `STUN_IMAGE_TAG` pins. Compose initializes fresh PostgreSQL volumes from `deploy/postgres/latest.sql`, persists relay structured logs under `/opt/agentunnel/logs/relay/relay.log`, persists STUN structured logs under `/opt/agentunnel/logs/relay/stun.log`, and does not run automatic migrations against existing databases.
- Production STUN is exposed directly on UDP `3478` by the Compose `stun` service through `relay stun serve`; nginx remains HTTP/WebSocket only and must not proxy STUN.
- Production Relay operations are Docker Compose only. Runtime Relay/PostgreSQL secrets live in remote `/opt/agentunnel/compose/.env`; local Ansible secret files are for deploy-only secrets such as GHCR login and certbot email, not duplicate runtime env values.
- `deploy/postgres/latest.sql` is the complete current PostgreSQL schema snapshot. Every PostgreSQL schema change must update this file so a fresh database can be fully recreated from it.
- Existing deployed PostgreSQL databases are changed manually by an operator running the required SQL on the server. Do not document or implement automatic production schema migration in the Docker Compose deployment path unless the product boundary changes explicitly.
- Stronger delivery guarantees may be explored later, but do not document or imply them before they exist in code and protocol.

## Docs Expectations

- Keep `README.md`, `docs/api.md`, `docs/protocol.md`, `docs/daemon.md`, `docs/architecture.md`, `CLAUDE.md`, and `AGENTS.md` aligned with the active daemon-transport contract and current implementation status when behavior or scope changes.
- If you change app-facing relay auth, public client endpoints, request or response shapes, app-visible error statuses or reasons, or connectivity WebSocket message contracts, update `docs/api.md`.
- If you change relay auth, relay lifecycle, client-facing endpoints, or PTY/input behavior, update `docs/architecture.md`.
- If you change PostgreSQL schema, update `deploy/postgres/latest.sql` in the same change and document any manual SQL required for existing deployed databases.
- If you change daemon lifecycle, tmux workspace ownership, workspace close behavior, launch validation, daemon health, local daemon state, or daemon failure reasons, update `docs/daemon.md`.
- If you change session-state semantics, daemon transport session messages, `/agent/ws` registration/readiness, or local daemon session management, update `README.md`, `docs/protocol.md`, `docs/architecture.md`, `docs/daemon.md`, `CLAUDE.md`, and `AGENTS.md`.
- If you change daemon snapshot generation, live-byte delivery, resize ownership, or structured input semantics, update `README.md`, `docs/protocol.md`, `docs/architecture.md`, `docs/daemon.md`, `CLAUDE.md`, and `AGENTS.md`.
- If you change operator-facing startup flow or environment variables, update `README.md`.
- If you change the public `tunnel` release flow, installer contract, or distribution repo surface, update `README.md`, `docs/release-distribution.md`, `docs/public-distribution-readme.md`, `CLAUDE.md`, and `AGENTS.md`.

## Verification

- `go test ./...`
- `go test ./internal/protocol ./internal/relay/...`
- `make test`
- `make test-relay`
- `make build`
