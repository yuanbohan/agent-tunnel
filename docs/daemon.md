---
title: Tunnel Daemon Implementation Contract
date: 2026-04-21
---

# Tunnel Daemon Implementation Contract

This document is the implementation contract for `tunnel daemon`. It exists to keep the daemon aligned with the tmux-backed device-launch plan and to prevent drift back toward GUI terminal automation, durable relay-owned device state, or daemon-owned PTYs.

## Purpose

`tunnel daemon` lets an authenticated user explicitly opt one machine into remote session launch. It keeps a long-lived device connection to the relay, receives launch requests for that machine, creates new `tunnel run <command>` sessions inside a dedicated local tmux workspace, and reports immediate launch validation results back to the relay.

The daemon does not replace `tunnel run`. Direct local sessions still start through `tunnel run <command>`, own their PTY locally, register with `/agent/ws`, and remain the authority for terminal state.

## Non-Negotiable Boundaries

- Remote launch is device-scoped. Clients discover online devices with `GET /api/devices` and launch through `POST /api/devices/:deviceID/launch`.
- Launch success means `session_ready`, not daemon acceptance. The relay completes the client request only after the launched `tunnel run` registers with a matching launch correlation and concrete `session_id`.
- The daemon must use a dedicated local tmux workspace for remotely launched sessions.
- The daemon must not require or automate GUI terminal applications.
- The daemon must not manage long-lived PTYs directly for remote launch. Tmux owns local session survival across daemon stop, crash, and restart.
- The relay must stay live-only for devices. It stores currently connected device metadata and in-flight launch correlation only.
- The relay must not own tmux state, terminal state, transcript history, launch history, last failures, stop history, or offline device inventory.
- `tunnel daemon open` and `tunnel daemon sessions` must target the tmux workspace directly and must not require the daemon control socket to be online.
- `tunnel daemon close` must only detach one open local view of the daemon tmux workspace. It must not stop the daemon, kill tmux, or terminate a launched session.
- A device may have at most one in-flight launch request. Concurrent launch attempts must fail with `busy` rather than queue.
- Windows support is out of scope for the current daemon implementation.

## CLI Contract

The daemon surface is explicit and lives under `tunnel daemon ...`:

- `start`: start a background daemon process, fail early if tmux is unavailable, and report preserved workspace sessions when applicable.
- `status`: read daemon status without active probing.
- `stop`: stop the daemon process and remove the device from online relay discovery, without killing tmux sessions.
- `doctor`: actively inspect daemon readiness, auth availability, relay reachability, tmux availability, workspace reachability, config readability, and recent launch failure state.
- `open`: attach to an existing daemon-managed tmux session from the current terminal. If no daemon-managed sessions exist, do not open tmux; tell the user there are no sessions to open.
- `close`: detach one currently open client from the daemon tmux workspace. If no workspace view is open, report that there is no open workspace to close and exit successfully.
- `sessions`: list sessions in the dedicated tmux workspace without adding custom session management.
- `pair`: ask the running daemon to create a signed short-lived pairing invitation.
- `devices`: list daemon-local trusted Android devices.
- `revoke <fingerprint>`: mark one daemon-local trusted Android device revoked and best-effort notify Relay live visibility.

Do not add daemon commands that create a custom tmux dashboard, picker, alias system, per-session close/open workflow, or terminal-recipe workflow unless the product scope is explicitly changed first. Use account-level session stop for destructive session shutdown; keep `close` reserved for the local workspace view lifecycle.

## Local State

Daemon-owned local state is split by purpose:

- Config: user-editable daemon config, including the first-token command allowlist.
- State: persisted device identity, connectivity identity, pairing invitations, trusted Android roster, and last daemon status.
- Runtime: local control socket, PID file, and dedicated tmux socket path.

The stable `device_id` belongs to machine-local daemon state. It should survive daemon restarts and be reused when the same machine daemon reconnects.

The connectivity identity is a separate long-lived Ed25519 identity stored in `connectivity_identity.json` with file mode `0600`. Its SHA-256 public-key fingerprint is exposed as `daemon_fingerprint` in daemon status and connectivity registration. It is the pairing trust root and future QUIC pinning identity; it is distinct from the legacy `device_id` routing/display identifier.

Pairing state is stored in `pairing_state.json` with file mode `0600`. It contains short-lived invitation records, consumed invitation markers retained until expiry, pending Android responses awaiting SAS confirmation, and the trusted Android roster. Relay does not own this durable trust state.

`tunnel daemon pair` requires the local daemon control socket and a live connectivity Relay reservation. The daemon receives the Relay-authenticated account id for the reservation, signs the invitation transcript with its connectivity identity, persists invitation state, and returns the JSON invitation payload to the CLI. QR rendering is not implemented in this revision.

When Relay forwards a signed Android pairing response, the daemon verifies it and stores it as a pending response. `tunnel daemon pair pending` lists pending responses and their derived SAS values; `tunnel daemon pair confirm <invitation-id> <sas>` consumes the invitation, stores Android trust, and sends `pair_completed` to Relay when the connectivity socket is online. A mismatched SAS consumes the invitation without storing trust.

`tunnel daemon revoke <fingerprint>` updates daemon-local trust first. If the connectivity Relay socket is online, the daemon sends `paired_device_revoked`; if Relay is offline, the next daemon connectivity registration omits the revoked fingerprint, so Relay cannot rebuild visibility for that Android device.

The tmux socket path is part of the workspace identity. Daemon shutdown must remove daemon runtime control state, but must not kill the tmux server or delete surviving tmux sessions.

## Tmux Workspace Contract

The daemon-managed workspace must be isolated from the user's default tmux environment by always passing the dedicated socket path.

Expected tmux behavior:

- `start` counts existing sessions and preserves them.
- `open` attaches when daemon-managed sessions exist and reports a no-session message when none exist.
- `close` detaches one attached tmux client from the dedicated daemon socket when one exists. It does not kill the tmux server, a tmux window, or any tmux session.
- `sessions` lists only sessions on the dedicated daemon socket.
- Each remote launch creates one detached tmux session with an opaque session name.
- The requested `cwd` is passed as the tmux session working directory.
- The launched command runs through a shell wrapper that starts `tunnel run <command>`, restores relevant environment variables, and then execs an interactive login shell so the tmux session remains available after `tunnel run` exits.

The implementation must not use the user's default tmux socket, infer desktop terminal state, or depend on the terminal that originally started the daemon.

## Launch Flow

The daemon-side launch handler owns immediate local validation:

1. Reject if another launch is already in flight.
2. Require `tmux`.
3. Parse the command string and validate the first token against the daemon allowlist.
4. Resolve and validate the per-request `cwd`.
5. Require the `tunnel` executable to be available in the daemon launch environment.
6. Create one tmux-backed launch session with scoped `TUNNEL_BASE_URL` and `TUNNEL_AUTH_TOKEN`, hidden `tunnel run --launch-source mobile --launch-request-id <id>` metadata, optional `--label`, and the requested command.
7. Return `accepted` only after the tmux session is created.

After `accepted`, the relay waits for the launched `tunnel run` process to register through `/agent/ws` with a `launch_context` containing `source: "mobile"` and the same request id. The relay returns `session_ready` only when that registration supplies the new `session_id`.

Launch source metadata must not be passed through environment variables. It is carried as internal `tunnel run` flags and then as the agent registration `launch_context`; the relay validates the context before exposing `launch_source: "mobile"` to clients.

The mobile/API launch flow must not auto-attach to the new session. Clients may use normal session discovery and attach APIs after launch completes.

## Session Stop Flow

Mobile and CLI session stop is a separate destructive operation from local workspace close.

1. The launched `tunnel run` process registers on `/agent/ws` with the launch correlation.
2. The relay marks the registered session with `launch_source: "mobile"` when the launch correlation matches.
3. `GET /api/sessions` exposes mobile launch source separately from local launch source.
4. `POST /api/sessions/:sessionID/stop` routes one `stop_session` control frame to the owning `/agent/ws` connection.
5. The owning `tunnel run` process stops itself. For daemon-launched sessions, this stops the process inside the tmux workspace session.
6. The relay removes the live session from discovery immediately after accepting stop and closes active attaches with `session_stopped`.

Local-launched and mobile-launched `tunnel run` sessions use the same stop path. `device_id` identifies the machine daemon identity, not proof that the session was mobile-launched.

## Failure Reasons

Structured failure reasons are part of the client-visible contract. Keep them stable and specific:

- `device_offline`: relay could not route to the requested online daemon, or the daemon disconnected before the request completed.
- `busy`: the device already has one launch in flight.
- `command_not_allowed`: command parsing failed, no command token was supplied, config could not be loaded, or the first command token is not allowed.
- `path_not_found`: the supplied `cwd` is missing, empty, or not usable as a directory.
- `tmux_not_found`: tmux is unavailable.
- `tunnel_not_found`: the daemon launch environment cannot find `tunnel`.
- `session_start_failed`: tmux session creation failed after validation.
- `launch_timeout`: the relay did not observe matching `session_ready` registration before the launch wait expired.

Do not collapse these into generic errors. If a new failure mode becomes client-visible, update `docs/api.md`, `docs/protocol.md`, and this document together.

## Health Model

`launch_health` is daemon-reported live metadata. Current values are:

- `healthy`: the daemon currently believes remote launch is available.
- `degraded`: recent local failures indicate launch may not work.

Only workspace or launch-substrate failures should degrade launch health. Validation failures caused by the request itself, such as disallowed command or invalid cwd, may update `last_failure` but should not mark the device globally degraded.

The relay only reflects the latest live device metadata. It must clear device presence on disconnect rather than retaining stale health.

## Security And Auth

Daemon start uses the same runtime auth precedence as `tunnel run`: `TUNNEL_AUTH_TOKEN` first, then saved local auth in `~/.tunnel/auth.json`.

The daemon authenticates to `/device/ws` and `/connectivity/daemon/ws` with a user-owned agent token. If that token is revoked, the relay must close matching device sockets and remove those devices from discovery.

Launch command authorization is intentionally narrow: only the first parsed command token is checked against the daemon allowlist. The rest of the command string is passed as launcher arguments after shell parsing. Any future expansion to command profiles, placeholders, per-command schemas, or interactive confirmation is outside this contract until separately specified.

## Documentation Requirements

When daemon behavior changes, keep these files aligned:

- `docs/daemon.md`
- `README.md`
- `docs/api.md`
- `docs/protocol.md`
- `docs/architecture.md`
- `CLAUDE.md`
- `AGENTS.md`

Update `docs/api.md` and `docs/protocol.md` for any app-visible endpoint, request/response, websocket frame, `launch_health`, or failure-reason change.

Update `docs/architecture.md` for ownership, lifecycle, relay boundary, tmux workspace, or auth changes.

Update `README.md` for user-facing CLI behavior, prerequisites, and remote-launch workflow changes.

## Verification

Focused daemon changes should run at least:

```sh
go test ./internal/tunnel/daemon ./internal/protocol ./internal/relay/device ./internal/relay/handler ./cmd/tunnel
```

Broader behavior or protocol changes should also run:

```sh
go test ./...
make test
make test-relay
make build
```

Tests should cover direct daemon code and the relay boundary that turns launch acceptance into `session_ready`. Unit tests that only assert tmux command construction are not enough for changes that alter launch correlation, failure reasons, or device discovery.
