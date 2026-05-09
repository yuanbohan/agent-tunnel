# Connectivity Relay Protocol Direction

## Status

This document captures the Relay-owned control-plane protocol for the QUIC connectivity architecture. Step 2 implemented the auth/pairing/visibility subset. Step 4 adds fallback relay tunnel setup and opaque packet forwarding. Step 5 adds live rendezvous hint exchange for direct UDP attempts.

## Purpose

Relay owns only these protocol concerns:

- daemon presence
- pairing message transport
- rendezvous hint exchange
- fallback relay-tunnel setup
- account tier exposure to the official app

Relay does not own:

- session discovery
- preview authority
- interactive authority
- per-session daemon-side authorization

The connectivity edge also needs a STUN service, but STUN itself is infrastructure rather than part of the Relay business protocol described here.

## Endpoint Shape

The target architecture keeps two primary authenticated realtime WebSockets:

- app-side realtime WebSocket: `GET /api/connectivity/ws`
- computer-side realtime WebSocket: `GET /connectivity/computer/ws`

They share one envelope style but do not necessarily receive the same startup snapshots.

Separately, Relay exposes:

- app-side pairing response submission: `POST /api/pairing/responses`
- fallback relay endpoint: `GET /connectivity/tunnel/ws` with `Authorization: Bearer <single-use-token>`
- authenticated app APIs that expose the current account tier

Compatibility aliases remain available in this revision: `GET /api/connectivity/app/ws` for app realtime and `GET /connectivity/daemon/ws` for computer realtime.

## App Authentication Model

Phase 1 uses the existing Relay-issued opaque app access token for app APIs and the app-side realtime WebSocket. The token value is opaque to clients; Relay stores the device fingerprint binding on the server-side app session. See `../contract.md` D4.

### Device Fingerprint Binding

The client app generates a long-lived device key on first authenticated setup, using platform secure storage such as Android Keystore or iOS Keychain/Secure Enclave where available. The fingerprint is:

```
client_fingerprint = sha256(public_key_raw_bytes)   // 32 bytes, hex-encoded for transport
```

Login flow:

1. The client sends `POST /api/auth/login` with body `{ username, password, client_fingerprint }`.
2. Relay validates credentials and persists `(account_id, app_session_id, client_fingerprint)` server-side.
3. Relay returns opaque access and refresh tokens.

Token refresh requires the same `client_fingerprint`; mismatch is rejected as an invalid app session.

Relay uses the authenticated account plus the server-side app-session `client_fingerprint` as the app-side identity for:

- pairing-derived daemon visibility
- pairing response routing
- account-tier reads

### Phase-1 Simplicity Tradeoff

Phase 1 does not require a per-WebSocket cryptographic proof that the app-session holder owns the device's private key. That tradeoff is accepted because daemon-side pinned device keys still protect direct and relay transport access; Relay-side app identity stays as simple as a normal authenticated app session.

Phase-2 may upgrade this by introducing `/auth/register-device` that requires the client to sign a Relay challenge with the device key, raising the app session to a proof-of-possession model (`../contract.md` open TODO `T-AUTH-POP`).

## Shared Envelope

Implemented Step 2 realtime messages use a compact JSON envelope with `type`, optional `protocol_version`, optional `request_id`, and event-specific fields. Future rendezvous/fallback work may add sequencing once those streams exist.

## Startup Snapshots

### App-Side

After authentication succeeds:

1. app sends `app_register`
2. Relay sends `computer_snapshot`
3. Relay sends later visibility updates as live daemon state changes

#### `app_register`

Sent by the app client as the first frame after app-session authentication, before `computer_snapshot`.

Recommended fields:

- `app_version`
- `protocol_version`

`computer_snapshot` contains the full computer roster visible to this client device:

- pairing-derived visibility
- daemon presence and daemon display metadata

It does not contain sessions.

Relay determines the client client identity from the authenticated server-side app session, then uses that `client_fingerprint` plus the authenticated account session to compute pairing-derived visibility.

The app learns its current account tier through authenticated Relay app APIs, not through realtime per-session policy snapshots.

### Daemon-Side

After daemon authentication succeeds:

1. daemon sends `computer_register`
2. Relay accepts the registration and starts routing later control-plane frames

The daemon-side socket does not need startup session or tier state.

#### `computer_register`

Sent by the daemon as the first frame after authentication.

Implemented fields:

- `computer_id`
- `display_name`
- `computer_public_key`
- `computer_fingerprint`
- `platform_family`
- `platform_id`
- `tunnel_version`
- `protocol_version`
- `trusted_clients`

Relay uses these fields to populate `computer_snapshot` for app-side consumers and to detect compatibility mismatches before the computer is announced as visible. The legacy `/connectivity/daemon/ws` alias may still accept `daemon_register` during this compatibility revision.

## Event Families

### Presence

- `computer_snapshot`
- `computer_visible`
- `computer_removed`
- `client_revoked`

Implemented daemon fields:

- `computer_id`
- `display_name`
- `platform_family`
- `platform_id`
- `computer_public_key`
- `computer_fingerprint`
- `tunnel_version`

### Pairing

- `pair_invitation_reserve`
- `pair_invitation_reserved`
- `pair_response_forward`
- `pair_completed`
- `computer_visible`
- `client_revoked`

Relay carries pairing transport, but the daemon remains the trust root.

Event responsibilities:

- `pair_invitation_reserve` is sent from daemon to Relay to reserve a short-lived `correlation_id`
- `pair_invitation_reserved` is sent from Relay to daemon carrying the reserved `correlation_id` request id and Relay-authenticated `account_id`
- signed pairing responses are submitted from the app client to Relay through `POST /api/pairing/responses`
- `pair_response_forward` is sent from Relay to the addressed daemon
- `pair_completed` is sent from daemon to Relay after both sides have stored trust locally and the SAS has been confirmed
- `computer_visible` is sent from Relay to the app client after `pair_completed`
- `computer_removed` is sent from Relay to the app client when a still-trusted daemon connection disappears or is replaced
- `client_revoked` is sent from Relay to the app client when the daemon revokes a previously paired client device

Implemented Step 5 supports app registration, daemon trusted-roster registration, app-side daemon snapshots, account-bound pairing invitation reservation, REST-submitted `pair_response_forward` routing for account-scoped reserved live correlations, `pair_completed` visibility grants, revocation/removal events, live rendezvous hint exchange, and fallback relay tunnel setup. Session index, preview, terminal bytes, input, and resize events remain inside the end-to-end connectivity transport rather than Relay realtime.

Relay's pairing state is a derived live authorization copy. It MUST be invalidated when the daemon revokes trust, and Relay MUST NOT grant new presence visibility, signaling routing, or fallback tunnel issuance to a revoked device.

### Rendezvous

- `rendezvous_open`
- `rendezvous_hint`
- `rendezvous_close`

These events let the app and daemon exchange the minimum hint set needed for direct QUIC attempts.

Recommended hint payload:

- `computer_id`
- `attempt_id`
- `public_udp_addr`
- `private_udp_addrs`
- `expires_at`

Relay must treat these hints as short-lived routing information, not durable device history.

Implemented app-to-Relay open frame:

```json
{
  "type": "rendezvous_open",
  "request_id": "req-1",
  "attempt_id": "attempt-uuid",
  "computer_id": "dev_abcd1234",
  "public_udp_addr": "203.0.113.10:50000",
  "private_udp_addrs": ["10.0.0.5:50000"]
}
```

Relay forwards the app hint to the paired online daemon as:

```json
{
  "type": "rendezvous_hint",
  "request_id": "req-1",
  "attempt_id": "attempt-uuid",
  "computer_id": "dev_abcd1234",
  "client_fingerprint": "<client-device-fingerprint>",
  "actor": "client",
  "public_udp_addr": "203.0.113.10:50000",
  "private_udp_addrs": ["10.0.0.5:50000"],
  "expires_at": 1777478400
}
```

The daemon answers with `rendezvous_hint` containing its candidate addresses.
Relay forwards that hint to the app with `actor: "daemon"` and the same
`attempt_id`. Daemon-origin `rendezvous_hint` and `rendezvous_close` frames
MUST include `client_fingerprint` so Relay can disambiguate app-minted
`attempt_id` values across paired client devices. Either side may send
`rendezvous_close` with `attempt_id` to remove live attempt state. After direct
QUIC/TLS accept succeeds, the daemon sends `direct_session_open` with
`attempt_id`, `computer_id`, and `client_fingerprint`; Relay records that direct
won the attempt. Relay sends `direct_session_close` to the daemon when that
accepted direct path must be canceled because the app session, agent token,
trusted client device, or account is no longer authorized. Relay rejects
unavailable, expired, unpaired, wrong-account, malformed, or superseded attempts
with `reason: "rendezvous_unavailable"`.

#### attempt_id Rules

`attempt_id` is minted by the app client per direct/fallback attempt.

- If the app opens a new `rendezvous_open` for the same app session and `computer_id` while a previous attempt is still in flight, Relay treats the older `attempt_id` as superseded and discards it immediately.
- All rendezvous hints expire after a short phase-1 lifetime. The current Relay default is 30 seconds.

#### private_udp_addrs Hygiene

App clients and daemons SHOULD include private addresses in `private_udp_addrs` only if they are RFC1918, RFC4193, or link-local ranges. Implementations SHOULD cap the list to bound information disclosure.

### Fallback Relay Setup

- `relay_tunnel_request`
- `relay_tunnel_ready`

These events describe fallback tunnel setup. Fallback tunnel teardown is
signaled by closing the WebSocket; no close frame is emitted in Step 5. They do
not carry terminal semantics.

Phase-1 tunnel-token rules:

- Step 4 fallback-only clients may send `relay_tunnel_request` immediately after choosing the fallback path. Step 5 direct-first clients send it only after the direct attempt is judged failed or timed out.
- If Relay accepts fallback for an attempt that still has a pending direct rendezvous, Relay removes the rendezvous and sends `rendezvous_close` to the daemon before issuing fallback tokens.
- If direct already won an attempt through `direct_session_open`, Relay rejects fallback for that same app session, daemon, and `attempt_id`.
- Relay issues one short-lived, single-use tunnel token per side
- each token is bound to:
  - `attempt_id`
  - requesting account and app session
  - app `client_fingerprint`
  - target `computer_id`
  - authenticated actor identity
  - actor type (`client` or `daemon`)
- a token may be redeemed exactly once at the fallback tunnel endpoint

Current Step 4 event payloads:

`relay_tunnel_request` from app to Relay:

- `request_id`
- `attempt_id`
- `computer_id`
- `fallback_reason` (optional)
- `direct_setup_latency_ms` (optional)
- `relay_setup_latency_ms` (optional)

`relay_tunnel_ready` from Relay to each side:

- `request_id`
- `attempt_id`
- `computer_id`
- `client_fingerprint`
- `actor` (`client` or `daemon`)
- `tunnel_token`
- `fallback_reason` (optional)
- `direct_setup_latency_ms` (optional)
- `relay_setup_latency_ms` (optional)

Relay authorizes the request only when the app's authenticated account and
server-side `client_fingerprint` currently have pairing-derived visibility to
the requested online daemon.

`client_fingerprint` is included in both side-specific ready frames. The daemon
uses it to look up the locally trusted client public key before starting the
inner pinned QUIC/TLS listener over the fallback packet tunnel.

Fallback diagnostic fields are app-supplied metadata. Relay forwards them to
both ready frames so the daemon can report them in the daemon-to-app
`path_state`; Relay does not derive or verify transport path semantics.

## Account Policy Surface

Relay is not the computer-selection or per-session entitlement authority in phase 1.

Instead, Relay exposes the current account tier to the official app through authenticated app APIs.

Current minimal shape:

- `account_id`
- `tier`: `free` or `pro`

Phase-1 rules:

- `free` means the official app may keep 1 active trusted computer
- `pro` means the official app may keep up to 10 trusted computers
- Relay does not track active computer selection
- Relay does not track any chosen session row
- Relay does not issue per-session access tokens
- Relay does not fan out tier or session-policy decisions to daemons
- daemon sockets do not receive tier-policy updates

The official app uses `tier` together with client-local trusted-computer state to decide which daemon transports it may open. Once a daemon transport is open, all sessions inside that computer are tier-neutral.

## Relay Tunnel Endpoint

The fallback relay endpoint is not a session attach protocol. It is a packet tunnel for encrypted QUIC traffic.

Properties:

- runs over WebSocket-over-HTTPS
- scoped to one authenticated peer-to-peer attempt
- keyed by Relay-issued short-lived tunnel tokens
- the current path is `GET /connectivity/tunnel/ws` with `Authorization: Bearer <single-use-token>`
- accepts binary WebSocket messages for QUIC packets
- forwards opaque encrypted packets only
- does not parse session frames
- closes active tunnel endpoints when the app session logs out, password change
  disconnects the app, the daemon disconnects, the agent token is revoked, the
  user is deleted, or daemon-local trust for the client fingerprint is revoked

## What Relay Does Not Carry

Relay realtime must not carry:

- session list
- preview text
- interactive snapshot bytes
- live terminal bytes
- input payloads
- daemon-side tier grants

Those belong to the daemon-owned end-to-end transport.

## State Simplifications Compared To WebRTC

The target Relay protocol deliberately removes:

- offer / answer
- ICE candidate trickle
- TURN credentials
- peer connection state taxonomies
- Relay-owned session index
- Relay-owned chosen-session state
- Relay-issued per-session capability tokens

## Acknowledged Downgrade Capability

Relay carries rendezvous hints and authorizes fallback tunnels. A misbehaving Relay can therefore manipulate or withhold rendezvous hints to prevent direct connections from succeeding, forcing the connection onto the fallback tunnel.

Relay cannot decrypt either path because both terminate inside the daemon and client app with pinned device identities.

## Daemon-Side Event Catalog

### Daemon Sends

- `pair_invitation_reserve`
- `computer_register`
- `pair_completed`
- `client_revoked`
- `rendezvous_hint`
- `rendezvous_close`

### Daemon Receives

- `pair_invitation_reserved`
- `pair_response_forward`
- `rendezvous_hint`
- `rendezvous_close`
- `relay_tunnel_ready`
- `error`

Daemon MUST silently ignore unknown event `type` values to allow forward-compatible Relay extensions.

## Server-Side Rate Limits

Client-side reconnect backoff is not sufficient by itself. Relay applies internal rate limits to protect against retry storms and abuse.

Phase-1 recommended defaults:

| Action | Per-account limit | Per-device limit |
|---|---|---|
| `rendezvous_open` | 10 / minute | — |
| `relay_tunnel_request` | 10 / minute | — |
| pairing response submit | — | 10 / minute |

When a limit is exceeded, Relay returns a structured error with `retry_after_seconds`.

## Failure Semantics

Relay failures affect:

- discovering which daemons are online
- transporting pairing responses
- exchanging rendezvous hints
- opening fallback relay tunnels
- fetching current account tier

Relay failures do not grant Relay the ability to read payloads.

When Relay is unavailable:

- existing direct daemon transport may continue
- new pairing cannot complete
- new direct rendezvous cannot start
- fallback relay cannot start
- the official app continues using the last known tier and local trusted-computer policy until it can refresh again

## References

- `../architecture.md`
- `../contract.md`
- `pairing.md`
- `transport.md`
- `../ux/android.md`
- `../ux/subscription.md`
