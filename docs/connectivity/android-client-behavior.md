# Android Connectivity Client Behavior

## Status

This document captures the target phase-1 Android behavior for the QUIC session-connectivity architecture.

## Core Rules

- login is required before any connectivity feature is available
- Relay provides daemon presence, not sessions
- daemon transport provides sessions, previews, and interactive traffic
- no preview cache in phase 1
- one daemon connection per visible online paired daemon
- interactive state is managed per session, not by a global single-session limit
- subscription enforcement is Relay-owned through active-session leases

## Startup Behavior

Recommended startup order:

1. restore account session
2. connect app realtime WebSocket
3. receive daemon presence snapshot
4. render daemon list
5. for each visible online paired daemon, start a transport connection manager
6. once daemon transport is ready, accept session metadata and preview updates

The app should not pretend session data exists before the daemon transport is actually up.

## Rendering Model

### Daemon List

Relay presence is enough to render:

- daemon display name
- online/offline state
- platform hints

### Session List

Session rows appear only after the daemon has sent `session_index`.

Free-tier session rows that are not currently leased should remain visible but locked.

### Preview

Preview is:

- empty until the daemon sends it for a leased session
- replaced whenever a fresh preview snapshot arrives
- never loaded from local preview cache in phase 1

Locked sessions must not display real preview text.

## Subscription Rendering

Phase-1 free tier UX:

- exactly one session may hold the account's active-session lease at a time
- the leased session renders normally
- all other visible sessions render with a lock icon and light greyed-out styling

Tapping a locked session should show an explanatory prompt and an upgrade path. It should not silently replace the currently leased session.

## Path Badge

The app should always expose the current transport path for each daemon connection:

- `Direct`
- `Relay`

The badge is informational only. Both paths share identical end-to-end encryption and pinned-identity authentication; Relay never sees terminal plaintext on either path. The badge primarily indicates expected latency. App copy MUST NOT imply that `Relay` is less secure than `Direct`.

The authoritative source of the badge is the Android connection manager, which knows whether it opened a direct UDP socket or a fallback relay tunnel. The daemon-sent `path_state` advisory frame is for cross-validation only.

## Interactive Behavior

When the user opens a session detail view:

1. if needed, app requests or renews an active-session lease through Relay
2. app sends `session_activate` with the Relay-issued lease token
3. app sends `interactive_request` on the control stream
4. app waits for `interactive_granted` or `interactive_denied`
5. on grant, app binds to the new interactive stream
6. app renders:
   - snapshot begin
   - snapshot chunks
   - snapshot end
   - live bytes
7. app sends input and resize only for sessions whose interactive attach remains active

When the user leaves:

1. app sends `interactive_release`
2. if the product action is "stop using this session", app also releases the active-session lease through Relay
3. app tears down the detail terminal view

## Reconnect Behavior

On daemon transport reconnect:

1. perform transport handshake again
2. accept a fresh `session_index`
3. accept fresh preview snapshots
4. for each session the app still wants interactive, re-send `interactive_request`
5. rebuild each affected terminal view from a fresh snapshot

The app must not expect missed-byte replay.

## Cache Rules

Phase 1 keeps caching simple:

- account/session auth cache: allowed as needed
- daemon presence cache: allowed as a normal UI convenience
- preview cache: not used
- interactive terminal cache: not used

Logout or account switch must clear any local daemon trust visibility derived from the previous account session.

The app must not assume that process death releases a free-tier active-session lease immediately. On restart, it should first try to resume the existing lease state reported by Relay.

Phase-1 recommended timing:

- lease TTL: `3 minutes`
- lease renewal cadence while actively in use: every `45 seconds`

## References

- `docs/connectivity/architecture.md`
- `docs/connectivity/relay-protocol.md`
- `docs/connectivity/transport-protocol.md`
- `docs/connectivity/mobile-reference.md`
