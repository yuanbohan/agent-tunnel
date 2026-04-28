# Connectivity Transport Decision Record

## Status

Accepted on 2026-04-24 as the current architecture direction for future implementation planning.

## Decision

The project should move to:

- QUIC transport
- TLS 1.3 session encryption
- device-key pinning based on pairing
- direct UDP first
- Relay fallback by tunneling encrypted QUIC packets over WebSocket-over-HTTPS

The project should not continue with:

- WireGuard / overlay transport
- WebRTC DataChannels

## What This Decision Means In Practice

This choice should be read as:

- pairing provides pinned long-lived device identity
- direct and relay fallback both run the same QUIC/TLS transport
- direct and relay differ only in how packets are carried
- session discovery, preview, and interactive traffic all come from the daemon after transport is established

## Context

The product goal is narrower than general device networking:

- Android must reach one daemon directly when possible
- terminal payloads must stay end-to-end encrypted
- Relay must fall back to opaque forwarding only when direct connectivity fails
- the user experience must stay simple
- Relay should carry less long-lived session state, not more

That goal does not require a VPN, device overlay, or media-oriented signaling stack.

## Option 1: WireGuard / Overlay

### Why It Was Attractive

- strong device-to-device security model
- clean direct/relay split in theory
- proven in products such as Tailscale

### Why It Was Rejected

- too heavy for a session-only feature
- introduces VPN / overlay concepts the product does not need
- pushes implementation toward TUN, helper, or OS-level networking concerns
- still leaves control-plane and fallback design work unsolved

WireGuard is a strong answer for general device networking. It is not the best answer for "secure session transport only".

## Option 2: WebRTC

### Why It Was Attractive

- direct and relay paths already exist as a common ecosystem pattern
- payload encryption is built in
- NAT traversal is well known

### Why It Was Rejected

- too much negotiation machinery for a byte-stream product
- introduces SDP offer/answer, ICE candidate flow, DTLS, SCTP, and DataChannel semantics
- requires TURN / coturn operational surface
- kept too much complexity in Relay and too many transport states in the app
- encouraged keeping Relay-aware session discovery and interactive state machines

WebRTC could have worked. It was rejected because it solves a broader and different problem than this product has.

## Option 3: QUIC/TLS + Device-Key Pinning

### Why It Was Selected

- one transport protocol for both direct and fallback
- simpler state model than WebRTC
- no SDP / ICE / TURN / DataChannel stack
- QUIC streams map naturally to control and interactive byte flow
- Relay can shrink to presence, pairing transport, rendezvous, and packet relay
- pairing remains the device trust root

This option best matches the actual product boundary:

- secure daemon-to-mobile session transport
- daemon-owned session authority
- Relay as control plane and blind fallback

## Android Transport Follow-Up

Earlier brainstorming tentatively chose Cronet for Android stability. After source review, that is no longer the recommended direction.

### Why Cronet Was Not Kept

Cronet is an HTTP stack with HTTP/3 over QUIC support. It is not a good architecture anchor for a custom arbitrary-stream application protocol.

### Selected Android QUIC Library

Confirmed on 2026-04-26: phase-1 Android implementation uses **Cloudflare quiche via JNI**.

Rationale:

- mature production use at Cloudflare
- supports custom application protocols over arbitrary QUIC streams (not HTTP-only)
- exposes the certificate verification callback needed for device-key pinning
- daemon-side `quic-go` and Android-side `quiche` both implement RFC 9000/9001 and have been demonstrated to interop in third-party deployments

Step 1 repository prerequisite before higher-level features are built:

- run a small Go mobile-simulator spike where `quic-go` listens with ALPN
  `tunnel-conn/1`, both Go endpoints complete the QUIC/TLS handshake using
  self-signed Ed25519 certificates, both sides verify peer SPKI pins, and the
  Android-facing frame/data sequence passes over direct UDP and the Relay-like
  packet carrier

FIXME(Android): Real Android `quiche` JNI/emulator/device validation remains
required before production Android compatibility is claimed. That validation
must run the same pinned TLS and stream/data exchange recorded by the Go
simulator.

Phase-1 fallback if `quiche` packaging proves unworkable on Android:

- `kwik` (pure Java QUIC) as a smaller-binary alternative

The fallback is not the default. It exists only to unblock implementation if a hard packaging issue is discovered with quiche.

## Relay Fallback Clarification

The fallback path should not be described as a generic session byte pipe.

It is more accurate to describe it as:

- a Relay-hosted WebSocket tunnel that forwards encrypted QUIC packets

That keeps one end-to-end security model while avoiding TURN.

## Phase-1 App Identity Simplification

Confirmed on 2026-04-27: phase 1 uses a Relay-issued app-session JWT as the only app-side Relay authentication mechanism.

The JWT should carry:

- account identity
- app-session identifier
- Android app `device_fingerprint`

This replaces the earlier idea of requiring an additional per-websocket `device_proof` on `app_register`.

Why this simplification was chosen:

- simpler Relay control-plane implementation
- fewer moving parts before coding plan
- still sufficient for Relay-side daemon visibility decisions
- direct and relay transport security still rely on daemon-side pairing trust and device-key pinning

Tradeoff:

- Relay-side Android device identity is only as strong as the authenticated app session, not a second websocket-bound signature proof

That tradeoff is accepted for phase 1.

## Phase-1 Daemon Card Connection Strategy

Confirmed on 2026-04-27: the official app should not eagerly connect to every visible online daemon in phase 1.

Instead:

- Relay presence renders daemon cards
- daemon transport starts only when the user opens a daemon card
- once connected, free/pro rules are evaluated only within that card
- pro may auto-subscribe preview for every live session in that opened card

Why this simplification was chosen:

- keeps the first version smaller
- avoids foreground fan-out to every visible daemon
- avoids viewport and idle-eviction policy work
- still gives a good pro experience once a daemon card is opened

Tradeoff:

- pro no longer sees all live previews across every visible daemon immediately on app open
- preview richness is scoped to opened daemon cards in phase 1

## Consequences

### Positive

- less protocol surface
- fewer connection states
- no Relay-owned session index in the target design
- simpler mobile implementation model
- clearer security boundary

### Negative

- session list cannot appear until the daemon transport is up
- Android transport implementation is no longer "just use libwebrtc"
- direct success rate must be measured in production instead of assumed from generic WebRTC numbers

## Public References

- QUIC streams and transport primitives: `https://github.com/quic-go/quic-go`
- Android custom QUIC candidate: `https://github.com/cloudflare/quiche`
- Cronet as HTTP/3 stack, not custom transport anchor: `https://developer.android.com/media/media3/exoplayer/network-stacks`
- Iroh as a public reference for QUIC + relay fallback shape:
  - `https://docs.iroh.computer/about/faq`
  - `https://www.iroh.computer/blog/iroh-0-91-0-the-last-relay-break`

## Resulting Document Set

The new architecture should be documented under `docs/connectivity/` rather than `docs/webrtc/`.

The key documents are:

- `../architecture.md`
- `../contract.md`
- `../protocol/relay.md`
- `../protocol/transport.md`
- `../protocol/pairing.md`
- `../protocol/local-broker.md`
- `../ux/android.md`
