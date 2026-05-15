# Android Client Reference

## Status

Phase-1 Android client behavior for the QUIC session-connectivity architecture. This reference defines what the app owns, what Relay owns, and what the daemon transport exposes.

When this doc and `../contract.md` disagree, `../contract.md` wins.

## Core Rules

- Login is required before any connectivity feature is available.
- Relay provides account tier, daemon presence, pairing transport, rendezvous, and fallback tunnel setup.
- Daemon transport provides sessions, previews, interactive traffic, input, resize, and path diagnostics.
- Free and Pro differ only by trusted-computer count.
- Inside one active trusted computer, Free and Pro session behavior is identical.
- Do not implement tier-based session row states, preview restrictions, or Free-only session UI.

## Communication Planes

### 1. Relay Control Plane

- account-authenticated startup
- `GET /api/account/policy`
- daemon presence
- REST pairing response submission
- rendezvous hint exchange
- fallback relay tunnel setup

Relay never sends session lists or per-session policy.

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

Recommended client identity model:

- generated once on first authenticated setup
- stored in Android Keystore where available
- `client_fingerprint = sha256(public_key_raw)` is the long-lived client identity reported to Relay
- reinstalling the app deletes the device key; the user must re-pair every daemon

## Login And App Session

Per `../contract.md` D4:

1. Login request body includes `client_fingerprint` alongside credentials.
2. Relay returns opaque app access and refresh tokens.
3. The server-side app session stores `account_id`, session id, expiry, and `client_fingerprint`.
4. Token refresh must include the same `client_fingerprint`; Relay rejects mismatch.

Phase 1 does not require an additional per-WebSocket device-key proof. Daemon-side security relies on pairing-pinned device keys, not the app-session token format.

## Tier And Computer Policy

The app fetches `GET /api/account/policy` after restoring or creating an app session.

Policy rules:

- `free`: keep at most one active trusted computer.
- `pro`: keep up to ten trusted computers.

Android owns the local trusted-computer inventory and the active-computer decision. Relay stores neither. Daemons are tier-unaware.

### Free

- If exactly one active trusted computer is online, auto-connect it.
- If no active trusted computer exists, show pairing.
- To change computers, run Replace Computer. Keep the old trust active until the new pairing SAS succeeds.
- If multiple active trusted computers are present due to stale state, require user resolution before connecting.

TODO: Replace Computer currently deletes old trust only from Android local state after successful SAS. Add old-daemon trust revocation later.

### Pro

- Auto-connect all online trusted computers, up to ten.
- If the user reaches ten trusted computers, block new pairing and ask them to remove one first.
- Session behavior inside each connected computer is the same as Free.

### Pro Downgrade To Free

If tier changes to `free` while multiple trusted computers exist:

1. Close or avoid opening extra daemon transports.
2. Show downgrade resolution.
3. Require the user to choose one computer to keep active.
4. After selection, only connect that computer.

Do not auto-connect multiple computers before resolution.

## Daemon Lifecycle Expectation

`tunnel run` on the user's computer starts the required daemon if it is not already running (`../contract.md` D2). From Android:

- a computer that has run `tunnel run` at least once should have a daemon listening
- computer presence in `computer_snapshot` is the source of truth for online status
- Android does not need "start daemon on the computer" UI in phase 1

## Pairing Flow

1. User is logged in on Android.
2. User runs `tunnel pair` on the computer; daemon mints a signed JSON invitation and prints a QR code.
3. User imports the invitation with the Android app.
4. App validates the daemon-signed invitation locally.
5. App signs the invitation challenge with its persistent device key, including the Relay-authenticated `account_id`.
6. App sends the pairing response through Relay.
7. App displays the SAS.
8. User confirms matching SAS with the daemon screen.
9. App persists daemon trust only after explicit user confirmation and after tier/computer-count rules allow it.

The app must not auto-trust other daemons under the same account.

Wire details: `../protocol/pairing.md`.

## Android State Model

The app should separate four kinds of state.

### 1. Relay-Control State

- account session
- account tier
- visible daemon list
- pairing visibility
- rendezvous attempt bookkeeping

Logout or account switch clears official-app account state and closes Relay / daemon transports. It does not revoke daemon-local trust by itself.

### 2. Trusted-Computer Policy State

- trusted computers known locally
- active computer for Free
- trusted-computer count for Pro
- Replace Computer transaction state
- downgrade-resolution state

This state decides which daemon transports the app may open. It must not decide which sessions are usable inside an already active daemon transport.

### 3. Per-Daemon Transport State

For each connected daemon:

- current path kind: `direct` or `relay`
- lifecycle: `offline`, `connecting_direct`, `connecting_relay`, `connected_direct`, `connected_relay`, `reconnecting`
- one control stream handle
- zero or more interactive stream handles

Path is a daemon-connection property, not a per-session property.

### 4. Per-Session UI State

For each session under a connected daemon:

- current metadata
- current preview, if subscribed
- whether interactive is requested
- whether interactive is granted
- terminal emulator instance, if displayed

There is no tier-derived per-session availability state.

Canonical state machines: `../reference/state-machines.md`.

## Startup Order

Recommended:

1. restore account session
2. fetch account policy from Relay
3. load local trusted-computer state
4. resolve Free downgrade or stale multi-active state before opening daemon transports
5. open Relay realtime WebSocket
6. send `app_register`
7. receive `computer_snapshot`
8. open daemon transports allowed by the current tier:
   - Free: the one active online trusted computer
   - Pro: all online trusted computers, up to ten
9. once each daemon transport is ready, accept session metadata and render its full session list

The app must not pretend session data exists before daemon transport is up.

## Daemon Transport Usage

Connection strategy:

- attempt direct QUIC using rendezvous hints
- fall back quickly to relay packet tunnel
- expose the current path badge
- open one long-lived control stream
- open one short-lived interactive stream per attached session as needed

The app may keep transports open for all tier-allowed online computers. It may also apply ordinary resource management, but that management is not a subscription rule.

## Session Bootstrap

After daemon transport is ready:

1. exchange `hello`
2. accept `session_index`
3. sort and render session rows within that computer by `started_at ASC`, then `session_id ASC`
4. subscribe to preview for rows the app wants live preview for

All rows are usable for both Free and Pro when the computer is active. Be prepared for a short gap where the computer is visible but sessions have not arrived. Show a lightweight `Loading sessions...` state.

## Path Badge

Per computer, expose:

- `Direct`: "Connected directly to your computer."
- `Relay`: "Connected through your account's relay. Encryption is the same as direct mode."

The badge is informational and primarily indicates expected latency. Both paths share identical end-to-end encryption and pinned-identity authentication; Relay never sees terminal plaintext on either path.

## Interactive Detail Views

When the user opens a session detail view:

1. if needed, app sends `preview_subscribe`
2. app sends `interactive_request`
3. app waits for `interactive_granted` or `interactive_denied`
4. on grant, app binds to the new interactive stream
5. app renders snapshot and live bytes
6. app sends input and resize only while the interactive attach is active

When the user leaves:

1. app sends `interactive_release`
2. app tears down the detail terminal view
3. app may keep or drop preview subscription per its list-UI policy

Multi-interactive UI focus:

- exactly one terminal view holds keyboard focus at any given time
- the focused terminal must be visually obvious
- background terminals must not receive input

## Reconnect Behavior

On daemon transport reconnect:

1. perform transport handshake again
2. accept a fresh `session_index`
3. render the full session list for that active computer
4. re-send `preview_subscribe` for any sessions the app still wants live preview for
5. for each session the app still wants interactive, re-send `interactive_request`
6. rebuild each affected terminal view from a fresh snapshot

The app must not expect missed-byte replay.

## Cache Rules

- account / session auth cache: allowed
- local trusted-computer cache: required for phase-1 policy
- daemon presence cache: allowed as a UI convenience
- preview cache: not used
- interactive terminal cache: not used

Logout or account switch clears account-derived visibility and open transports. It does not revoke daemon-local pairing trust.

## Path Selection Rules

- direct-first
- fast fallback (default 3s direct deadline per `../protocol/transport.md`)
- reconnect by opening a fresh QUIC connection; no in-place transport migration

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
