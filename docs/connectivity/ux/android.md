# Android Client Reference

## Status

Phase-1 Android client behavior for the QUIC session-connectivity architecture. This is the consolidated reference for Android implementers — what the app must do, what it must not do, and how to use the daemon transport safely.

When this doc and `../contract.md` disagree, `../contract.md` wins.

## Core Rules

- login is required before any connectivity feature is available
- Relay provides daemon presence and subscription tier — never sessions
- the daemon transport provides sessions, previews, and interactive traffic
- no preview cache in phase 1
- daemon cards are the primary information architecture
- daemon is subscription-unaware
- free / pro differences are enforced only by the official app

## Two Communication Planes

Android talks to two planes only.

### 1. Relay Control Plane

- account-authenticated startup
- subscription tier fetch
- daemon presence
- pairing transport
- rendezvous hint exchange
- fallback relay tunnel setup

### 2. Daemon Transport Plane

- session metadata
- preview subscribe / unsubscribe
- preview text
- interactive request / grant / deny / release
- terminal snapshots
- live terminal bytes
- input and resize

Do not reintroduce session discovery or interactive control on the Relay plane.

## Account And Device Preconditions

- user must be logged in
- app must own a persistent Android device key

Recommended device identity model:

- generated once on first authenticated setup
- stored in Android Keystore where available (hardware-backed where possible)
- the public-key fingerprint (`device_fingerprint = sha256(public_key_raw)`) is the long-lived device identity reported to Relay
- reinstalling the app deletes the device key; the user must re-pair every daemon

## Login And App Session

Per `../contract.md` D4:

1. login request body includes `device_fingerprint` alongside credentials
2. Relay returns opaque app access and refresh tokens
3. the server-side app session stores `account_id`, session id, expiry, and `device_fingerprint`
4. token refresh must include the same `device_fingerprint`; Relay rejects mismatch

Phase 1 does not require an additional per-WebSocket device-key proof. Daemon-side security relies on pairing-pinned device keys, not the app-session token format.

## Daemon Lifecycle Expectation

`tunnel run` on the user's computer auto-starts the daemon if it is not already running (`../contract.md` D2). From the Android side, this means:

- a computer that has run `tunnel run` at least once should have a daemon listening
- daemon presence in `daemon_snapshot` is the source of truth for connectability
- Android does not need any "start daemon on the computer" UI in phase 1

## Pairing Flow

1. user is logged in on Android
2. user runs `tunnel daemon pair` on the computer; daemon mints a signed JSON invitation
3. user imports the invitation with the Android app (QR rendering is deferred)
4. app validates the daemon-signed invitation locally
5. app signs the invitation challenge with its persistent device key, including the Relay-authenticated `account_id`
6. app sends the pairing response through Relay
7. app displays the SAS
8. user confirms matching SAS with the daemon screen (active confirmation, ≥ 1s delay, no auto-prefocus)
9. app persists daemon trust only after explicit user confirmation

The app must not auto-trust other daemons under the same account.

Wire details: `../protocol/pairing.md`.

## Realtime WebSocket Usage

Treat the realtime WebSocket as daemon presence and rendezvous only.

### App-Side Startup

1. app sends `app_register(app_version, protocol_version, ...)`
2. Relay sends `daemon_snapshot`
3. Relay sends `realtime_ready`

The app learns its subscription tier through authenticated Relay app APIs, not through realtime per-session policy events. Relay derives `account_id` and `device_fingerprint` from the authenticated server-side app session.

### App Sends

- pairing response submission
- rendezvous open / hint / close

### App Receives

- daemon presence updates
- pairing visibility updates
- rendezvous hints
- relay tunnel readiness

Do not design the app around session snapshots coming from Relay.

## Android State Model

The app should explicitly separate three kinds of state.

### 1. Relay-Control State

- account session
- subscription tier
- visible daemon list
- pairing visibility
- rendezvous attempt bookkeeping

Logout or account switch clears official-app account state and closes Relay / daemon transports. It does not revoke pairing trust by itself; pairing trust remains daemon-local until the daemon explicitly revokes that device.

### 2. Per-Daemon Transport State

For each daemon:

- current path kind: `direct` or `relay`
- transport lifecycle: `offline`, `connecting_direct`, `connecting_relay`, `connected_direct`, `connected_relay`, `reconnecting`
- one control stream handle
- zero or more interactive stream handles

Path is a daemon-connection property, not a per-session property.

### 3. Per-Session UI State

For each session under a daemon:

- current metadata
- current preview, if subscribed
- whether interactive is requested
- whether interactive is granted
- which terminal emulator instance owns that session, if displayed
- whether the session is currently locked by the app's free/pro rule
- per-daemon `unlocked_session_id` (free tier; see `subscription.md`)

Canonical state machines: `../reference/state-machines.md`.

## Startup Order

Recommended:

1. restore account session
2. fetch subscription tier from Relay app APIs
3. open Relay realtime WebSocket
4. send `app_register`
5. receive `daemon_snapshot`
6. render daemon list
7. wait for the user to open one daemon card
8. open daemon transport for that daemon card
9. once a daemon transport is ready, accept session metadata and apply the local free/pro rule for that daemon card

The app must not pretend session data exists before the daemon transport is up.

## Daemon Transport Usage

Phase-1 connection strategy:

- maintain one connection manager per daemon card the user has opened
- attempt direct QUIC using rendezvous hints
- fall back quickly to relay packet tunnel
- expose the current path badge
- open one long-lived control stream
- open one short-lived interactive stream per attached session as needed

### Lazy Connect

The app opens daemon transport only when the user opens a daemon card. Phase 1 does not require viewport-based lazy connect/disconnect, eager background fan-out, or per-card idle tear-down. Free-tier policy never depends on a cross-daemon global roster.

## Session Bootstrap

After the daemon transport is ready:

1. exchange `hello`
2. accept `session_index`
3. sort and render session rows within that daemon card by `started_at ASC`, then `session_id ASC` as a stable tie-breaker
4. apply the free / pro policy locally for that daemon card
5. subscribe to preview only for the rows the app actually wants live preview for

Be prepared for a short gap where the daemon is visible but sessions have not arrived. Show a lightweight `Loading sessions…` state on the card.

## Free / Pro Rendering

Phase-1 subscription is an official-app product rule, not a daemon-side hard authorization (`../contract.md` D3, `subscription.md`).

### Free

- all session rows remain visible
- the app maintains one `(daemon_id → unlocked_session_id?)` per daemon card
- `unlocked_session_id` is set on **first explicit `interactive_request`** the user issues for that card and never changes while that session is alive
- when the unlocked session ends, `unlocked_session_id` clears and the next user-attach picks the new one — there is no auto-rollover
- only that unlocked session may receive preview or interactive in the official app for that daemon card
- all other rows render with a lock icon and light grey styling

### Pro

- the app does not apply the free-tier rule
- after roster bootstrap, the app automatically subscribes to preview for every live session in the opened daemon card

## Locked Session UI

When the user taps a locked session on free tier:

- explain "Free can only run 1 session per computer at a time. Wait for `<unlocked label>` to finish, or upgrade Pro."
- name the currently unlocked session when known
- do not silently switch
- do not offer override or manual switching in phase 1

Locked rows should:

- show a lock icon
- use light grey styling
- keep metadata readable
- not show real preview text

If the payment flow is still deferred, the upgrade affordance is informational only (no clickable upgrade link).

## Path Badge

Per daemon card, expose:

- `Direct`: "Connected directly to your computer."
- `Relay`: "Connected through your account's relay. Encryption is the same as direct mode."

The badge is informational and primarily indicates expected latency. Both paths share identical end-to-end encryption and pinned-identity authentication; Relay never sees terminal plaintext on either path. UI copy MUST NOT imply the relay path is less secure.

Suggested treatment: the badge is tap-to-explain rather than a permanent text label, to avoid "why is it on Relay" anxiety.

## Interactive Detail Views

When the user opens a session detail view for an unlocked session:

1. if needed, app sends `preview_subscribe`
2. app sends `interactive_request`
3. app waits for `interactive_granted` or `interactive_denied`
4. on grant, app binds to the new interactive stream
5. app renders snapshot and live bytes
6. app sends input and resize only while the interactive attach is active

When the user leaves:

1. app sends `interactive_release`
2. app tears down the detail terminal view
3. app may keep or drop the preview subscription per its list-UI policy

Multi-Interactive UI Focus:

- exactly one terminal view holds keyboard focus at any given time
- the focused terminal must be visually obvious
- background terminals must not receive input

Do not rely on the daemon's drop-input-for-non-granted-session safeguard as a primary defense — local focus discipline is the first line.

## Reconnect Behavior

On daemon transport reconnect:

1. perform transport handshake again (direct first, fallback on deadline)
2. accept a fresh `session_index`
3. recompute lock state per daemon card (the sticky `unlocked_session_id` carries through if its session still exists)
4. re-send `preview_subscribe` for any sessions the app still wants live preview for
5. for each session the app still wants interactive, re-send `interactive_request`
6. rebuild each affected terminal view from a fresh snapshot

The app must not expect missed-byte replay.

## Cache Rules

Phase 1 keeps caching simple:

- account / session auth cache: allowed
- daemon presence cache: allowed as a UI convenience
- preview cache: not used
- interactive terminal cache: not used

Logout or account switch clears any local daemon visibility derived from the prior account session, but does not revoke pairing trust.

## Path Selection Rules

- direct-first
- fast fallback (sequential, not happy-eyeballs; default 3s direct deadline per `../protocol/transport.md`)
- reconnect by opening a fresh QUIC connection — no in-place transport migration

Do not maintain complex direct-vs-relay transition state beyond the current badge and connection lifecycle.

## References

- `../architecture.md`
- `../contract.md`
- `../protocol/relay.md`
- `../protocol/transport.md`
- `../protocol/pairing.md`
- `subscription.md`
- `../reference/state-machines.md`
- `../reference/error-codes.md`
