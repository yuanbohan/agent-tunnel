# Connectivity Relay Protocol Direction

## Status

This document captures the target Relay-owned control-plane protocol for the QUIC session-connectivity architecture. It is a design contract for future implementation, not a claim that the current repository already exposes these endpoints or message types.

## Purpose

Relay now owns only five protocol concerns:

- daemon presence
- active-session selection state
- pairing message transport
- rendezvous hint exchange
- fallback relay-session setup

In addition, Relay remains the source of truth for account-global active-session selection and device-scoped access-token issuance used by the subscription model.

The connectivity edge also needs a STUN service, but STUN itself is infrastructure rather than part of the Relay business protocol described here.

Relay does not own session discovery, preview, or interactive terminal flow.

## Endpoint Shape

The target architecture keeps two authenticated realtime WebSockets:

- app-side realtime WebSocket
- daemon-side realtime WebSocket

They share one envelope style but do not necessarily receive the same startup snapshots.

Separately, Relay exposes a fallback relay endpoint used to tunnel encrypted QUIC packets over WebSocket-over-HTTPS.

The same edge footprint should also expose a self-hosted STUN listener for direct connection attempts. The product should not depend on public third-party STUN infrastructure.

## Shared Envelope

All realtime messages should use one envelope:

- `type`
- `seq`
- `ts`
- `body`

Where:

- `type` is the event family
- `seq` is per-socket monotonic
- `ts` is Relay-generated time
- `body` is the event payload

This keeps logging and reconciliation simple without inventing multiple protocol dialects.

## Startup Snapshots

### App-Side

After authentication succeeds, Relay should send:

1. `daemon_snapshot`
2. `active_session_selection_snapshot`
3. `realtime_ready`

`daemon_snapshot` contains the full daemon roster visible to this Android device:

- pairing-derived visibility
- daemon presence and daemon display metadata

It does not contain sessions.

`active_session_selection_snapshot` contains the currently selected session set for this account. On free tier that set has length `0..1`. On pro it may contain more than one entry.

### Daemon-Side

After daemon authentication succeeds:

1. daemon sends `daemon_register` (see below)
2. Relay sends `access_token_signing_keys`
3. Relay sends `realtime_ready`

The daemon-side socket does not need a startup roster snapshot. The daemon is the trust source for paired mobile devices and does not consume an app-visible daemon list. It does need the current Relay access-token signing public keys so it can validate `session_activate` access tokens offline.

#### `daemon_register`

Sent by the daemon as the first frame after authentication, before `realtime_ready`.

Recommended fields:

- `daemon_id`
- `daemon_display_name` — derived from local config or hostname
- `daemon_pubkey` — Ed25519 device public key
- `platform_family` — `linux` / `macos`
- `platform_id` — finer-grained OS info, for example `darwin/24.4.0` or `ubuntu/22.04`
- `tunnel_version` — semver string used for compatibility-line checks
- `protocol_version` — single integer matching the QUIC protocol version

Relay uses these fields to populate `daemon_snapshot` for app-side consumers and to detect compatibility-line mismatches before the daemon is announced as visible. If `daemon_register` is missing required fields or carries an incompatible `protocol_version`, Relay closes the daemon-side WebSocket with a structured error.

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

### Active Session Selection

- `active_session_selection_snapshot`
- `active_session_selection_changed`

These events let all app devices on the same account converge on the same currently usable session set.

Recommended selection fields:

- `account_id`
- `daemon_id`
- `session_id`
- `selection_epoch`
- `changed_by_device_fingerprint`
- `changed_at`

Phase-1 rules:

- free tier has at most one selected session at a time
- pro may have more than one selected session
- when one device changes the selection, Relay MUST fan out the resulting state to all app realtime sockets on the same account
- daemon sockets do not receive selection snapshots directly; they receive access-token revocation for affected device/session tokens

### Pairing

- `pair_response_submit`
- `pair_response_forward`
- `pair_completed`
- `paired_device_visible`
- `paired_device_revoked`

Relay carries pairing transport, but the daemon remains the trust root.

Event responsibilities:

- `pair_response_submit` is sent from Android to Relay carrying the signed pairing response.
- `pair_response_forward` is sent from Relay to the addressed daemon to deliver the pairing response.
- `pair_completed` is sent from daemon to Relay after both sides have stored trust locally and the SAS has been confirmed.
- `paired_device_visible` is sent from Relay to Android after `pair_completed` so the Android app can update its visible daemon list incrementally without needing a second full snapshot type.
- `paired_device_revoked` is sent from Relay to Android when the daemon revokes a previously paired device. Existing app realtime websockets that belong to the revoked device MUST be closed by Relay as part of revoke fan-out.

Relay's pairing state is a derived authorization copy. It MUST be invalidated when the daemon revokes trust, and Relay MUST NOT grant new presence visibility, signaling routing, or fallback tunnel issuance to a revoked device.

Relay-side revoke fan-out is not sufficient by itself. The daemon is also required to terminate any already-established QUIC connections for that revoked device fingerprint; see `docs/connectivity/pairing-protocol.md`.

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

`attempt_id` is a UUID minted by Android per direct/fallback attempt. It scopes one connection establishment from rendezvous through possible fallback through QUIC handshake completion.

- If Android opens a new `rendezvous_open` for the same `daemon_id` while a previous attempt is still in flight, both daemon and Relay SHOULD treat the older `attempt_id` as superseded and discard its in-flight state after a short grace period (e.g., `5s`).
- All rendezvous hints expire after a phase-1 default of `30s` measured from `rendezvous_open`.

#### private_udp_addrs Hygiene

Android and daemon SHOULD include private addresses in `private_udp_addrs` only if they are RFC1918 (`10/8`, `172.16/12`, `192.168/16`), RFC4193 (`fc00::/7`), or link-local (`169.254/16`, `fe80::/10`) ranges. Implementations SHOULD cap the list at four entries to bound information disclosure and prevent malformed input from bloating Relay state.

### What STUN Does And Does Not Do

STUN is not modeled as a Relay websocket event family because it is not application control traffic.

Its job is much smaller:

- tell a peer its observed public UDP mapping

It does not:

- authenticate pairings
- decide trust
- carry session metadata
- carry terminal payload

Rendezvous events consume STUN results; they do not replace STUN itself.

#### STUN Deployment Shape

Phase 1 self-hosts STUN as part of the same edge footprint as Relay:

- one STUN listener per Relay region on UDP port `3478`
- the STUN endpoint hostname follows a fixed convention derived from the Relay base URL, e.g., `stun.<relay-domain>:3478`
- daemon and Android both resolve the STUN endpoint from their currently configured Relay base URL; no separate configuration knob is exposed
- the protocol used is RFC 5389 / RFC 8489 classic Binding Request and Binding Response only
- ICE-style features (priority, type-preferences, controlling-controlled, candidate-pair lifecycle) are not used

The STUN service is operationally separate from Relay (it does not need to share the same process) but should be deployed in the same regional footprint so that the public-mapping result reflects the network path the eventual UDP traffic will take.

The product MUST NOT depend on public third-party STUN servers in production deployments. They may be used during local development.

### Fallback Relay Setup

- `relay_tunnel_request`
- `relay_tunnel_ready`
- `relay_tunnel_closed`

These events describe the WebSocket-over-HTTPS fallback tunnel lifecycle. They do not carry terminal semantics.

Phase-1 tunnel-token rules:

- `relay_tunnel_request` is sent separately from rendezvous and only after the direct attempt is judged failed or timed out
- Relay issues one short-lived, single-use tunnel token **per side**
- each token is bound to:
  - `attempt_id`
  - authenticated actor identity
  - actor type (`android` or `daemon`)
- a token may be redeemed exactly once at the fallback tunnel endpoint
- Relay must not pre-issue tunnel tokens inside `rendezvous_hint`
- if the same side asks for a second tunnel token for the same `attempt_id`, Relay MUST reject it

### Active-Session Selection And Access Tokens

Relay is the source of truth for account-global active-session selection and device-scoped access-token issuance and renewal.

Recommended responsibilities:

- accept a request to select one `session_id` for one `account_id`
- enforce the account's current active-session selection limit
- update the account-global selected-session set and increment `selection_epoch` when a change is accepted
- return a short-lived signed access token bound to:
  - `account_id`
  - `device_fingerprint`
  - `daemon_id`
  - `session_id`
  - `selection_epoch`
  - `expires_at`
- renew that token while the app is still actively using the selected session
- release or revoke the selection and/or affected tokens when:
  - the user explicitly gives it up
  - the user explicitly replaces it with another selected session
  - the app logs out or switches accounts
  - pairing trust is revoked
  - the selected session disappears
- fan out authoritative selection changes to app clients and token invalidation to daemons when release or revoke happens before token expiry

Phase-1 recommended access-token defaults:

- token TTL: `3 minutes`
- renewal cadence while in active use: every `45 seconds`
- free tier selection limit: `1`
- pro hard cap: `10`

Relay does not send session content itself. The selection and token exist only so that the daemon can distinguish:

- visible session metadata
- real content delivery authorization

The exact token format, signing algorithm, selection semantics, and revocation behavior are specified in `docs/connectivity/subscription-model.md`.

### Access-Token Signing Key Distribution

Relay signs access tokens with a private key that the daemon must verify offline. Public keys are distributed to the daemon over the daemon-side WebSocket.

Phase-1 distribution model:

- on daemon-side WebSocket startup, Relay sends one `access_token_signing_keys` event after `daemon_register` and before `realtime_ready`
- Relay also pushes a fresh `access_token_signing_keys` event whenever the keyset changes (key rotation, key compromise response)
- the daemon persists the most recent received keyset to local disk so that access-token validation continues to work across daemon restarts and during transient Relay outages

Recommended `access_token_signing_keys` payload:

- `keys`: an array of objects, each with:
  - `kid` — short string identifying this key
  - `alg` — phase 1 fixed to `EdDSA`
  - `pubkey` — base64-encoded Ed25519 public key
  - `not_before` — Unix seconds
  - `not_after` — Unix seconds, optional
- `current_kid` — the `kid` of the key Relay is currently signing with

Phase-1 rules:

- daemon MUST keep all received non-expired keys; this allows rolling rotation where two keys are valid simultaneously
- daemon MUST reject tokens whose `kid` is not in the active keyset
- daemon MUST validate token `exp` against its local clock with no additional daemon-side grace window
- if the daemon has never received an `access_token_signing_keys` event (e.g., very first startup with Relay unreachable), it MUST refuse all `session_activate` until at least one keyset is received

### Access-Token Revocation Fan-Out

Relay MUST push an `access_token_revoked` event to the daemon-side WebSocket whenever a still-unexpired token is invalidated before its `exp`.

Typical reasons:

- explicit user release
- explicit user replacement of the selected session
- logout
- account switch
- pairing revoke
- later Relay decision that displaces the old selected session

Recommended `access_token_revoked` payload:

- `jti`
- `device_fingerprint`
- `daemon_id`
- `session_id`
- `reason`
- `revoked_at`

The daemon uses this event to invalidate the corresponding token immediately on direct connections, without waiting for expiry.

## Relay Tunnel Endpoint

The fallback relay endpoint is not a session attach protocol. It is a packet tunnel for encrypted QUIC traffic.

Properties:

- runs over WebSocket-over-HTTPS
- scoped to one authenticated peer-to-peer attempt
- keyed by Relay-issued short-lived tunnel tokens
- forwards opaque encrypted packets only
- does not parse session frames

This is intentionally narrower than TURN and narrower than the old attach WebSocket.

## What Relay Does Not Carry

Relay realtime must not carry:

- session list
- preview text
- interactive snapshot bytes
- live terminal bytes
- input payloads
- interactive grant state

Those now belong to the daemon-owned end-to-end transport.

## State Simplifications Compared To WebRTC

The target Relay protocol deliberately removes:

- offer / answer
- ICE candidate trickle
- TURN credentials
- peer connection state taxonomies
- Relay-owned interactive grant state
- session index snapshots

This is the main complexity win of the QUIC direction.

## Acknowledged Downgrade Capability

Relay carries rendezvous hints and authorizes fallback tunnels. A misbehaving Relay can therefore manipulate or withhold rendezvous hints to prevent direct connections from succeeding, forcing the connection onto the fallback tunnel.

Relay cannot decrypt either path because both terminate inside the daemon and Android with pinned device identities. The product accepts this downgrade capability as the price of using Relay for rendezvous; it does not blur the confidentiality guarantee on either path.

## Daemon-Side Event Catalog

The daemon-side WebSocket carries the following events. This catalog is the authoritative list daemon implementations must handle.

### Daemon Sends

- `daemon_register` — startup registration with display metadata
- `pair_completed` — final ack after SAS confirmation
- `rendezvous_hint` — daemon's STUN-derived candidates for a given `attempt_id`
- `relay_tunnel_request` — daemon-side request for a fallback tunnel token

### Daemon Receives

- `access_token_signing_keys` — initial and rotation push of Relay's access-token verification keys
- `access_token_revoked` — authoritative invalidation of a still-unexpired access token
- `realtime_ready` — startup completion marker
- `pair_response_forward` — Android's signed pairing response, routed by Relay
- `paired_device_revoked` — daemon already revoked an Android device locally; this is the inverse direction where Relay surfaces a revoke that originated elsewhere (e.g., admin tool in a future phase). Phase-1 daemon implementations may treat unsolicited revokes as advisory.
- `rendezvous_open` — Android wants to establish a connection
- `rendezvous_hint` — Android's candidates for a given `attempt_id`
- `rendezvous_close` — Android cancels or completed an attempt
- `relay_tunnel_ready` — daemon's tunnel token is ready

Daemon MUST silently ignore unknown event `type` values to allow forward-compatible Relay extensions.

## Server-Side Rate Limits

Client-side reconnect backoff is not sufficient by itself. Relay applies internal rate limits to protect against retry storms and abuse. These limits are operational concerns and are not surfaced as user-facing subscription rules.

Phase-1 recommended defaults:

| Action | Per-account limit | Per-device limit |
|---|---|---|
| `rendezvous_open` | 10 / minute | — |
| `relay_tunnel_request` | 10 / minute | — |
| selection change / token issue / renew | 30 / minute | 60 / minute |
| pairing response submit | — | 10 / minute |

When a limit is exceeded, Relay returns a structured error with `retry_after_seconds`. Clients SHOULD honor `retry_after_seconds` instead of immediately retrying with their normal exponential backoff.

These numbers are conservative starting points. Real values should be tuned from production data once usage patterns are observable.

## Failure Semantics

Relay failures affect:

- discovering which daemons are online
- transporting pairing responses
- exchanging rendezvous hints
- opening fallback relay tunnels

Relay failures do not grant Relay the ability to read payloads.

When Relay is unavailable:

- existing direct daemon transport may continue
- new pairing cannot complete
- new direct rendezvous cannot start
- fallback relay cannot start

## References

- `docs/connectivity/architecture.md`
- `docs/connectivity/pairing-protocol.md`
- `docs/connectivity/transport-protocol.md`
- `docs/connectivity/mobile-reference.md`
- `docs/connectivity/sequence-flows.md`
