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
- active-session selection updates and device-scoped access-token issuance / renewal

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
2. `active_session_selection_snapshot`
3. `realtime_ready`

### App Sends

- pairing response submission
- rendezvous open/close requests if needed by the final wire design

### App Receives

- daemon presence updates
- active-session selection updates
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
- account-global active-session selection state
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
- current preview, if this session is currently selected for the account and activated on this device
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

### Multi-Daemon Connection Strategy

Phase-1 strategy is **eager-with-idle-tear-down**:

- on app foreground with a logged-in account, open one QUIC connection per visible online paired daemon
- keep the foreground daemon (the one the user is currently looking at, or most recently looked at) connected even when no session is active
- background daemons (not currently in the user's view and not the most-recently-used) MAY have their QUIC connection torn down after `60s` of UI inactivity to conserve battery and mobile data
- the Relay realtime WebSocket remains open in all these cases; it carries presence updates so the daemon list stays current
- when the user returns to a daemon whose QUIC connection was torn down, the connection manager opens a fresh QUIC connection on demand

This phase-1 default trades a small first-look latency for materially lower steady-state battery cost when many daemons are paired. The strategy may be revisited once production data shows real concurrency distribution.

## Session Bootstrap

After the daemon transport is ready:

1. send or receive `hello`
2. accept `session_index`
3. render session rows
4. accept `preview_snapshot` for sessions that are currently selected for the account and activated on this device
5. fill preview UI for those selected-and-activated sessions only

The app should be prepared for a short gap where the daemon is visible but sessions have not arrived yet.

This is expected and should be treated as normal:

- Relay tells the app that a daemon exists and is online
- the daemon itself tells the app which sessions currently exist

### Daemon-Visible-But-No-Sessions Loading State

For each daemon card, the app should render with this priority:

1. while no daemon transport exists yet: show daemon card with a generic `Loading sessions…` subtitle
2. while transport is connecting: show transport state (`Connecting`, `Connecting via Relay`, etc.) on the card
3. once `session_index` arrives: replace the placeholder with the real session list
4. once `preview_snapshot` arrives for a selected-and-activated session: fill in preview text

Phase 1 MUST NOT display stale cached session counts from previous app sessions, because the cached count may not match what the daemon reports today. The trade-off here is favoring correctness over a perceived "instant list".

## Path Badge Semantics

The `Direct` / `Relay` badge displayed per daemon card describes only the network path. Both paths use the same end-to-end encryption and the same pinned-identity authentication; Relay never sees terminal plaintext on either path.

The badge primarily indicates expected latency. App copy MUST NOT imply that `Relay` is "less secure" than `Direct`. Suggested phrasing:

- `Direct` tooltip: "Connected directly to your computer."
- `Relay` tooltip: "Connected through your account's relay. Encryption is the same as direct mode."

The user is not expected to take security-relevant action based on the badge.

## Subscription And Locked Sessions

Phase-1 subscription is based on Relay-owned account-global active-session selection plus device-scoped access tokens.

For free users:

- all session rows may still appear in the list
- only one session may be selected at a time
- only the selected session may receive real preview or interactive content

All phones on the same account should converge on the same selected-session state. If another phone changes the selected session, this phone must update its lock states and detail views immediately.

Locked sessions should:

- show a lock icon
- use light greyed-out styling
- keep metadata readable
- not show real preview text

### Active Session UX

The app does NOT auto-clear the selected session when the user leaves the session detail view. The selected session remains active until one of the following happens:

- the user taps an explicit "Release this session" control
- the user taps a different locked session and confirms replacement in the modal
- the user logs out or switches accounts

When the user explicitly releases or replaces a session, Relay is authoritative: Android calls Relay's selection endpoint, Relay records the new account-global selection state, Relay fans out `active_session_selection_changed` to all app devices on the account, and Relay fans out `access_token_revoked` to the daemon so old tokens become unusable immediately even on a direct connection.

When the user taps a locked session while a different session is currently selected, the modal MUST:

- name both sessions clearly ("Currently active: X" / "You're trying to open: Y")
- if the app knows another device made the current selection, mention that device in helper text
- offer "Replace X with Y" as an explicit action
- NOT silently switch the active session

The phase-1 upgrade affordance is text-only because the payment flow is deferred. The modal mentions that Pro unlocks more simultaneous sessions but does not yet provide a clickable upgrade link.

## Account Switch Behavior

Phase-1 rules for logout / account switch:

- local pairing trust is daemon-scoped, not account-scoped — Android keeps the pinned daemon fingerprints in local storage even when the user logs out or switches accounts
- Relay-derived visibility (which paired daemons appear in the daemon list) is account-scoped and MUST be cleared from any in-memory caches on logout
- on switching to a different account, the daemon list is rebuilt from the new account's `daemon_snapshot` over the realtime WebSocket; daemons paired only under the previous account are not visible
- on switching back to the original account, those daemons reappear automatically via Relay presence; pairing trust does not need to be re-established because it was never deleted

When the user explicitly removes an account from the device (not just signs out), the app SHOULD also delete local pairing fingerprints associated with that account's prior visible daemons. This is a hard reset, not an account switch.

The Android device key itself is account-independent and persists across account switches. It is regenerated only on app reinstall or explicit user action.

## Active Session Lease Behavior

Relay is the source of truth for account-global active-session selection.

Android should treat the selected session as:

- account-scoped
- shared across all phones on that account until Relay says otherwise

Android should treat the access token as:

- device-bound
- short-lived but renewable

Phase-1 default timing:

- access-token TTL: `3 minutes`
- renewal cadence while the selected session is still actively used on this device: every `45 seconds`

If the app process is killed, the selected session should not be assumed released. The app should first read the current selection state from Relay on restart and then request a fresh device-scoped access token for that selected session if it still wants content.

### Cross-Device Selection Changes

If another phone changes the selected session while this phone is running:

- update the session list immediately so the old session becomes locked and the new session becomes unlocked
- stop showing real preview for the old session as soon as the daemon stops sending it or `access_token_revoked` arrives
- if the user is currently inside the old session detail view, show a blocking notice:
  - `Current active session switched to <new session>`
  - `Changed by <device name>` when available
- offer only:
  - `Open <new session>`
  - `Back to list`

Do not silently keep the user inside a now-unselected interactive session.

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
