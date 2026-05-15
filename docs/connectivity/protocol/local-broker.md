# Local Session Registration Protocol

## Status

This document defines the phase-1 local protocol between the daemon and each local `tunnel run` process.

Step 3 implemented the local registration and preview subset. The current broker also carries snapshot, live-byte, input, and resize frames for trusted connectivity transports.

Its purpose is narrow:

- keep `tunnel run` as the PTY owner
- let daemon expose those sessions to mobile clients
- avoid making daemon the session owner

This is a local-machine protocol only. It is not exposed to Relay.

## Core Rule

Each `tunnel run` process opens one long-lived local connection to the daemon and explicitly registers itself.

That daemon-local connection is authoritative for:

- whether the session is alive
- the session's current metadata
- the session's latest preview
- interactive snapshot and live bytes

If the local connection disappears, the daemon must treat that session as gone.

## Transport

Phase-1 transport should be:

- Unix domain socket
- dedicated daemon-owned listener for long-lived session registrations
- one long-lived connection per `tunnel run`

The daemon accepts only local clients on the same machine.

This listener is separate from the daemon's short-lived local control RPC socket. The current socket filename is `broker.sock` under the daemon runtime directory, created with owner-only permissions.

Recommended phase-1 hardening:

- daemon creates the socket path with owner-only permissions
- daemon creates the broker socket path with owner-only permissions
- `tunnel run` refuses to talk to a broker socket path it does not own through the current local user boundary on Linux/macOS
- `tunnel run` verifies daemon control status still reports the expected Relay base URL and auth-context fingerprint before every broker connect or reconnect
- daemon closes local broker connections that do not complete an initial `register_session` within the registration timeout
- daemon peer-credential checks beyond owner-only socket/path checks are future hardening

## Ownership Model

`tunnel run` owns:

- PTY lifecycle
- terminal mirror
- preview generation
- interactive attach semantics
- PTY input handling

daemon owns:

- local session directory
- mapping `session_id -> local connection`
- forwarding preview and interactive traffic between mobile transport and the owning `tunnel run`

## Connection Lifecycle

### Session Startup

When `tunnel run` starts:

1. it opens the local daemon socket
2. it sends `register_session`
3. it waits for the broker to acknowledge that the session id was accepted
4. it keeps the connection open for the lifetime of the session

Startup must fail before terminal prep and child process launch if the daemon or broker cannot accept the registration. After startup, broker reconnect remains best-effort with backoff so local terminal work can continue through daemon restarts.

### Daemon Restart

If the daemon restarts:

- the local socket disappears
- each still-running `tunnel run` should reconnect automatically
- before reconnecting, `tunnel run` verifies the daemon still runs against the expected Relay base URL and auth-context fingerprint
- after reconnect it sends a fresh `register_session`

Until that reconnect succeeds, the session is temporarily absent from the daemon's mobile-visible roster.

### Session Exit

When `tunnel run` exits normally:

- it should send `session_gone` if possible
- then close the local connection

If the process crashes or the local connection drops unexpectedly:

- daemon should treat the session as gone on connection loss

## Message Families

The phase-1 local protocol should stay minimal.

### `tunnel run` -> daemon

- `register_session` (implemented in Step 3)
- `session_update` (implemented in Step 3)
- `preview_update` (implemented in Step 3)
- `interactive_granted`
- `interactive_denied`
- `snapshot_begin`
- `snapshot_chunk`
- `snapshot_end`
- `live_bytes`
- `session_gone` (implemented in Step 3)

### daemon -> `tunnel run`

- `register_ack` (implemented in Step 3 and required during startup)
- `interactive_request`
- `interactive_release`
- `input_text`
- `input_key`
- `resize`

## Required Fields

### `register_session`

Minimum fields:

- `session_id`
- `label`
- `command_preview`
- `cwd`
- `git_branch`
- `started_at`
- `updated_at`
- `online`

This is the message that makes a session visible to the daemon's mobile-facing roster.

### `register_ack`

Minimum fields:

- `type: "register_ack"`
- `session_id`

The daemon sends this after it accepts a `register_session` frame and records that `session_id` in the live broker roster. `tunnel run` waits for this acknowledgement during startup before terminal prep and child process launch.

### `session_update`

Carries a full metadata refresh for the same session.

Phase 1 should prefer full replacement payloads over patch-style deltas.

### `preview_update`

Carries the newest preview text for the session.

Important rule:

- `tunnel run` pushes preview updates to daemon proactively
- daemon caches only the latest preview per session
- daemon does not need a second local preview subscription state machine

### `snapshot_update` and `output_bytes`

`tunnel run` also publishes coalesced latest terminal snapshots and incremental live output bytes over the same daemon-local broker socket for trusted connectivity transports. Snapshot updates are throttled with preview publishing and replace the cached latest snapshot; live output frames carry only the incremental bytes.

### `interactive_request`

Sent by daemon when a mobile client wants interactive access to the session.

Minimum fields:

- `session_id`
- `cols`
- `rows`

Phase-1 rule:

- at most one active interactive lifetime exists per `session_id`
- if another request arrives while one lifetime is still active for that same session, `tunnel run` should reject it with `daemon_busy`

### `interactive_granted`

Sent by `tunnel run` after it accepts the interactive request.

This means the broker accepted the daemon as the active interactive owner for that session. The connectivity transport then writes the initial snapshot and live bytes on the per-interactive stream:

- `snapshot_begin`
- `snapshot_chunk`
- `snapshot_end`
- `live_bytes`

The `interactive_granted` dimensions must match the following `snapshot_begin` dimensions. If the broker already has a cached terminal snapshot, those cached dimensions are authoritative; otherwise the requested dimensions are echoed.

### `interactive_denied`

Sent by `tunnel run` when it rejects the request.

Phase-1 recommended reasons:

- `device_not_trusted`
- `session_unavailable`
- `daemon_busy`
- `unknown`

### `interactive_release`

Sent by daemon when the mobile client leaves or explicitly releases interactive.

### `input_text` / `input_key` / `resize`

Sent by daemon only after interactive has been granted for that session. `input_text` and `input_key` are routed to the owning `tunnel run` input path; `resize` updates the PTY size and publishes a coalesced `snapshot_update`.

`tunnel run` should drop these messages if no active interactive lifetime exists for that session.

## Preview Pipeline

Preview is generated by the owning `tunnel run`, cached by the daemon, fanned out to subscribed paired devices. Relay never sees preview content. Android never derives preview from raw terminal output.

### Source: `tunnel run`

`tunnel run` extracts preview from the terminal mirror it already maintains. It pushes updates to the daemon proactively over the local socket using `preview_update`. There is no daemon-side pull request; this avoids a per-frame ask-response loop.

### Cache: daemon broker

- daemon keeps **only the latest** `preview_update` per session
- no history, no deltas
- daemon reapplies preview normalization and the current length limit before caching, even though the official `tunnel run` client already sends bounded previews
- on a fresh paired-device subscription in Step 4, the daemon will serve the cached latest preview immediately
- subsequent `preview_update` events will fan out to all currently subscribed paired devices in Step 4

### Fanout: paired devices

- Android explicitly subscribes via `preview_subscribe` on the QUIC control stream
- daemon emits `preview_snapshot` to every subscribing device when the cached preview changes
- daemon does not consult account tier; trusted-computer policy is an Android-side decision (see `../ux/subscription.md`)

This fanout is not implemented in Step 3.

### Preview Shape

Recommended phase-1 defaults for `preview_update` content produced by `tunnel run`:

- plain text only — strip ANSI control sequences
- bounded length — current implementation keeps at most 2,000 recent preview characters
- normalized whitespace for list rendering, including collapsed horizontal whitespace
- not terminal emulation

### Update Cadence

- event-driven: emit when PTY output updates the local mirror
- lightly throttled to avoid flooding under chatty output; the current local client sends at most one preview per 250ms throttle window while output is continuous
- always send the current snapshot, not a diff
- phase 1 optimizes for correctness and simplicity over bandwidth

### Empty Preview

Empty preview is valid and SHOULD be tolerated by the Android UI:

- brand new session with no output yet
- session metadata published before the first preview update
- output not yet meaningful for preview

### Phase-1 App Implications

Because the cache is fresh and per-session:

- Free and Pro use the same session roster and preview protocol inside an active trusted computer
- the app subscribes to previews according to UI and performance needs, not tier entitlement
- tier policy decides which trusted computers the app may connect to, not which sessions are usable inside the computer

This matches the intended UX:

- all sessions remain visible and attachable inside an active trusted computer
- preview behavior is consistent across Free and Pro for a single computer

## Failure Rules

- unknown local `session_id` messages must be ignored and logged
- daemon must remove a session from its roster on local connection loss
- `tunnel run` should retry reconnect with backoff if daemon is temporarily unavailable
- first startup registration is required; reconnect after a successful startup is best-effort
- daemon should treat duplicate `register_session` for the same `session_id` as replacement of the old local connection
- daemon must remove a previous session if one local connection re-registers with a different `session_id`
- broker writes are best-effort and bounded so preview/session-gone delivery cannot block local `tunnel run` shutdown indefinitely

## Security Boundary

This protocol is local-only and phase-1 assumes the same local user boundary that already exists for `tunnel run` and daemon.

Relay base URL and auth-context fingerprint matching prevent a still-running `tunnel run` from reconnecting to a daemon that restarted against a different Relay host or resolved auth token. The fingerprint is local mismatch detection only; Step 4 fanout must still enforce paired-device visibility and transport authorization before paired devices can observe broker previews.

It is not yet a cross-account security boundary. The strong security boundary remains:

- pairing trust
- QUIC/TLS transport identity pinning
- Relay-authenticated app login

## References

- `../architecture.md`
- `../contract.md`
- `transport.md`
- `../reference/sequence-flows.md`
