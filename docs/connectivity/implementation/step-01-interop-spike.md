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

Prove the riskiest transport and security assumptions before production daemon,
Relay, or Android product integration begins.

## Major Modules

- QUIC/TLS interop between Go daemon side and Android side
- Device-key identity and certificate pinning
- Pairing SAS algorithm
- Length-framed control/raw-byte message codec
- WSS-carried QUIC packet experiment
- Minimal reconnect/leak stability harness

## In Scope

- Prove `quic-go` and Android `quiche` are viable together.
- Prove Relay fallback can carry opaque encrypted QUIC packets.
- Produce reusable primitives for later steps.

## Out Of Scope

- Production daemon behavior
- Production Relay routes
- Pairing UX
- Session list, preview, terminal attach
- STUN/direct UDP

## Acceptance Checklist

- [ ] Go and Android complete pinned QUIC/TLS handshake.
  - Go `quic-go` pinned mutual TLS passes in `internal/connectivity/transport` and `internal/connectivity/interop`.
  - Android `quiche` JNI/device interop has not been run in this workspace.
- [x] Bidirectional control stream works in the Go harness.
- [x] Daemon-to-app unidirectional stream works in the Go harness.
- [x] Relay-like WSS carrier sees only opaque packet bytes in the Go harness.
- [x] Handoff states whether the program can proceed or must change transport library strategy.

## Implementation Summary

- Added `internal/connectivity/identity` for Ed25519 self-signed X.509 certificate generation, SPKI extraction, and pinned peer-certificate verification.
- Added `internal/connectivity/pairing` with the fixed 6-digit SAS algorithm from `docs/connectivity/protocol/pairing.md`, including length-prefixed canonical input helpers.
- Added `internal/connectivity/frame` with `[type][QUIC varint payload_length][payload]` encoding, stream read/write helpers, unknown-frame tolerance, and truncation/oversize failures.
- Added `internal/connectivity/transport` with pinned TLS 1.3 configs, raw Ed25519 peer-key inputs that derive SPKI pins internally, session-ticket resumption disabled, required ALPN `tunnel-conn/1`, 0-RTT rejection checks, and a `quic-go` harness covering bidirectional and daemon-initiated unidirectional streams.
- Added `internal/connectivity/carrier` with an in-memory ordered packet relay exposed as `net.PacketConn`, proving `quic-go` can run over a WebSocket-like packet carrier abstraction.
- Added `internal/connectivity/interop` as the Step 1 evidence directory. Its automated test is a Go pinned-QUIC interop harness; the README records the Android `quiche` manual gate that must be filled before Step 2.
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
- 10-iteration reconnect loop without observable goroutine growth beyond the harness threshold.
- Relay-like packet carrier forwarding, close/deadline behavior, and QUIC-over-carrier application plaintext opacity check.

## Known Gaps

- Android `quiche` JNI/emulator/device interop has not been executed. This is the remaining hard gate for Step 1.
- No production daemon, Relay, CLI, Android app, STUN, session list, preview, terminal attach, or pairing UX behavior was changed.
- The packet carrier is an in-memory `net.PacketConn` spike. Step 4 still needs the real Relay WebSocket endpoint and production attempt-token routing.
- The Go evidence supports continuing with `quic-go` on the daemon side and a packet-carrier abstraction for fallback. It does not yet prove that Android should stay on `quiche`; do not start Step 2 as fully unblocked until Android `quiche` completes pinned TLS and stream exchange.

## Follow-Up For Step 2

- Before Step 2 starts, run the Android `quiche` JNI spike against the same protocol choices:
  - self-signed Ed25519 certificate whose SPKI is the paired device public key
  - custom SPKI pinning with standard PKIX validation bypassed
  - mutual certificates
  - ALPN `tunnel-conn/1`
  - no 0-RTT or early application bytes
  - at least 1 KB bidirectional control stream exchange
  - at least 1 KB daemon-to-Android unidirectional stream exchange
- Record Android build target, emulator API level, physical device/API level, `quiche` version, pass/fail details, and packaging blockers in this handoff.
- If Android `quiche` packaging or TLS pinning blocks, decide whether to switch to `kwik` before Relay auth, pairing, daemon broker, or fallback transport work begins.
