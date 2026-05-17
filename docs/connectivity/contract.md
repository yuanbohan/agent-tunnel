# Connectivity Implementation Contract

## Status

This file records the local implementation boundary for `agent-tunnel`.
Cross-repository protocol details live in:

- `../agent-tunnel-protocols/docs/end-to-end-flows.md`
- `../agent-tunnel-protocols/docs/api.md`
- `../agent-tunnel-protocols/docs/architecture.md`
- `../agent-tunnel-protocols/docs/draws/README.md`
- `../agent-tunnel-protocols/docs/pairing.md`
- `../agent-tunnel-protocols/docs/relay-control-plane.md`
- `../agent-tunnel-protocols/docs/protocol.md`

When this repository disagrees with the protocol SSOT, treat the SSOT as the
protocol target and update this repository or the SSOT explicitly.

## Local Decisions

- Relay remains the account/auth, computer launch, pairing transport, presence,
  direct rendezvous, fallback setup, and opaque packet-forwarding control
  plane.
- Relay does not expose session list, preview, terminal attach, terminal
  bytes, input, resize, or transcript replay APIs for the official mobile
  companion.
- The local daemon and broker are required for daemon-owned session transport.
  `tunnel run` registers metadata, previews, snapshots, and live bytes locally
  before the mobile daemon transport can expose them.
- Direct is attempted first when available. If direct is unavailable, skipped,
  times out, or fails with a network-safe reason, the app/daemon use Relay
  fallback with a fresh QUIC/TLS transport over the binary WebSocket packet
  tunnel.
- Identity/protocol failures fail closed and must not trigger Relay fallback as
  if they were network failures.
- Free/Pro policy is a mobile product rule over active trusted-computer count:
  Free keeps one active trusted computer, Pro keeps up to ten. Daemon session
  behavior inside an active trusted computer is tier-neutral in this revision.
- Relay account policy exposes tier. Relay does not issue per-session grants
  and daemons do not receive tier updates.

## Local Implementation Map

- Pairing: `protocol/pairing.md`
- Relay control plane: `protocol/relay.md`
- Daemon transport: `protocol/transport.md`
- Local broker mechanics: `protocol/local-broker.md`
- Android UX reference: `ux/android.md`
- Subscription/product policy: `ux/subscription.md`
- Protocol provenance: `../protocols/connectivity.md`

## Change Rule

If a change affects cross-repository compatibility, update
`agent-tunnel-protocols` first or in the same PR set. This includes pairing
transcripts, realtime frame shapes, rendezvous/fallback token rules, daemon
transport frame types, payload fields, stream roles, TLS/pinning invariants,
and direct/relay data-flow boundaries.
