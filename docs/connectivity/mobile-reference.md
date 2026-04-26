# Connectivity Mobile Reference

## Status

This document is the Android-focused implementation reference for the target connectivity architecture under `docs/connectivity/`.

It explains what the mobile app must do, what it must not do, and how to use the new daemon transport safely.

## Planes

Android talks to two planes only.

### 1. Relay Control Plane

Used for:

- account-authenticated startup
- daemon presence
- pairing transport
- rendezvous hint exchange
- fallback relay tunnel setup
- active-session lease issuance and renewal

### 2. Daemon Transport Plane

Used for:

- session metadata
- preview text
- interactive requests and responses
- terminal snapshots
- live terminal bytes
- input and resize

Do not reintroduce session discovery or interactive control back into Relay.

## Account And Device Preconditions

Before any connectivity action:

- user must be logged in
- app must have a persistent device key

Recommended device identity model:

- generate once on first authenticated setup
- store the private key in Android Keystore where possible
- use the public-key fingerprint as the long-lived device identity

## Pairing Usage

Android pairing flow:

1. user is logged in
2. user scans daemon QR
3. app validates daemon invitation locally
4. app signs the invitation challenge with its persistent device key
5. app sends pairing response through Relay
6. app displays the SAS
7. user confirms matching SAS with the daemon screen
8. app persists daemon trust only after successful confirmation

The app must not auto-trust all same-account daemons.

## Realtime WebSocket Usage

Treat the realtime WebSocket as daemon presence and rendezvous only.

### App-Side Startup

Expect:

1. `daemon_snapshot`
2. `realtime_ready`

### App Sends

- pairing response submission
- rendezvous open/close requests if needed by the final wire design

### App Receives

- daemon presence updates
- pairing visibility updates
- rendezvous hints
- relay tunnel readiness

Do not design the app around session snapshots coming from Relay.

## Android State Model

The Android app should explicitly separate three kinds of state:

### 1. Relay-Control State

- account session
- visible daemon list
- pairing visibility
- rendezvous attempt bookkeeping

### 2. Per-Daemon Transport State

For each daemon:

- current path kind: `direct` or `relay`
- transport lifecycle: `offline`, `connecting_direct`, `connecting_relay`, `connected_direct`, `connected_relay`, `reconnecting`
- one control stream handle
- zero or more interactive stream handles

### 3. Per-Session UI State

For each session under a daemon:

- current metadata
- current preview, if this session is currently leased
- whether interactive is requested
- whether interactive is granted
- which terminal emulator instance owns that session, if displayed
- whether the session is currently locked by subscription

The app should not try to store path kind per session. Path is a daemon-connection property.

## Daemon Transport Usage

For each visible online paired daemon, Android should maintain one connection manager.

Connection manager responsibilities:

- attempt direct QUIC using rendezvous hints
- fall back quickly to relay packet tunnel
- expose current path badge
- open one long-lived control stream
- bind one short-lived interactive stream per interactive session when needed

The connection manager should not own session rendering logic directly. Its job is:

- build and maintain the transport
- surface stream events
- report transport state to higher-level session and UI stores

## Session Bootstrap

After the daemon transport is ready:

1. send or receive `hello`
2. accept `session_index`
3. render session rows
4. accept `preview_snapshot` for sessions that currently hold an active-session lease
5. fill preview UI for those leased sessions only

The app should be prepared for a short gap where the daemon is visible but sessions have not arrived yet.

This is expected and should be treated as normal:

- Relay tells the app that a daemon exists and is online
- the daemon itself tells the app which sessions currently exist

### Daemon-Visible-But-No-Sessions Loading State

For each daemon card, the app should render with this priority:

1. while no daemon transport exists yet: show daemon card with a generic `Loading sessions…` subtitle
2. while transport is connecting: show transport state (`Connecting`, `Connecting via Relay`, etc.) on the card
3. once `session_index` arrives: replace the placeholder with the real session list
4. once `preview_snapshot` arrives for a leased session: fill in preview text

Phase 1 MUST NOT display stale cached session counts from previous app sessions, because the cached count may not match what the daemon reports today. The trade-off here is favoring correctness over a perceived "instant list".

## Path Badge Semantics

The `Direct` / `Relay` badge displayed per daemon card describes only the network path. Both paths use the same end-to-end encryption and the same pinned-identity authentication; Relay never sees terminal plaintext on either path.

The badge primarily indicates expected latency. App copy MUST NOT imply that `Relay` is "less secure" than `Direct`. Suggested phrasing:

- `Direct` tooltip: "Connected directly to your computer."
- `Relay` tooltip: "Connected through your account's relay. Encryption is the same as direct mode."

The user is not expected to take security-relevant action based on the badge.

## Subscription And Locked Sessions

Phase-1 subscription is based on Relay-owned active-session leases.

For free users:

- all session rows may still appear in the list
- only one session may be leased at a time
- only the leased session may receive real preview or interactive content

Locked sessions should:

- show a lock icon
- use light greyed-out styling
- keep metadata readable
- not show real preview text

When the user taps a locked session, the app should explain that free allows only one active session at a time and offer an upgrade path. It should not silently switch the active session.

## Active Session Lease Behavior

Relay is the source of truth for active-session leases.

Android should treat the lease as:

- account-scoped
- device-bound
- short-lived but renewable

Phase-1 default timing:

- lease TTL: `3 minutes`
- renewal cadence while the leased session is still actively used: every `45 seconds`

If the app process is killed, the lease should not be assumed released immediately. The app should attempt to resume the existing lease on restart before offering a different session for activation.

## Multi-Interactive UI Focus

The protocol allows multiple interactive sessions to coexist on one daemon connection. The app's UI MUST make the input destination unambiguous:

- exactly one terminal view holds keyboard focus at any given time
- the focused terminal MUST display a clear visual indicator (border, header highlight, or equivalent)
- the soft keyboard, hardware keyboard, and any pasted content route only to the focused terminal
- background interactive sessions continue to receive `live_bytes` but MUST NOT receive any `input_text`, `input_key`, or `resize` while not focused

The daemon protects against UI bugs by dropping input frames whose `session_id` is not currently granted, but the app MUST NOT rely on that as a primary safeguard. The `session_id` field is included on every input frame the app sends.

## Control Stream Usage

Android should use the control stream for:

- session metadata bootstrap and updates
- preview snapshots
- interactive request / grant / deny / release
- input text
- input keys
- resize
- path-state updates

The control stream is structured, not raw terminal bytes.

## Interactive Stream Usage

The interactive stream is byte-oriented.

Android should use it for:

- snapshot begin
- snapshot chunks
- snapshot end
- live bytes

Multiple interactive streams may exist concurrently. Each one is bound to one session attach lifetime.

Each displayed interactive session should own its own terminal emulator instance. The protocol does not require there to be only one.

## Input Rules

Android may send input only after `interactive_granted` for that session.

If the daemon denies the request, the app must stay read-only for that session.

When an interactive session ends or the connection drops, stop sending input for that session immediately.

## Path Selection Rules

Phase 1 prioritizes implementation simplicity:

- direct-first
- fast fallback
- reconnect by opening a new QUIC connection
- no in-place transport migration

The app should not try to keep complex direct-vs-relay transition state beyond the current badge and connection lifecycle.

### Direct And Fallback Mental Model

The Android side should think in this order:

1. Relay says the daemon is online
2. Android and daemon exchange rendezvous hints
3. Android first tries direct QUIC over UDP
4. if that does not succeed quickly, Android opens the relay tunnel carrier
5. Android establishes a new QUIC connection over the relay carrier
6. higher-level session synchronization starts only after the connection is ready

The higher-level session protocol should not have special "direct version" and "relay version" handlers.

## Library Guidance

The mobile transport implementation must support a custom QUIC application protocol with arbitrary streams.

Practical guidance:

- do not anchor the design on WebRTC
- do not anchor the design on Cronet request APIs
- validate daemon interoperability with the chosen Android QUIC stack before building higher-level features

## What Not To Build

Do not build:

- preview caching in phase 1
- a Relay-backed session list
- a WebRTC compatibility layer
- app-layer payload encryption on top of QUIC in phase 1

## References

- `docs/connectivity/architecture.md`
- `docs/connectivity/relay-protocol.md`
- `docs/connectivity/transport-protocol.md`
- `docs/connectivity/pairing-protocol.md`
- `docs/connectivity/daemon-session-sync.md`
- `docs/connectivity/android-client-behavior.md`
- `docs/connectivity/sequence-flows.md`
