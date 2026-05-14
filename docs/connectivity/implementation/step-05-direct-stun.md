---
title: step 05: Direct UDP, STUN, and degradation
type: handoff
status: completed
date: 2026-04-29
step: 5
github_issue: https://github.com/yuanbohan/agent-tunnel/issues/89
related_plan: docs/plans/2026-04-28-001-feat-quic-connectivity-program-plan.md
related_guide: docs/plans/2026-04-28-002-feat-quic-connectivity-review-guide.md
---

# Step 5: Direct UDP, STUN, And Degradation

## Purpose

Add direct-first connection attempts, self-hosted STUN, and automatic fallback
behavior.

## Major Modules

- STUN Binding service
- Candidate discovery and filtering
- Rendezvous hint exchange
- Direct QUIC attempt manager
- Direct deadline and fallback transition
- Path diagnostics and metrics
- Direct-vs-relay path badge data

## In Scope

- Try direct UDP first when possible.
- Fall back automatically to Step 4 Relay tunnel when direct fails.
- Measure direct success, fallback reasons, and latency.

## Out Of Scope

- UDP relay
- Manual user path selection
- Different encryption model for fallback

## Acceptance Checklist

- [x] Direct works in controlled local Go-simulator tests.
- [x] Direct timeout falls back cleanly to the Step 4 Relay tunnel.
- [x] Diagnostics explain whether the path is direct or relay.
- [x] Direct-path data is available for Android path badge integration.
- [ ] Production Android direct UDP is validated in the Android companion repo.
- [ ] Cone-NAT measurement panel is run against deployed edge infrastructure.

## Implementation Summary

- Relay can start a Binding-only STUN UDP listener from `relay serve` when
  `RELAY_STUN_LISTEN_ADDR` is enabled for local/manual deployments. Production
  Docker Compose runs STUN separately with `relay stun serve`, publishes UDP
  `3478` directly on the VPS, and documents the `stun.agentunnel.cn`
  firewall/DNS expectation. Nginx remains HTTP/WebSocket only and does not
  proxy STUN.
- `internal/connectivity/direct` provides reusable UDP sockets, same-socket
  STUN discovery, candidate normalization/filtering, UDP probes, and
  direct-first attempt orchestration with relay fallback reasons.
- Relay realtime now supports live-only `rendezvous_open`,
  `rendezvous_hint`, and `rendezvous_close` between exactly the paired
  app/daemon peers. Rendezvous state expires, is superseded by newer attempts,
  and is removed on disconnect/revocation.
- The Go mobile simulator can attempt direct QUIC first and fall back to the
  existing opaque Relay tunnel. Both paths still terminate in the same pinned
  QUIC/TLS session protocol.
- The daemon handles app rendezvous hints by opening a direct UDP QUIC listener,
  sending daemon candidates, probing app candidates, and serving the existing
  `ConnectivityTransport` with `path_kind=direct` on success.
- `path_state` now carries `attempt_id`, `path_kind`, fallback reason, and
  coarse setup latency fields for Android badge/diagnostic integration.

## Verification Performed

- `go test ./internal/connectivity/stun ./cmd/relay`
- `go test ./internal/connectivity/direct ./internal/connectivity/stun`
- `go test ./internal/protocol ./internal/relay/connectivity ./internal/relay/handler`
- `go test ./internal/connectivity/direct ./internal/connectivity/interop ./internal/tunnel/daemon`
- `go test ./internal/connectivity/sessionproto ./internal/connectivity/interop ./internal/tunnel/daemon`
- `go test ./cmd/stuncheck ./internal/connectivity/direct ./internal/connectivity/stun`

## Known Gaps

- Production Android direct UDP and `quiche` integration remain Step 6.
- Production cone-NAT and symmetric-NAT measurement has not been run; this is
  left for deployed edge operations after `relay-cn-status` verifies the public
  `stun.agentunnel.cn:3478` Binding path against real networks.
- The daemon direct hint currently uses local UDP candidates from its direct
  socket; production deployments should prefer STUN-observed public candidates
  once Step 6/operations supplies the app-side STUN endpoint configuration.
- UDP relay remains out of scope; fallback is still WSS-tunneled QUIC.

## Follow-Up For Step 6 And Step 7

- Step 6 Android should implement the documented rendezvous frames, same-socket
  STUN discovery, direct-first deadline, fallback request, and Direct/Relay
  badge using `hello.path_kind` plus `path_state`.
- Step 6 should validate pinned TLS and frame exchange through the Android
  QUIC client on emulator and physical devices.
- Step 7 operations should collect attempt-level direct success, fallback
  reason, and setup latency from structured logs/status and run the cone-NAT
  measurement panel before changing fallback strategy.
