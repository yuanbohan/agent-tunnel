---
title: step 01: Interop spike and connectivity primitives
type: handoff
status: active
date: 2026-04-28
step: 1
github_issue: https://github.com/yuanbohan/agent-tunnel/issues/85
related_plan: docs/plans/2026-04-28-001-feat-quic-connectivity-program-plan.md
related_guide: docs/plans/2026-04-28-002-feat-quic-connectivity-review-guide.md
---

# Step 1: Interop Spike And Connectivity Primitives

## Purpose

Prove the riskiest transport, protocol, and data-format assumptions before
production daemon, Relay, or Android product integration begins.

Step 1 deliberately uses Go on both sides. The "mobile" side is a Go simulator
that follows the Android-facing protocol shape so this repository can validate
the architecture and wire data without depending on the Android repository,
JNI packaging, emulator setup, or a physical device.

FIXME(Android): Real Android `quiche` JNI/emulator/device validation has not
been performed in this step. That remains a later implementation gate before
claiming production Android compatibility.

## Major Modules

- QUIC/TLS interop between a Go daemon side and a Go mobile-simulator side
- Device-key identity and certificate pinning
- Pairing SAS algorithm
- Length-framed control/raw-byte message codec
- WSS-carried QUIC packet experiment
- Minimal reconnect/leak stability harness

## In Scope

- Prove the daemon/mobile protocol shape with `quic-go` on both sides.
- Validate frame types, JSON control payloads, raw snapshot/live byte payloads,
  and stream ordering with a Go mobile simulator.
- Prove Relay fallback can carry opaque encrypted QUIC packets.
- Produce reusable primitives for later steps.

## Out Of Scope

- Production daemon behavior
- Production Relay routes
- Real Android app, `quiche` JNI packaging, emulator, or physical-device runs
- Pairing UX
- Session list, preview, terminal attach
- STUN/direct UDP

## Acceptance Checklist

- [x] Go daemon and Go mobile simulator complete pinned QUIC/TLS handshake.
  - Go `quic-go` pinned mutual TLS passes in `internal/connectivity/transport` and `internal/connectivity/interop`.
  - FIXME(Android): Android `quiche` JNI/device interop has not been run in this workspace and is not claimed by Step 1.
- [x] Bidirectional control stream works in the Go mobile-simulator harness.
- [x] Daemon-to-app unidirectional stream works in the Go mobile-simulator harness.
- [x] Go mobile simulator validates protocol ordering and data shapes:
  - JSON `hello`
  - JSON `session_index`
  - JSON `interactive_request`
  - JSON `interactive_granted`
  - JSON `snapshot_begin`
  - raw `snapshot_chunk`
  - JSON `snapshot_end`
  - raw `live_bytes`
- [x] Relay-like WSS carrier sees only opaque packet bytes in the Go harness.
- [x] Handoff states that Step 1 validates protocol/data primitives only and does not validate real Android capability.

## Implementation Summary

- Added `internal/connectivity/identity` for Ed25519 self-signed X.509 certificate generation, SPKI extraction, and pinned peer-certificate verification.
- Added `internal/connectivity/pairing` with the fixed 6-digit SAS algorithm from `docs/connectivity/protocol/pairing.md`, including length-prefixed canonical input helpers.
- Added `internal/connectivity/frame` with `[type][QUIC varint payload_length][payload]` encoding, stream read/write helpers, unknown-frame tolerance, and truncation/oversize failures.
- Added `internal/connectivity/transport` with pinned TLS 1.3 configs, raw Ed25519 peer-key inputs that derive SPKI pins internally, session-ticket resumption disabled, required ALPN `tunnel-conn/1`, 0-RTT rejection checks, and a `quic-go` harness covering bidirectional and daemon-initiated unidirectional streams.
- Added `internal/connectivity/carrier` with an in-memory ordered packet relay exposed as `net.PacketConn`, proving `quic-go` can run over a WebSocket-like packet carrier abstraction.
- Added `internal/connectivity/interop` as the Step 1 evidence directory. Its automated tests use a Go mobile simulator to exercise the Android-facing protocol sequence over both direct UDP and the Relay-like packet carrier.
- Added `internal/connectivity/interop/mobile.go`, which models the Step 1 mobile client logic: pinned QUIC connect, JSON control-frame exchange, `interactive_request`/`interactive_granted`, daemon-initiated UNI stream validation, raw snapshot chunks, and raw live bytes.
- Added an initial frame type registry in `internal/connectivity/frame` for the Step 1 protocol/data harness.
- Promoted `github.com/quic-go/quic-go v0.59.0` to a direct dependency because Step 1 now imports it directly.

## Verification Performed

- `go test ./internal/connectivity/...`
- `go test -race ./internal/connectivity/...`
- `go test ./...`

Covered scenarios:

- SAS golden vectors and boundary-split canonicalization.
- SPKI extraction and pin mismatch failure.
- Frame round trip, unknown frame type tolerance, truncated varint rejection, oversized declared payload rejection, incomplete payload rejection, and raw byte payload read/write.
- QUIC pinned mutual TLS handshake with ALPN `tunnel-conn/1`.
- TLS config disables session-ticket resumption so reconnects cannot skip the pin check through cached TLS state.
- ALPN mismatch rejection before application frames are processed.
- Peer SPKI mismatch rejection.
- 1 KB bidirectional stream exchange and 1 KB daemon-to-app unidirectional stream exchange.
- Go mobile simulator exchange of `hello`, `session_index`, `interactive_request`, `interactive_granted`, `snapshot_begin`, `snapshot_chunk`, `snapshot_end`, and `live_bytes`.
- Unknown JSON field tolerance in daemon-to-mobile control and interactive frames.
- 10-iteration reconnect loop without observable goroutine growth beyond the harness threshold.
- Relay-like packet carrier forwarding, close/deadline behavior, and QUIC-over-carrier application plaintext opacity check.

## Known Gaps

- FIXME(Android): Android `quiche` JNI/emulator/device interop has not been executed. Step 1 does not prove that the production Android runtime can package `quiche`, complete the pinned TLS callback path, or exchange streams on emulator/device.
- No production daemon, Relay, CLI, Android app, STUN, session list, preview, terminal attach, or pairing UX behavior was changed.
- The packet carrier is an in-memory `net.PacketConn` spike. Step 4 still needs the real Relay WebSocket endpoint and production attempt-token routing.
- The Go evidence supports continuing with `quic-go` on the daemon side, the current frame/data model, and a packet-carrier abstraction for fallback. It does not prove that Android should stay on `quiche`; run the Android validation before production Android integration claims compatibility.

## Follow-Up TODO/FIXME

- Before Android production integration or an Android compatibility claim, run the Android `quiche` JNI spike against the same protocol choices:
  - self-signed Ed25519 certificate whose SPKI is the paired device public key
  - custom SPKI pinning with standard PKIX validation bypassed
  - mutual certificates
  - ALPN `tunnel-conn/1`
  - no 0-RTT or early application bytes
  - JSON `hello`, `session_index`, `interactive_request`, and `interactive_granted`
  - daemon-to-Android `snapshot_begin`, `snapshot_chunk`, `snapshot_end`, and `live_bytes`
  - at least 1 KB daemon-to-Android unidirectional stream exchange
- Record Android build target, emulator API level, physical device/API level, `quiche` version, pass/fail details, and packaging blockers in this handoff.
- If Android `quiche` packaging or TLS pinning blocks, decide whether to switch to `kwik` before production Android transport work depends on `quiche`.
