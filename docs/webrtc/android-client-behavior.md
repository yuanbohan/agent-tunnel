# Android Client Behavior Direction

## Status

This document captures the recommended first-phase Android client behavior for the WebRTC session-connectivity architecture described in `docs/webrtc/architecture.md`.

It focuses on:

- initial list rendering
- connection policy
- local caching
- interactive-session handling

This is still a planning document, not yet the final app implementation specification.

## Purpose

The Android client must feel fast while preserving the architectural boundary that:

- Relay provides session metadata
- daemon provides session content

The client should therefore render in layers rather than waiting for the daemon content connection to be ready before showing anything useful.

## Recommended First-Phase Behavior

### 1. Login Gate

Android must require account login before:

- opening realtime WebSocket connections
- scanning pairing invitations
- fetching session discovery
- opening daemon WebRTC connections

This keeps all client behavior inside the account and entitlement model.

### 2. Initial Session List Rendering

The client should render the session list in two stages:

1. Relay metadata layer
2. daemon preview layer

Practical behavior:

- use realtime startup snapshots as the default mobile bootstrap once the realtime WebSocket is connected
- use `GET /api/sessions` only for compatibility, fallback, or explicit refresh paths
- render list rows immediately from metadata
- then hydrate preview content as daemon WebRTC connections become ready

The list should not wait for preview before first paint.

## Recommended Connection Policy

### Online Daemons

First-phase default:

- maintain one `PeerConnection` for every online daemon visible to the current Android device
- do not implement viewport-based lazy connection logic in phase 1

This is justified by the current product usage assumptions:

- most users have one active computer
- some have two
- the number of simultaneously visible daemons is small

### Interactive Session

The client should allow:

- many previewed sessions across connected daemons
- at most one global interactive session at a time

Entering a session detail view should:

- reuse the existing daemon connection
- request interactive mode
- initialize the terminal view from a fresh snapshot

Leaving the detail view should:

- release interactive mode
- return the session to preview-only behavior

## Recommended Local Cache Model

The recommended first-phase cache model is:

- cache Relay metadata
- do not cache interactive terminal content for resume

### Cache Layer 1: Relay Metadata Cache

Recommended cached fields:

- daemon metadata
- session index metadata
- pairing visibility state

Purpose:

- fast list skeleton on app foreground or cold start
- less dependency on immediate network completion for basic UI shape

This cache is low risk because it contains metadata already considered acceptable for Relay visibility.

### No Interactive Resume Cache

Recommended first-phase rule:

- do not persist interactive snapshot or live terminal bytes for resume

Reasons:

- interactive content is the most sensitive payload
- resume correctness is harder than preview correctness
- fresh snapshot recovery already exists as the product model

If the app reconnects, the correct behavior is:

- restore list from metadata cache
- re-enter interactive mode through a fresh interactive snapshot if needed

## Freshness Rules

### Relay Metadata Freshness

Relay metadata should be treated as the list skeleton truth until replaced by fresher Relay state.

If cached metadata is present:

- use it immediately
- refresh it from Relay
- replace it when the new snapshot or delta arrives

### Preview Freshness

Phase 1 should not cache preview locally.

Recommended behavior:

- render metadata-only rows first
- render preview only after fresh daemon preview arrives
- if preview is unavailable, leave the preview area empty

The client should not attempt preview diff reconstruction or stale-preview classification in phase 1.

## Logout And Account Switch Rules

On logout or account switch, the Android client should clear:

- Relay metadata cache
- pairing visibility state associated with the logged-out account
- active WebRTC daemon connections

This avoids cross-account leakage on shared devices.

On revoke or lost pairing visibility for a daemon, the Android client should:

- close any active WebRTC connection to that daemon
- release any interactive session tied to that daemon

## Reconnect Behavior

When the app returns to foreground or a connection drops:

1. restore the list from local caches if present
2. reconnect realtime control plane
3. rebuild daemon WebRTC connections
4. refresh preview content from daemon snapshots
5. if an interactive session should recover, request a fresh interactive snapshot

The client should not attempt missed-byte replay.

## UI Guidance

### Session List

Each row should be renderable from:

- Relay metadata only
- Relay metadata plus fresh daemon preview

This gives the UI graceful degraded states rather than one hard loading state.

### Daemon Connection Badge

The UI should surface a simple connection badge derived from `connection_state_snapshot`:

- `Direct` for `connected + direct`
- `Relay` for `connected + relay`
- `Connecting` for `connecting`
- `Offline` for `disconnected`
- `Unstable` for `degraded`

This badge describes the media path only.

If the media path is connected but `control_plane_available` is false, the app should keep the badge as `Direct` or `Relay` and separately disable or gate actions that require Relay-mediated control.

### Interactive View

The detail screen should only create a terminal emulator for the active interactive session.

The client should not create hidden terminal emulators for background preview rows.

Interactive input must only be sent while the client holds the current interactive lease for that session and daemon connection.

## Recommended Best-Practice Defaults

This document recommends:

- immediate rendering from Relay metadata
- best-effort local caching of Relay metadata
- no preview cache in phase 1
- no persisted interactive terminal resume cache
- one daemon connection per online daemon
- no viewport-based connection scheduler in phase 1
- fresh interactive snapshot recovery after reconnect
- no special stale-preview UI treatment in phase 1
- no Android-side "forget this daemon" action in phase 1

## Open Decisions For Later Discussion

No additional Android-specific product decisions are currently blocking the phase-1 behavior described here.

## Related Documents

- `docs/webrtc/architecture.md`
- `docs/webrtc/realtime-protocol.md`
- `docs/webrtc/datachannel-protocol.md`
- `docs/webrtc/session-index-contract.md`
- `docs/webrtc/pairing-protocol.md`
