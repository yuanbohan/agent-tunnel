# Connectivity Relay Protocol Direction

## Status

This document captures the target Relay-owned control-plane protocol for the QUIC connectivity architecture. It is a design contract for future implementation, not a claim that the current repository already exposes these endpoints or message types.

## Purpose

Relay owns only these protocol concerns:

- daemon presence
- pairing message transport
- rendezvous hint exchange
- fallback relay-tunnel setup
- subscription tier exposure to the official app

Relay does not own:

- session discovery
- preview authority
- interactive authority
- per-session daemon-side authorization

The connectivity edge also needs a STUN service, but STUN itself is infrastructure rather than part of the Relay business protocol described here.

## Endpoint Shape

The target architecture keeps two authenticated realtime WebSockets:

- app-side realtime WebSocket
- daemon-side realtime WebSocket

They share one envelope style but do not necessarily receive the same startup snapshots.

Separately, Relay exposes:

- a fallback relay endpoint used to tunnel encrypted QUIC packets over WebSocket-over-HTTPS
- authenticated app APIs that expose the current subscription tier

## App Authentication Model

Phase 1 uses one simple Relay-issued app-session JWT for both app APIs and the app-side realtime WebSocket. See `../contract.md` D4.

### Device Fingerprint Binding

The Android app generates a long-lived device key on first authenticated setup (in Android Keystore where available). The fingerprint is:

```
device_fingerprint = sha256(public_key_raw_bytes)   // 32 bytes, hex-encoded for transport
```

Login flow:

1. Android sends `POST /auth/login` with body `{ username, password, device_fingerprint }`.
2. Relay validates credentials and persists `(account_id, sid, device_fingerprint)` server-side.
3. Relay returns a JWT carrying these claims.

JWT claims:

- `sub` = account identifier
- `device_fingerprint` = the value supplied at login (Relay echoes it for daemon-side comparison)
- `sid` = app-session identifier
- `exp`

Token refresh requires the same `device_fingerprint`; mismatch is rejected with `relay_account_mismatch`.

Relay uses the authenticated account plus `device_fingerprint` from the JWT claims as the app-side identity for:

- pairing-derived daemon visibility
- pairing response routing
- subscription-tier reads

### Phase-1 Simplicity Tradeoff

Phase 1 does not require a per-WebSocket cryptographic proof that the JWT holder owns the device's private key. That tradeoff is accepted because daemon-side pinned device keys still protect direct and relay transport access; Relay-side app identity stays as simple as a normal authenticated app session.

Phase-2 may upgrade this by introducing `/auth/register-device` that requires the client to sign a Relay challenge with the device key, raising the JWT to a proof-of-possession token (`../contract.md` open TODO `T-AUTH-POP`).

## Shared Envelope

All realtime messages use one envelope:

- `type`
- `seq`
- `ts`
- `body`

Where:

- `type` is the event family
- `seq` is per-socket monotonic
- `ts` is Relay-generated time
- `body` is the event payload

## Startup Snapshots

### App-Side

After authentication succeeds:

1. app sends `app_register`
2. Relay sends `daemon_snapshot`
3. Relay sends `realtime_ready`

#### `app_register`

Sent by Android as the first frame after app-session authentication, before `daemon_snapshot`.

Recommended fields:

- `app_version`
- `protocol_version`

`daemon_snapshot` contains the full daemon roster visible to this Android device:

- pairing-derived visibility
- daemon presence and daemon display metadata

It does not contain sessions.

Relay determines the Android device identity from the authenticated app-session JWT, then uses that `device_fingerprint` plus the authenticated account session to compute pairing-derived visibility.

The app learns its current subscription tier through authenticated Relay app APIs, not through realtime per-session policy snapshots.

### Daemon-Side

After daemon authentication succeeds:

1. daemon sends `daemon_register`
2. Relay sends `realtime_ready`

The daemon-side socket does not need startup session or subscription state.

#### `daemon_register`

Sent by the daemon as the first frame after authentication, before `realtime_ready`.

Recommended fields:

- `daemon_id`
- `daemon_display_name`
- `daemon_pubkey`
- `platform_family`
- `platform_id`
- `tunnel_version`
- `protocol_version`

Relay uses these fields to populate `daemon_snapshot` for app-side consumers and to detect compatibility mismatches before the daemon is announced as visible.

## Event Families

### Presence

- `daemon_snapshot`
- `daemon_upsert`
- `daemon_remove`

Recommended daemon fields:

- `daemon_id`
- `daemon_display_name`
- `online`
- `last_seen_at`
- `platform_family`
- `platform_id`

### Pairing

- `pair_invitation_reserve`
- `pair_invitation_reserved`
- `pair_response_submit`
- `pair_response_forward`
- `pair_completed`
- `paired_device_visible`
- `paired_device_revoked`

Relay carries pairing transport, but the daemon remains the trust root.

Event responsibilities:

- `pair_invitation_reserve` is sent from daemon to Relay to reserve a short-lived `correlation_id`
- `pair_invitation_reserved` is sent from Relay to daemon carrying the reserved `correlation_id`
- `pair_response_submit` is sent from Android to Relay carrying the signed pairing response
- `pair_response_forward` is sent from Relay to the addressed daemon
- `pair_completed` is sent from daemon to Relay after both sides have stored trust locally and the SAS has been confirmed
- `paired_device_visible` is sent from Relay to Android after `pair_completed`
- `paired_device_revoked` is sent from Relay to Android when the daemon revokes a previously paired device

Relay's pairing state is a derived authorization copy. It MUST be invalidated when the daemon revokes trust, and Relay MUST NOT grant new presence visibility, signaling routing, or fallback tunnel issuance to a revoked device.

### Rendezvous

- `rendezvous_open`
- `rendezvous_hint`
- `rendezvous_close`

These events let the app and daemon exchange the minimum hint set needed for direct QUIC attempts.

Recommended hint payload:

- `daemon_id`
- `attempt_id`
- `public_udp_addr`
- `private_udp_addrs`
- `expires_at`

Relay must treat these hints as short-lived routing information, not durable device history.

#### attempt_id Rules

`attempt_id` is a UUID minted by Android per direct/fallback attempt.

- If Android opens a new `rendezvous_open` for the same `daemon_id` while a previous attempt is still in flight, both daemon and Relay SHOULD treat the older `attempt_id` as superseded and discard its in-flight state after a short grace period.
- All rendezvous hints expire after a short phase-1 lifetime.

#### private_udp_addrs Hygiene

Android and daemon SHOULD include private addresses in `private_udp_addrs` only if they are RFC1918, RFC4193, or link-local ranges. Implementations SHOULD cap the list to bound information disclosure.

### Fallback Relay Setup

- `relay_tunnel_request`
- `relay_tunnel_ready`
- `relay_tunnel_closed`

These events describe the WebSocket-over-HTTPS fallback tunnel lifecycle. They do not carry terminal semantics.

Phase-1 tunnel-token rules:

- `relay_tunnel_request` is sent separately from rendezvous and only after the direct attempt is judged failed or timed out
- Relay issues one short-lived, single-use tunnel token per side
- each token is bound to:
  - `attempt_id`
  - authenticated actor identity
  - actor type (`android` or `daemon`)
- a token may be redeemed exactly once at the fallback tunnel endpoint

## Subscription Policy Surface

Relay is not the per-session subscription authority in phase 1.

Instead, Relay exposes the current app policy to the official app through authenticated app APIs.

Recommended minimal shape:

- `tier`: `free` or `pro`

Phase-1 rules:

- Relay does not track any account-global chosen session row
- Relay does not issue per-session access tokens
- Relay does not fan out per-session subscription decisions to daemons
- daemon sockets do not receive subscription-policy updates

The official app uses `tier` together with daemon-owned `session_index` data to determine which rows are locked or usable.

## Relay Tunnel Endpoint

The fallback relay endpoint is not a session attach protocol. It is a packet tunnel for encrypted QUIC traffic.

Properties:

- runs over WebSocket-over-HTTPS
- scoped to one authenticated peer-to-peer attempt
- keyed by Relay-issued short-lived tunnel tokens
- forwards opaque encrypted packets only
- does not parse session frames

## What Relay Does Not Carry

Relay realtime must not carry:

- session list
- preview text
- interactive snapshot bytes
- live terminal bytes
- input payloads
- daemon-side subscription grants

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

Relay cannot decrypt either path because both terminate inside the daemon and Android with pinned device identities.

## Daemon-Side Event Catalog

### Daemon Sends

- `pair_invitation_reserve`
- `daemon_register`
- `pair_completed`
- `rendezvous_hint`
- `relay_tunnel_request`

### Daemon Receives

- `pair_invitation_reserved`
- `realtime_ready`
- `pair_response_forward`
- `paired_device_revoked`
- `rendezvous_open`
- `rendezvous_hint`
- `rendezvous_close`
- `relay_tunnel_ready`

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
- fetching current subscription tier

Relay failures do not grant Relay the ability to read payloads.

When Relay is unavailable:

- existing direct daemon transport may continue
- new pairing cannot complete
- new direct rendezvous cannot start
- fallback relay cannot start
- the official app continues using the last known tier/policy until it can refresh it again

## References

- `../architecture.md`
- `../contract.md`
- `pairing.md`
- `transport.md`
- `../ux/android.md`
- `../ux/subscription.md`
