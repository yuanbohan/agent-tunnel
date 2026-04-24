# WebRTC Mobile Reference

## Status

This document is the Android-focused reference for the WebRTC session-connectivity design in `docs/webrtc/`.

Its job is to explain:

- what the Android app must implement
- which protocol surfaces it talks to
- how to use those protocol surfaces safely
- which simplifications are intentional in phase 1

This is the reference an implementer should read before building the mobile feature.

## Scope

This document covers:

- account/login prerequisites
- pairing from Android
- realtime WebSocket usage
- WebRTC PeerConnection usage
- DataChannel usage
- Android cache behavior
- interactive lease handling
- security-sensitive implementation notes

It does not define the exact wire schema. Use it together with:

- `docs/webrtc/architecture.md`
- `docs/webrtc/realtime-protocol.md`
- `docs/webrtc/datachannel-protocol.md`
- `docs/webrtc/session-index-contract.md`
- `docs/webrtc/pairing-protocol.md`
- `docs/webrtc/android-client-behavior.md`

## Core Mental Model

Android talks to two planes:

### 1. Relay Control Plane

Android uses Relay for:

- account-authenticated app startup
- session discovery
- daemon discovery
- pairing completion
- connection-state updates
- WebRTC signaling
- interactive lease requests and release
- TURN credential retrieval

### 2. Daemon Data Plane

Android uses the daemon WebRTC connection for:

- preview content
- interactive terminal snapshot
- interactive terminal live bytes
- interactive input
- interactive resize after a lease is granted

The mobile app should never treat the Relay realtime WebSocket as a terminal-content transport.

## Account And Device Preconditions

Android must not use the WebRTC session-connectivity stack before the user is logged in.

The app must require login before:

- opening the app realtime WebSocket
- calling `GET /api/sessions`
- scanning a pairing QR
- opening daemon `PeerConnection`s
- requesting TURN credentials

The mobile app should maintain one local persistent device identity.

Recommended phase-1 device identity model:

- generate one persistent Android device key pair on first authenticated app setup
- prefer Android Keystore-backed keys when available
- persist the public-key fingerprint as the device identity used by pairing and local trust displays

## Pairing Flow From Android

The Android-side pairing flow should be:

1. user is already logged in
2. user scans the QR shown by `tunnel daemon pair`
3. app parses daemon-authored invitation material
4. app verifies basic invitation constraints locally:
   - account binding
   - expiry
   - invitation format
5. app signs the daemon challenge with the Android device private key
6. app submits pairing response to Relay with the invitation correlation information
7. app waits for daemon-approved completion through the control plane
8. app persists local trust state only after successful completion

Android must not treat account login by itself as enough to establish device trust.

## Startup Sequence

Recommended Android startup order:

1. restore local account session
2. restore cached Relay metadata
3. render list skeleton from cached metadata if present
4. connect app realtime WebSocket
5. receive app-side startup snapshots
6. treat those realtime startup snapshots as the authoritative mobile bootstrap
7. call or refresh `GET /api/sessions` only for compatibility, fallback, or explicit refresh flows
8. build the visible daemon/session store
9. open one `PeerConnection` per online paired daemon
10. hydrate preview content from daemon DataChannels

The app should feel useful before step 9 completes.

## App Realtime WebSocket Usage

Android should treat the realtime WebSocket as the authoritative control-plane stream.

### On Connect

Expect, in order:

1. `daemon_index_snapshot`
2. `session_index_snapshot`
3. `pairing_state_snapshot`
4. `connection_state_snapshot`
5. `realtime_ready`

Android should not consider bootstrap complete until `realtime_ready` arrives.

### What Android Sends

Android sends:

- WebRTC signaling messages
- interactive lease requests
- interactive lease release
- pairing response submission

### What Android Receives

Android receives:

- daemon/session index snapshots and deltas
- pairing visibility state
- connection-state updates
- signaling from Relay
- interactive lease grant/deny/release state

## PeerConnection Strategy

Phase-1 default:

- one `PeerConnection` per online daemon
- keep it alive while the daemon remains visible and online
- do not build a viewport-based connection scheduler in phase 1

### Offer/Answer Strategy

Do not invent a custom glare-resolution protocol.

Recommended default:

- use WebRTC perfect-negotiation style behavior
- every signaling event must carry:
  - `peer_connection_id`
  - `negotiation_id`
- Android must discard stale signaling events whose identifiers no longer match the current connection attempt

This is the safest default for reconnects and renegotiation.

### TURN Usage

Android should request TURN credentials from Relay only when needed for connection establishment.

Recommended rules:

- credentials are short-lived
- credentials are not treated as durable client secrets
- credentials are fetched through the account- and pairing-authorized control plane
- credentials should not be written to long-term local storage

## DataChannel Usage

Android should open two DataChannels per daemon connection:

- `control`
- `stream`

Both are reliable and ordered in phase 1.

### `control` Channel

Android uses `control` for:

- preview snapshots and updates
- interactive lease acknowledgements/state
- interactive snapshot boundaries
- interactive resize events
- interactive input messages

### `stream` Channel

Android uses `stream` only for terminal byte content:

- snapshot byte chunks
- live byte chunks

The stream channel is not a general message bus.

## Interactive Lease Handling

Interactive mode is lease-based.

Android must not treat “opening the detail page” as equivalent to having interactive authority.

### Required Client Rule

Android may send interactive input only when:

- the current app connection holds the active lease
- the lease matches the current daemon connection
- the lease has not been released, preempted, expired, or invalidated

### Practical Client Flow

1. user enters a session detail page
2. app requests interactive attach through Relay
3. app waits for lease grant
4. on grant, app initializes terminal view
5. app receives `interactive_snapshot_begin`
6. app consumes snapshot bytes on `stream`
7. app receives `interactive_snapshot_end`
8. app continues with live bytes
9. app sends input only while lease remains active

If the app loses the lease, it must immediately stop sending input.

## Terminal Rendering Rules

Android should only create a terminal emulator for the active interactive session.

It should not create background terminal emulators for session previews.

### Snapshot And Live

The detail screen should treat terminal content as:

1. snapshot begin
2. snapshot bytes
3. snapshot end
4. live bytes

This is intentionally close to the current product model.

### Stream Epoch

The stream bytes are bound to the current interactive `stream_epoch`.

Android must discard stream chunks that do not match the active epoch for the current interactive lease.

This protects the client from stale chunks after reconnect or lease turnover.

## Session List Rendering

Each row should be built from two layers:

### Layer 1: Relay Metadata

Used for:

- immediate row creation
- ordering
- labels
- command/cwd/branch metadata

### Layer 2: Daemon Preview

Used for:

- live preview text
- filling in preview when fresh daemon content arrives

Preview is plain text only. It is not terminal emulation.

## Cache Rules

### Metadata Cache

Android may cache Relay metadata locally for faster startup.

### Preview Cache

Phase 1 should not persist preview locally.

Preview behavior is intentionally simple:

- list rows render from Relay metadata first
- preview appears only when fresh daemon preview arrives
- if preview is absent, the preview area stays empty

### Interactive Content

Do not persist interactive terminal snapshot or live bytes for resume.

Interactive recovery must happen through a fresh snapshot.

## Reconnect Behavior

When Android reconnects:

1. restore metadata cache
2. reconnect realtime WebSocket
3. rebuild daemon connections
4. refresh preview from daemon snapshots
5. if the app should resume the current interactive session, request a fresh interactive lease or recovery path
6. reinitialize the terminal from a fresh interactive snapshot

Android must not attempt missed-byte replay.

## Connection-State UI

The connection badge describes the media path only.

Recommended mapping:

- `connected + direct` -> `Direct`
- `connected + relay` -> `Relay`
- `connecting` -> `Connecting`
- `disconnected` -> `Offline`
- `degraded` -> `Unstable`

If the media path is connected but `control_plane_available` is false:

- keep the media-path badge as `Direct` or `Relay`
- separately disable or gate actions that require Relay-mediated control

Do not overload the path badge to mean more than it actually means.

## Required Security Notes

### 1. Device Keys

Android must use a persistent device key for pairing proof-of-possession.

### 2. Preview Is Sensitive

Preview text is not terminal byte stream, but it is still content.

Treat it as sensitive in-memory display data.

### 3. Metadata Is Not Harmless

`command_preview`, `cwd`, and `git_branch` remain Relay-visible in phase 1, but Android should still treat them as potentially sensitive display metadata.

Do not assume they are safe to log arbitrarily, surface in notifications, or export to analytics without a separate policy.

### 4. TURN Is An Authorization Surface

TURN fallback is not just a network detail. Android must not cache TURN credentials long-term or reuse them outside the authorized daemon/account context.

### 5. Revocation Must Tear Down Access

If pairing is revoked or entitlement is lost, Android must:

- close the relevant daemon connection
- stop rendering that daemon's content
- stop interactive input immediately

## Existing Open-Source Patterns To Prefer

The mobile implementation should prefer established building blocks rather than inventing new subprotocols where avoidable.

Recommended defaults:

- use the platform WebRTC implementation for DataChannels and ICE behavior
- use WebRTC perfect-negotiation style signaling handling
- use coturn-compatible ephemeral TURN credentials from Relay
- use Android Keystore-backed device keys when available

What not to invent in phase 1:

- custom NAT traversal logic
- custom unreliable transport
- app-layer payload encryption on top of WebRTC without a specific new requirement
- custom terminal preview diff protocol
- unordered or partially reliable DataChannel policies

## Implementation Checklist

Before Android work is considered ready to start, the implementer should be able to answer:

- how login gates pairing and transport startup
- how realtime bootstrap is completed
- how a daemon `PeerConnection` is keyed and rebuilt
- how stale signaling is discarded
- how interactive lease grant and release are enforced
- how `stream_epoch` is tracked
- how control-plane-unavailable state affects UI actions
- how revocation tears down live access

## Related Documents

- `docs/webrtc/architecture.md`
- `docs/webrtc/realtime-protocol.md`
- `docs/webrtc/datachannel-protocol.md`
- `docs/webrtc/session-index-contract.md`
- `docs/webrtc/pairing-protocol.md`
- `docs/webrtc/android-client-behavior.md`
