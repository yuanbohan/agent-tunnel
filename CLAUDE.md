# CLAUDE.md

This file provides guidance to coding agents when working in this repository.

During brainstorming and spec phases, avoid writing code whenever possible; implementation should happen in the subsequent implementation phase.

## Start Here

- The main product is the `tunnel` CLI with strict relay startup requirement and automatic reconnect with backoff after startup.
- `cmd/tunnel` builds the `tunnel` CLI. It launches a PATH-resolved CLI command, keeps the local terminal interactive, and maintains the authoritative headless terminal mirror for the current PTY session.
- `cmd/relay` is the standalone relay server. It exposes authenticated HTTP and WebSocket APIs for external clients, authenticates app clients with bearer app sessions, authenticates agents with user-owned bearer agent tokens, keeps operator maintenance routes local-only outside the public `/api/` namespace, persists accounts and auth state in PostgreSQL, and maintains live in-memory routing only for online sessions and online device daemons. It does not retain transcript history. It starts via explicit subcommands such as `serve`, `invite create`, `invite disable`, and `user delete`.
- `cmd/migrate` builds the standalone relay schema migrator used by legacy/local PostgreSQL schema workflows. Docker Compose relay deployments do not run it automatically.
- `internal/tunnel/session/` owns PTY lifecycle, Hub fanout, local terminal attach, resize/input forwarding, and the terminal mirror used for attach snapshots.
- `internal/tunnel/daemon/` owns the explicit background daemon started by `tunnel daemon ...`, including the local control socket, persisted device identity, dedicated tmux workspace, doctor/status behavior, and device-side relay connector.
- `internal/protocol/` defines attach-oriented wire types: agent registration, attach control, optional submit anchors on snapshot completion, live submit-anchor controls, session info, structured input, and client-routed terminal-byte packets.
- `internal/protocol/` also defines device-oriented wire types for device registration and launch request/result routing.
- `internal/tunnel/connector/` is the mandatory outbound connector from a local `tunnel` process to `/agent/ws` on the relay. It registers sessions, publishes resize metadata, answers attach-open/attach-close control, records submit anchors for future fresh attaches, emits live submit anchors for attached clients, and routes client-scoped terminal bytes.
- `internal/relay/auth/` owns invite codes, usernames/passwords, app sessions, and agent token services.
- `internal/relay/operator/` owns operator-only invite and user maintenance services.
- `internal/relay/device/` owns live in-memory online-device routing, device listing, and launch-request coordination.
- `internal/relay/session/` owns live session ownership, stop control routing, attach routing, and attach-session indexing.
- `internal/config/` owns relay process configuration loaded during relay startup.
- `internal/logx/` owns the global structured logger setup used across the relay.
- `internal/relay/handler/` owns the Gin router, HTTP middleware, REST handlers, and WebSocket transport split by API, agent, device, and attach concerns.
- `internal/migration/` owns the relay schema migration runner and migration tracking logic for legacy/local workflows.
- `internal/relay/store/postgres/` owns PostgreSQL persistence for relay auth and operator state.
- `internal/tunnel/launcher/` is the thin PATH resolution layer for the user-provided launcher command.
- `internal/buildinfo/` owns shared tunnel/relay version metadata and compatibility-line helpers used by release builds, public manifests, and version reporting.
- `docs/api.md` is the current public app-facing relay API reference, including auth, request and response shapes, and error contracts.
- `docs/daemon.md` is the daemon-specific implementation contract for lifecycle, tmux workspace ownership, workspace close behavior, launch validation, launch health, and failure reasons.
- `docs/architecture.md` describes how all Go packages and relay-facing protocols interact.
- `docs/release-distribution.md` describes the private-source/public-distribution release workflow for `tunnel`.

## Current Product Boundaries

- `session_id` identifies one running `tunnel` process. Relay reconnects for that same process keep the same `session_id`. A fresh agent launch gets a new `session_id`.
- `tunnel` is the PTY owner. It has no localhost HTTP server; all remote client access goes through the relay.
- `tunnel run` uses `--base-url` (or `TUNNEL_BASE_URL`) plus runtime auth resolved as `TUNNEL_AUTH_TOKEN` first and `~/.tunnel/auth.json` second. `TUNNEL_BASE_URL` is optional and defaults to `https://diaro.me`.
- Interactive `tunnel run` also performs one native binary update check at most once per 24-hour interval unless `TUNNEL_UPDATE_DISABLED=1` is set directly or through `~/.tunnel/settings.json`.
- `tunnel update` installs the latest official release in place, and `tunnel rollback` re-downloads the previously recorded official release after one successful official-to-official upgrade.
- On launch, `tunnel` must complete relay registration within the startup wait window. If registration does not succeed, startup fails and the local terminal session does not start.
- After startup, relay unavailability must not interrupt local terminal work. The connector keeps retrying relay registration with backoff, and local sessions continue running unchanged while remote visibility and input are unavailable.
- The agent is the authority for current terminal state. It maintains the headless terminal mirror and produces attach snapshots from that mirror.
- Remote viewing is session-scoped: clients discover sessions with `GET /api/sessions` and attach with `GET /api/sessions/:id/attach/ws`.
- Session discovery includes metadata such as `git_branch`, optional local daemon `device_id`, relay-controlled `launch_source`, `platform_family`, `platform_id`, and normalized `computer_name`.
- Remote launch is device-scoped: clients discover devices with `GET /api/devices` and request new session creation with `POST /api/devices/:id/launch`, which requires per-launch `cwd`, may include optional `label`, and succeeds only when the new session becomes `session_ready`. Session shutdown is the unified `POST /api/sessions/:id/stop` operation for any owned live session.
- Browser attach clients must be same-origin with the relay host; native clients that omit `Origin` remain supported.
- Remote recovery in this revision is fresh snapshot recovery of the current terminal state, including up to 10,000 lines of bounded agent-local normal-buffer scrollback and bounded submit anchors when available. Already attached clients can also receive live `submit_anchor` controls for newly recorded submit Enter events. There is no transcript replay API and no global live-output websocket contract.
- The relay stores live session metadata, owner connection state, and active attach routing state. It must not be described as retaining transcript history or terminal state.
- The relay only keeps transient routing state for currently connected `/device/ws` daemons and the in-flight correlation needed to turn one launch request into one `session_ready` result or timeout. Device health, tmux workspace details, stop history, and last failure remain daemon-local.
- `tunnel run` includes `device_id` in session registration when it can read an existing daemon identity from local daemon state; otherwise the field is an empty string. The relay stores the registered `device_id` without launch-request validation.
- The agent-side mirror may retain up to 10,000 lines of bounded in-memory normal-buffer scrollback and bounded submit anchors for attach snapshots. That is agent-local state, not relay-owned or durable history.
- The local terminal remains the most complete source of truth for session output in the current product revision.
- A successful attach yields `attached`, snapshot bytes, `snapshot_done`, then live PTY bytes on the same websocket. `snapshot_done` may include optional `submit_anchors`; each anchor is a content-free submit navigation hint with a line relative to the restored snapshot buffer. Already attached clients may receive live `submit_anchor` controls for new submit anchors; live anchor lines are interpreted against the current attached terminal state when the control is received.
- If the owning agent disconnects, the relay removes that session from discovery immediately. If the same running agent reconnects later with the same `session_id`, the session becomes discoverable again.
- If an app session logs out or a password change revokes app sessions, the relay closes the affected app-side attaches but does not disconnect the owning agent session.
- Relay state is live-only and in-memory. If the owning agent socket disappears, the relay removes the session immediately.
- Protocol-facing timestamps such as `started_at` are Unix timestamps encoded as JSON integer seconds.
- The relay is content-opaque. It may forward output bytes, attach control, snapshot submit-anchor metadata, and live submit-anchor metadata generated by the owning agent, but it must not emulate the terminal or derive previews, anchors, or other message semantics from terminal content.
- PTY size remains local-terminal-owned in this phase. Remote clients follow forwarded resize events and do not become size authority.
- Structured remote input remains `input_text` and `input_key`, with PTY-byte translation owned by `tunnel`. Local-terminal input and remote attach input that send the `ENTER` carriage return outside bracketed-paste regions may create agent-local submit anchors for future snapshots and live attached clients; draft text and non-Enter special keys do not create anchors.
- The relay does not ship a bundled frontend. Any UI or client experience is owned by external clients such as the mobile app.
- Public `tunnel` binary distribution lives in `yuanbohan/tunnel`, which is a distribution-only repo with stable `install.sh`, `latest.json`, and GitHub Releases assets, including `checksums.txt` for native self-update integrity checks.
- Persistent CLI-owned local state for Tunnel lives under `~/.tunnel/`: `auth.json` for saved auth fallback, `settings.json` for user-editable settings env overrides, and internal `updater.json` for cadence and rollback bookkeeping.
- Official Tunnel and Relay releases are dispatched through the private repo `Release` workflow with an explicit product selection. Source tags are product-prefixed (`tunnel-vX.Y.Z` or `relay-vX.Y.Z`), but published Tunnel versions and Relay image tags remain plain `vX.Y.Z`.
- Local `make install` builds and installs development binaries only; it must not create, increment, or push release tags and must not mark binaries as official releases.
- Docker Compose relay deployment uses the GHCR Relay image plus PostgreSQL in Compose. Compose initializes fresh PostgreSQL volumes from `deploy/postgres/latest.sql`, persists relay structured logs under `/opt/agentunnel/logs/relay/relay.log`, and does not run automatic migrations against existing databases.
- Production Relay operations are Docker Compose only. Runtime Relay/PostgreSQL secrets live in remote `/opt/agentunnel/compose/.env`; local Ansible secret files are for deploy-only secrets such as GHCR login and certbot email, not duplicate runtime env values.
- `deploy/postgres/latest.sql` is the complete current PostgreSQL schema snapshot. Every PostgreSQL schema change must update this file so a fresh database can be fully recreated from it.
- Existing deployed PostgreSQL databases are changed manually by an operator running the required SQL on the server. Do not document or implement automatic production schema migration in the Docker Compose deployment path unless the product boundary changes explicitly.
- Tunnel and relay compatibility is guaranteed only within the same compatibility line. For `v1+`, that line is the semver major version. For pre-`v1`, that line is `0.minor`, so `v0.1.x` and `v0.2.x` are different compatibility lines.
- Stronger delivery guarantees may be explored later, but do not document or imply them before they exist in code and protocol.

## Docs Expectations

- Keep `README.md`, `docs/api.md`, `docs/protocol.md`, `docs/daemon.md`, `docs/architecture.md`, `CLAUDE.md`, and `AGENTS.md` aligned with the active attach-based contract and current implementation status when behavior or scope changes.
- If you change app-facing relay auth, public client endpoints, request or response shapes, app-visible error statuses or reasons, or client attach WebSocket message contracts, update `docs/api.md`.
- If you change relay auth, relay lifecycle, client-facing endpoints, or PTY/input behavior, update `docs/architecture.md`.
- If you change PostgreSQL schema, update `deploy/postgres/latest.sql` in the same change and document any manual SQL required for existing deployed databases.
- If you change daemon lifecycle, tmux workspace ownership, workspace close behavior, launch validation, daemon health, local daemon state, or daemon failure reasons, update `docs/daemon.md`.
- If you change attach lifecycle semantics, session-state semantics, `/api/sessions/:id/attach/ws`, `/agent/ws` attach-control messages, or submit-anchor metadata, update `README.md`, `docs/protocol.md`, `docs/architecture.md`, `CLAUDE.md`, and `AGENTS.md`.
- If you change snapshot generation, live-byte delivery, resize ownership, or structured input semantics, update `README.md`, `docs/protocol.md`, `docs/architecture.md`, `CLAUDE.md`, and `AGENTS.md`.
- If you change operator-facing startup flow or environment variables, update `README.md`.
- If you change the public `tunnel` release flow, installer contract, compatibility-line contract, or distribution repo surface, update `README.md`, `docs/release-distribution.md`, `docs/public-distribution-readme.md`, `CLAUDE.md`, and `AGENTS.md`.

## Verification

- `go test ./...`
- `go test ./internal/protocol ./internal/relay/...`
- `make test`
- `make test-relay`
- `make build`
