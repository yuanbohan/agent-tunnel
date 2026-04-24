# WebRTC Pairing Protocol Direction

## Status

This document captures the recommended pairing and device-trust model for the WebRTC session-connectivity architecture described in `docs/webrtc/architecture.md`.

It is still a planning document. It defines the trust flow and product boundary, not yet the final QR payload or persistence schema.

## Purpose

Pairing exists to answer:

- which Android device may access which daemon
- how a daemon grants long-lived trust to a phone
- how device trust stays separate from account ownership

Pairing is not the same thing as account login.

## Core Principles

### 1. Account Ownership And Device Trust Are Separate

An account may own:

- multiple phones
- multiple daemons

But same-account ownership does not automatically imply trust.

Each daemon must explicitly pair with each Android device that should access it.

### 2. Android Login Is Required Before Pairing

The Android app must be logged in before it can:

- scan a pairing QR code
- send a pairing response
- use session discovery or realtime connectivity

This keeps pairing inside the subscription and device-ownership model.

### 3. Pairing Is Daemon-Initiated

The primary user flow starts from the daemon side:

1. user runs `tunnel daemon pair`
2. daemon starts if necessary
3. daemon creates a short-lived, one-time pairing invitation
4. CLI displays a QR code
5. Android scans the QR code
6. Android sends a pairing response back through Relay
7. daemon validates and records trust locally

This keeps the trust-approval action anchored on the machine that owns the sessions.

### 4. Relay May Transport Pairing Responses But Is Not The Trust Root

Relay may help complete pairing by transporting the Android response back to the daemon.

Relay must not be treated as the final trust authority.

The recommended first-phase default is:

- the daemon authors the trust-bearing invitation material
- Relay issues a short-lived `correlation_id`
- Android returns its pairing response through Relay together with that `correlation_id`
- Relay uses the `correlation_id` only to route and correlate the in-flight pairing attempt
- the daemon still performs the final trust validation locally

The daemon must perform the trust decision by validating:

- invitation identity
- invitation expiry
- one-time-use semantics
- account binding
- response authenticity

Phase 1 should make `response authenticity` concrete:

- Android owns a persistent device key pair
- the preferred implementation uses Android Keystore-backed keys when available
- the invitation binds `daemon_id`, `account_id`, expiry, nonce, and the in-flight `correlation_id`
- Android signs the daemon-generated challenge with its device private key
- the daemon stores the Android public-key fingerprint as the trusted device identity
- the daemon rejects reused invitation nonces, reused `correlation_id` values, and mismatched account bindings

### 5. Pairing Trust Is Long-Lived And Local

A successful pairing creates persistent daemon-scoped trust until it is revoked.

That trust is stored:

- on the daemon
- on the Android device

It is not cloud-restored from Relay after device replacement or reinstall.

## Recommended User Flow

### First Pair

Recommended flow:

1. user logs into the Android app
2. user logs into `tunnel` on the computer if needed
3. user runs `tunnel daemon pair`
4. daemon auto-starts if needed
5. daemon shows a short-lived, one-time QR code
6. Android scans the QR code
7. Android sends a pairing response through Relay
8. daemon validates and records the Android device as trusted
9. Android can now discover and connect to that daemon's sessions

### Revoke

Recommended daemon-side revoke surface:

- `tunnel daemon devices`
- `tunnel daemon revoke <device>`

This keeps the daemon as the trust owner for its own device roster.

## Invitation Shape

The invitation should be:

- short-lived
- one-time
- account-bound
- daemon-authored

The invitation may also include or be associated with a Relay-issued short-lived `correlation_id` used for response routing.

Recommended invitation responsibilities:

- identify the daemon
- identify the account context
- carry minimum data needed for Android to create a valid response
- avoid becoming a reusable long-lived credential

The QR code is therefore a trust bootstrap artifact, not a durable device identity token.

## Account Binding

Pairing invitations should be bound to the current account.

Implications:

- if Android is logged into the wrong account, pairing should fail
- the daemon should not accidentally trust a phone from another account
- subscription and device ownership remain consistent with pairing flows

This keeps pairing UX simple without letting device trust float independently of ownership.

## Trust Storage Model

### Daemon Stores

The daemon should store a trusted-mobile roster containing at least:

- Android device identifier
- Android device public-key fingerprint
- Android device display name
- pairing time
- trust active/revoked state
- pairing epoch or roster version

The daemon needs this for:

- `tunnel daemon devices`
- `tunnel daemon revoke`
- deciding whether an Android device may discover its sessions

### Android Stores

Android should store at least:

- daemon identifier
- daemon display name
- local trust state

This allows the app to understand which machines it has paired with even before they are currently online.

### Relay Stores

Relay should store the minimum pairing graph needed for:

- authorization filtering
- discovery filtering
- realtime signaling authorization
- TURN authorization
- in-flight pairing correlation and response routing

Relay stores a derived authorization copy, not the trust root itself.

Recommended phase-1 rule:

- daemon-approved pairing state is authoritative
- Relay authorization state is synchronized from daemon-approved pairing state
- new authorization decisions must fail closed if Relay pairing state is stale relative to the daemon's current pairing epoch
- revoke must fan out to existing app and daemon connections, not just future discovery or signaling requests

Relay does not become the trust recovery authority.

## Pairing State Reconciliation

Because pairing state exists both:

- locally on the daemon
- as a derived authorization copy in Relay

the reconciliation rule must be explicit.

Recommended phase-1 rule:

- daemon-local pairing approval is authoritative
- every successful pair or revoke advances the daemon pairing epoch
- the daemon synchronizes the current pairing epoch and authorized device set to Relay
- Relay must not grant new discovery, signaling, or TURN authorization from stale pairing state
- when Relay pairing state and daemon pairing state disagree, access should fail closed until the daemon resynchronizes

This keeps Relay useful for authorization without turning it into the trust root.

## Relationship To Discovery

Pairing gates daemon visibility.

That means:

- a daemon may be online before anything is paired
- an unpaired Android device cannot discover that daemon's sessions
- once pairing succeeds, that Android device may discover that daemon's sessions

The authorization granularity is daemon-scoped, not session-scoped.

## Relationship To Subscription

Account login and subscription determine:

- who owns the devices
- which devices belong together
- what entitlements apply

Pairing determines:

- which Android device may access which daemon

This means a paid account may own multiple phones and multiple daemons, while still requiring explicit daemon-scoped trust approval per pairing.

## TURN Credential Contract

TURN access must be treated as an authorization surface, not an implementation detail.

Recommended phase-1 rule:

- Relay issues ephemeral coturn credentials only after account, entitlement, pairing, and daemon-visibility checks succeed
- TURN credentials are short-lived
- TURN credentials are never stored as long-term client secrets
- revoke, logout, or entitlement loss must prevent issuance of new TURN credentials
- operator-managed coturn shared-secret rotation remains possible without changing pairing state

This keeps TURN fallback aligned with the same trust boundary as direct connectivity.

## Recommended Best-Practice Defaults

This document recommends:

- daemon-initiated pairing
- short-lived one-time QR invitations
- Android-login-required pairing
- account-bound invitations
- Relay-assisted response transport
- Relay-issued short-lived pairing correlation identifiers for routing
- daemon-local trust approval and persistence
- Android device proof-of-possession using a persistent device key
- daemon-authoritative pairing epochs synchronized to Relay authorization state
- ephemeral TURN credentials issued only after pairing and entitlement checks
- no automatic trust inheritance across same-account devices
- no cloud restore of pairing trust
- no Android-side "forget this daemon" action in phase 1; revoke remains daemon-owned

## Open Decisions For Later Discussion

These areas still need explicit review before the protocol is final:

- the exact QR invitation payload format
- the exact Android device identifier format exposed in the trusted-device roster
- whether the daemon should store additional audit metadata such as last-used time

## Related Documents

- `docs/webrtc/architecture.md`
- `docs/webrtc/realtime-protocol.md`
- `docs/webrtc/session-index-contract.md`
