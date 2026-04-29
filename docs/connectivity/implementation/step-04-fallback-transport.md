---
title: step 04: Fallback-only QUIC session transport
type: handoff
status: in_progress
date: 2026-04-28
step: 4
github_issue: https://github.com/yuanbohan/agent-tunnel/issues/88
related_plan: docs/plans/2026-04-28-001-feat-quic-connectivity-program-plan.md
related_guide: docs/plans/2026-04-28-002-feat-quic-connectivity-review-guide.md
---

# Step 4: Fallback-Only QUIC Session Transport

## Purpose

Implement the full mobile session protocol over encrypted Relay fallback before
adding NAT/direct UDP complexity.

## Major Modules

- App realtime connection to Relay
- Daemon realtime connection to Relay
- Short-lived fallback tunnel token issuance
- Relay opaque packet tunnel
- Daemon QUIC session transport
- Session index delivery
- Preview subscribe/update flow
- Interactive request/grant/release flow
- Snapshot/live-byte stream flow
- Input and resize routing
- Reconnect recovery through fresh index and fresh snapshot

## In Scope

- The new app-to-daemon session protocol works over Relay fallback.
- Relay cannot parse terminal/session content.
- A simulated app can list sessions, subscribe previews, attach interactively, send input, release, and reconnect.

## Out Of Scope

- Direct UDP
- STUN
- UDP relay
- Payment enforcement beyond app-visible tier state

## Acceptance Checklist

- [ ] End-to-end fallback path works against a real daemon.
- [x] Relay forwards fallback tunnel packets opaquely and never parses terminal/session plaintext.
- [x] Reconnect gives fresh session index and preview state without missed-byte replay promises.
- [x] Android companion work has enough stable fallback control-frame and Relay tunnel contract to begin the non-snapshot subset.

## Implementation Summary

- Added production Step 4 session payload types under `internal/connectivity/sessionproto` and completed the frame registry for session index, preview, interactive, input, resize, snapshot, live bytes, path state, and recoverable errors.
- Added Relay fallback tunnel authorization to the live connectivity registry. Paired app sessions request a tunnel through the app realtime websocket; Relay issues separate short-lived, one-use app and daemon tunnel tokens; `/connectivity/tunnel/ws` redeems tokens and forwards binary websocket packets unchanged between the two actors.
- Added `internal/connectivity/carrier.WSPacketConn`, a `net.PacketConn` adapter that carries one QUIC packet per binary websocket message.
- Added daemon-side fallback QUIC control-stream handling for pinned `hello`, fresh `session_index`, broker session upserts/removals, preview subscriptions, `interactive_request` grant/deny, `interactive_release`, and input/resize routing into the local broker.
- Wired daemon realtime `relay_tunnel_ready` handling so the daemon redeems its token, creates a websocket packet carrier, starts the pinned QUIC/TLS daemon side, and serves the same `ConnectivityTransport` over Relay fallback.
- Extended the local daemon broker socket so the daemon can route `input_text`, `input_key`, and `resize` command frames back to the owning `tunnel run` process after an interactive grant.
- Added daemon-owned interactive stream creation. An accepted `interactive_request` now announces the daemon-initiated QUIC stream id and sends an initial `snapshot_begin` / `snapshot_end` boundary on that stream.
- Added a Go fallback simulator test that opens a QUIC connection over the fallback packet carrier, reads fresh session state, requests interaction, routes input to the broker owner, reconnects, and observes fresh preview state.

## Verification Performed

- `go test ./internal/connectivity/sessionproto ./internal/connectivity/frame ./internal/connectivity/interop`
- `go test ./internal/relay/connectivity ./internal/relay/handler`
- `go test ./internal/protocol ./internal/relay/...`
- `go test ./internal/connectivity/carrier ./internal/connectivity/transport`
- `go test ./internal/tunnel/daemon ./cmd/tunnel`
- `go test ./...`

## Known Gaps

- The Go simulator currently composes Relay tunnel, websocket packet carrier, and daemon QUIC transport with focused package tests rather than one process-level test against a running Relay and daemon.
- Interactive snapshot chunks and live-byte forwarding are still not implemented on the local broker bridge. Current Step 4 transport covers session index, previews, exclusive interactive grant/release state, daemon-initiated interactive stream boundaries, input, resize, and reconnect resync.
- Relay tunnel logging/metrics currently prove opacity through forwarding tests, but packet counters and operator log fields still need hardening before production operations review.

## Follow-Up For Step 5 And Step 6

- Step 4 follow-up should add a process-level fallback acceptance test against a running Relay and daemon.
- Snapshot chunk and live-byte bridge support should be completed before Android presents a full terminal attach UI over the new transport.
- Step 5 can build direct UDP/STUN path selection on top of the same session protocol and fallback tunnel contracts.
- Step 6 Android work can begin against the stable frame registry, Relay tunnel setup contract, and fallback reconnect semantics, with snapshot chunk/live-byte work called out as the remaining daemon-side dependency.
