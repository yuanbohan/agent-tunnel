# QUIC Session Connectivity Architecture

## Status

This document records the current architecture direction for direct session connectivity. It is a design anchor for future implementation, not a statement that the repository already ships this transport.

Use this document together with:

- `docs/connectivity/decision-record.md`
- `docs/connectivity/relay-protocol.md`
- `docs/connectivity/transport-protocol.md`
- `docs/connectivity/pairing-protocol.md`
- `docs/connectivity/daemon-session-sync.md`
- `docs/connectivity/subscription-model.md`
- `docs/connectivity/mobile-reference.md`
- `docs/connectivity/sequence-flows.md`

## Goals

- Prefer direct mobile-to-daemon connectivity for session traffic.
- Keep terminal payload end-to-end encrypted on both direct and fallback paths.
- Reduce Relay to account, presence, rendezvous, pairing transport, and fallback packet relay.
- Remove WebRTC-specific signaling, TURN, and DataChannel complexity from the product design.
- Keep user operation simple: account login, `tunnel daemon pair`, mobile scan, then use.

## Non-Goals

- No device-wide VPN or machine overlay.
- No Relay-resident session index in the target design.
- No plaintext preview or terminal content in Relay.
- No dependency on WebRTC, coturn, or SDP/ICE negotiation.
- No attempt to hide daemon presence or account ownership metadata from Relay.

## System Shape

The system is split into four concerns:

1. Account and entitlement
2. Device trust and pairing
3. Relay rendezvous and fallback
4. End-to-end session transport

### 1. Account And Entitlement

The account system remains because it solves:

- subscriptions and entitlements
- multiple phones and multiple computers under one paid identity
- app login and device ownership
- Relay authorization for daemon presence, pairing transport, and fallback usage
- Relay-owned active-session leases

The account system is not the payload trust model.

In phase 1, subscription does not decide which sessions exist. It decides only how many sessions the mobile app may actively use at the same time.

### 2. Device Trust And Pairing

Each daemon and each Android app installation owns one long-lived device key.

Pairing rules:

- Android must already be logged in.
- Same-account devices are not automatically trusted.
- Trust is daemon-scoped.
- `tunnel daemon pair` creates a short-lived, one-time invitation.
- The daemon is the trust root for approval.
- Relay may carry pairing messages, but it does not decide trust.
- Both sides persist the other side's public-key fingerprint locally after successful pairing.

The pairing contract is specified in `docs/connectivity/pairing-protocol.md`.

### 3. Relay Rendezvous And Fallback

Relay remains in the system, but its role is narrower.

Relay is responsible for:

- account authentication
- entitlement checks
- daemon presence
- pairing request and response transport
- rendezvous hint exchange for direct connection attempts
- fallback packet relay over WebSocket-over-HTTPS

Relay is not responsible for:

- session discovery authority
- preview generation
- interactive lease authority
- terminal byte routing semantics
- payload decryption

### 4. End-To-End Session Transport

After pairing, Android and the daemon connect over one authenticated, encrypted transport per daemon.

Transport rules:

- direct path: QUIC over UDP
- fallback path: QUIC packets tunneled through Relay over WebSocket-over-HTTPS
- the same daemon-side session protocol runs on either path
- Relay only sees encrypted transport packets on the fallback path

The transport contract is specified in `docs/connectivity/transport-protocol.md`.

## Security Model

### Transport Security

The selected security model is:

- QUIC with TLS 1.3 session encryption
- peer verification by pinned device public-key fingerprint
- no public CA trust requirement
- no second application-layer encryption envelope in phase 1

The product requirement is not "use Noise" by name. The requirement is:

- device-authenticated end-to-end encryption
- Relay cannot read terminal payloads
- direct and fallback paths share one security model

This design should be described as:

- `QUIC/TLS 1.3 + device-key pinning`

not:

- `WebRTC`
- `WireGuard`
- `Noise-IK over QUIC`

### Cryptographic Building Blocks

The architecture should be understood as two different key layers:

1. long-lived device identity keys
2. per-connection session keys

The current preferred direction is:

- device identity and pairing signatures: `Ed25519`
- TLS 1.3 ephemeral key exchange: `X25519` or implementation-selected TLS 1.3 ECDHE group
- key derivation: `HKDF`
- packet encryption: TLS 1.3 AEAD such as `AES-GCM` or `ChaCha20-Poly1305`

The exact negotiated TLS cipher suite is an implementation detail, but the architectural rule is stable:

- pairing establishes who the peer is
- TLS 1.3 establishes fresh symmetric keys for this connection
- terminal payload is encrypted only with the per-connection session keys, not with the long-lived device identity keys directly

### Why Pairing And Transport Are Separate

Pairing answers:

- who is this device
- do I trust this device

QUIC/TLS answers:

- can this peer prove it owns the trusted device identity
- what fresh symmetric keys should protect this connection

That split is important because:

- compromising a long-lived identity key should not automatically decrypt old transport captures
- transport encryption should rotate naturally per connection
- Relay should never participate in session-key derivation

### Pairing Confirmation

Pairing includes a human-visible 6-digit SAS derived from both device identities and the current invitation context. Users confirm that both screens show the same number before trust is finalized.

This closes the remaining "Relay swapped keys during pairing transport" class of attack without introducing a heavyweight UX.

The SAS gives approximately 20 bits of MITM resistance, which is on par with similar peer-to-peer pairing protocols such as ZRTP, Signal safety numbers, and Matrix Olm key verification. The exact algorithm and inputs are pinned in `docs/connectivity/pairing-protocol.md`.

### Threat Model Summary

The following table summarizes the threats this architecture defends against, what defense applies, and what residual risk remains.

| Threat | Defense | Residual Risk |
|---|---|---|
| Relay reads terminal bytes | QUIC + TLS 1.3 with peer cert pinned to device key from pairing | None |
| Relay or network attacker substitutes TLS keys to MITM the transport | Public-key pinning at the QUIC/TLS layer; cert chain validation is bypassed and only SubjectPublicKeyInfo equality is checked | None |
| Relay swaps device keys during pairing transport | 6-digit SAS confirmed by the user on both screens before trust is finalized | User must actively confirm; click-fatigue is mitigated by `pairing-protocol.md` UX rules |
| Relay tampers with rendezvous candidates | Inner QUIC/TLS handshake will fail against a wrong endpoint; cert pinning catches the substitution | Relay can force-downgrade the path from direct to fallback; confidentiality is preserved on either path |
| Network attacker captures past traffic and later steals the long-lived device key | TLS 1.3 ECDHE per connection provides forward secrecy on session keys | Past sessions remain secret; future sessions can be impersonated until the affected key is revoked |
| QR invitation is photographed by a bystander | Invitation is one-time, expires quickly, and is bound to a specific Android device key by signature | Invitation alone cannot pair an attacker's device; SAS would mismatch even if attacker tries |
| Pairing response is replayed by Relay | Invitation is single-use; reuse fails closed | None |
| Daemon device key is exfiltrated from the host | Pairing trust is per-pair; user must `tunnel daemon revoke` and re-pair all paired Android devices | All paired Android devices must re-pair; no in-band recovery |
| Android device key is stolen with the device | Daemon-side `tunnel daemon revoke <device>` removes trust, immediately terminates active QUIC connections for that fingerprint, and Relay revoke fan-out blocks new visibility and fallback issuance | Brief in-flight packets already on the wire may still arrive until transport close propagates |

This table should be updated whenever a new defense or new known residual risk is identified.

### Acknowledged Downgrade Capability

Relay sits in the path that exchanges rendezvous hints and authorizes fallback tunnels. A misbehaving Relay can therefore:

- prevent direct connection attempts from succeeding by manipulating or withholding rendezvous hints
- force the connection to use the fallback relay tunnel

It cannot, however, decrypt either path because both terminate inside the daemon and Android with pinned device identities. The product accepts this downgrade capability as the price of using Relay for rendezvous; it does not blur the confidentiality guarantee on either path.

### Daemon Key Compromise

If a daemon's long-lived device key is exfiltrated from the host, the architecture cannot in-band revoke that key. Recovery is operational:

- the user generates a new daemon device key (typically by reinstalling or rotating identity files)
- all previously paired Android devices must repeat `tunnel daemon pair` against the new identity
- the old fingerprint becomes permanently invalid in each Android device's local trust store after the new pair completes

Phase 1 does not provide automatic key rotation or daemon-key revocation through Relay. Operators must treat daemon device keys with care comparable to SSH host keys.

## Security Walkthrough

### What Pairing Produces

Successful pairing produces:

- Android stores the daemon public-key fingerprint
- daemon stores the Android public-key fingerprint
- both sides remember that this peer is trusted for future transport authentication

It does not produce a long-lived shared transport secret.

### What A Later Connection Does

On a later direct or relay-backed connection:

1. Android and daemon establish a QUIC connection
2. QUIC uses TLS 1.3 to perform authenticated key exchange
3. each side verifies that the peer matches the pinned device identity from pairing
4. TLS derives fresh symmetric traffic secrets for this one connection
5. session metadata, preview, and interactive traffic flow over that encrypted connection

This means:

- pairing trust is long-lived
- session encryption keys are short-lived
- Relay never learns the payload keys

## Source Of Truth Boundaries

### Relay Is The Source Of Truth For

- account ownership
- entitlements
- daemon presence
- daemon display metadata
- pairing transport coordination
- rendezvous coordination
- fallback relay authorization

### Daemon Is The Source Of Truth For

- session list
- session metadata such as `label`, `command_preview`, `cwd`, and `git_branch`
- preview text for sessions whose Relay-issued active-session lease has been accepted
- interactive session ownership
- terminal snapshots
- live terminal bytes

Relay remains the source of truth for subscription and active-session lease issuance. The daemon enforces lease tokens but does not know free vs pro tier details.

In the target architecture, session discovery happens after Android establishes a secure daemon transport connection. `GET /api/sessions` is not part of the target design.

## STUN And Edge Ownership

The product should not depend on public third-party STUN servers.

Instead, the same edge footprint that operates Relay should also operate the STUN service used for direct connection attempts.

Recommended shape:

- Relay business/control plane service
- self-hosted STUN service
- fallback relay tunnel service

These may share deployment infrastructure, but they should remain distinct service responsibilities.

Rationale:

- public STUN availability is not reliable enough for product infrastructure
- self-hosted STUN keeps regional routing and observability under product control
- STUN is a lightweight infrastructure component compared with fallback relay or transport itself

## Connection Model

### Daemon Granularity

Android maintains one end-to-end transport connection per visible online daemon.

This transport is daemon-scoped rather than session-scoped because:

- one daemon may expose multiple sessions
- one user may monitor several sessions at once
- per-session transport fanout adds connection count and state with little benefit

### Session Granularity

Within one daemon connection:

- daemon pushes current session metadata
- daemon pushes preview updates
- Android may request interactive attach for any session independently
- the daemon may grant or deny interactive ownership per session

### Interactive Rule

The transport must not assume there is only one interactive session at a time.

Interactive state is session-scoped:

- one session may be interactive while others are preview-only
- multiple sessions may be interactive concurrently if the client chooses to keep them attached
- daemon decisions are made per session, not by a global single-interactive lock

## Stream Model

The selected default is one long-lived `control` stream plus zero or more `interactive` streams per daemon connection:

- one long-lived `control` stream
- one short-lived `interactive` stream per attached interactive session

Why not one stream for everything:

- large snapshot chunks should not block metadata and preview updates
- QUIC streams are cheap
- two streams preserve simple ordering without WebRTC-like channel machinery

Why not many streams in phase 1:

- preview data is lightweight and fits the control stream
- QUIC streams are cheap enough that one-per-interactive-session stays simple
- the implementation still stays smaller than the old WebRTC/DataChannel direction

## State Model

The transport state should be maintained per daemon connection, not per session.

Recommended daemon-connection state:

- `offline`
- `connecting_direct`
- `connecting_relay`
- `connected_direct`
- `connected_relay`
- `reconnecting`

Recommended per-session state under a daemon connection:

- current session metadata
- current preview snapshot, if any
- whether this session currently holds interactive ownership
- the interactive stream id, if attached

This separation keeps the app and daemon from inventing a false model where each session independently chooses `direct` vs `relay`.

## Direct And Fallback Strategy

Phase 1 uses a conservative direct-first algorithm with a fixed deadline:

1. Android learns the daemon is online through Relay presence.
2. Android and daemon exchange rendezvous hints through Relay.
3. Both sides attempt direct QUIC over UDP.
4. If the direct QUIC handshake has not completed within `3s` (the **direct attempt deadline**), Android and daemon open fallback relay tunnels.
5. They establish a new QUIC connection through the relay tunnel.
6. After transport becomes ready, daemon pushes session state and preview data.

The direct attempt deadline is a sequential decision rather than a parallel "happy eyeballs" race. Sequential keeps the state machine and the UX badge unambiguous in phase 1; happy eyeballs may be revisited later if the direct success rate is high enough that paying the relay tunnel setup cost on every attempt becomes wasteful.

Reconnect attempts after a transport drop use exponential backoff with full jitter: base `1s`, cap `60s`, reset to base on a successful connection. Each daemon connection manager backs off independently so that multi-daemon environments do not synchronize their retries.

The direct path and fallback path share one session protocol. The app can always expose a user-facing path badge:

- `Direct`
- `Relay`

### Path Badge Semantics

The badge is informational only. Both the direct and the fallback paths use the same end-to-end encryption and the same pinned-identity authentication; Relay never sees terminal plaintext on either path. The badge primarily indicates expected latency: `Direct` is typically lower latency than `Relay`. The user is not expected to take security-relevant action based on it. Mobile UI copy should not imply that `Relay` is "less secure" than `Direct`.

### Direct Success Sequence

The intended direct happy path is:

1. Relay tells Android that a paired daemon is online
2. Android and daemon each learn their public UDP mapping through STUN
3. Relay carries rendezvous hints between them
4. both sides send UDP packets toward the candidate addresses
5. if NAT state allows it, a direct packet path opens
6. QUIC/TLS completes on that direct path
7. daemon pushes `session_index`, preview, and any interactive streams

### Fallback Sequence

If direct does not succeed quickly:

1. Android and daemon both open fallback relay tunnels to Relay
2. Relay pairs those tunnels for the current attempt id
3. QUIC/TLS is established across that relay tunnel
4. daemon re-sends `session_index`, preview, and any interactive streams needed by the client

This is product-level seamlessness, not transport migration within one existing QUIC connection.

## Liveness And Backpressure

The protocol relies on QUIC PING frames for liveness. Recommended phase-1 transport parameters:

- idle timeout: `30s`
- keep-alive interval: `15s`

No application-layer heartbeat is required. Implementations should not invent one.

Daemon implementations must isolate per-session output queues so that backpressure from a slow Android consumer on one interactive stream does not block other interactive streams or the control stream. Each interactive stream has its own QUIC stream-level flow control window; the daemon should write per-stream from independent goroutines or equivalent isolation primitives so that a stalled reader on one session never starves the others.

## Session UX Implications

The target UX changes from Relay-owned session discovery to daemon-owned session discovery.

That means:

- app startup can immediately show daemon cards from Relay presence
- per-daemon sessions appear after secure daemon connectivity is established
- previews appear only after the daemon transport is ready and the session holds an active-session lease
- no preview cache is required in phase 1
- free users may still see all session rows, but non-leased sessions remain locked and do not receive real preview or interactive content

This is a conscious tradeoff in favor of lower protocol complexity and stricter payload privacy.

## Recommended Implementation Stack

### Daemon

- `quic-go` for QUIC transport
- a local packet-conn adapter for fallback relay tunneling
- `pion/stun` or an equivalently small STUN client for public endpoint discovery

### Android

- a dedicated custom QUIC client implementation suitable for arbitrary bidirectional streams
- phase-1 recommended investigation target: `quiche` via JNI
- Android Keystore-backed persistent device identity where possible

The architecture should not assume Cronet as the application transport API. Cronet is primarily an HTTP stack and is not a good architectural anchor for a custom QUIC stream protocol.

## Rejected Directions

- WireGuard / overlay transport: too heavy for a session-only feature.
- WebRTC DataChannels: workable but too much negotiation, ICE, TURN, and protocol surface for a byte-stream product.
- Relay-owned session index: keeps too much business state and too much routing state in Relay.
- Single transport stream for all traffic: simpler on paper, worse in practice when snapshots and control messages contend.

The reasons are recorded in `docs/connectivity/decision-record.md`.

## References

- `docs/connectivity/decision-record.md`
- `docs/connectivity/relay-protocol.md`
- `docs/connectivity/transport-protocol.md`
- `docs/connectivity/pairing-protocol.md`
- `docs/connectivity/daemon-session-sync.md`
- `docs/connectivity/subscription-model.md`
- `docs/connectivity/mobile-reference.md`
- `docs/connectivity/sequence-flows.md`
- QUIC transport: `https://www.ietf.org/rfc/rfc9000.html`
- QUIC + TLS integration: `https://datatracker.ietf.org/doc/html/rfc9001`
- TLS 1.3: `https://datatracker.ietf.org/doc/html/rfc8446`
- X25519: `https://datatracker.ietf.org/doc/html/rfc7748`
- Ed25519: `https://datatracker.ietf.org/doc/html/rfc8032`
