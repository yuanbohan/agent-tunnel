---
title: step 02: App identity, account tier surface, pairing, and visibility
type: handoff
status: completed
date: 2026-04-28
step: 2
github_issue: https://github.com/yuanbohan/agent-tunnel/issues/86
related_plan: docs/plans/2026-04-28-001-feat-quic-connectivity-program-plan.md
related_guide: docs/plans/2026-04-28-002-feat-quic-connectivity-review-guide.md
---

# Step 2: App Identity, Subscription Surface, Pairing, And Visibility

## Purpose

Establish account/device trust and daemon visibility without carrying terminal
traffic.

## Major Modules

- App session device fingerprint binding
- Temporary operator-managed account tier surface
- Daemon long-term identity
- Pairing invitations and SAS confirmation
- Trusted Android device roster
- Relay-assisted pairing transport
- Live daemon visibility for paired devices
- Revoke/list paired device management

## In Scope

- Android identity is tied to account login.
- Daemon and Android can pair through Relay.
- Daemon decides trust locally.
- Relay can show paired devices which daemons are online.
- Relay operators can mark a user as `free` or `pro` until real payment integration exists.

## Out Of Scope

- Session transport
- Terminal preview
- Interactive attach
- Direct UDP
- Payment or purchase flow

## Acceptance Checklist

- [x] A test client pairs with a daemon through Relay.
- [x] Revoking a device removes visibility and active trust state.
- [x] Relay restart/daemon reconnect rebuilds live visibility from daemon-local trust.
- [x] Android can fetch `free` or `pro` policy state.
- [x] Operator can upgrade or downgrade a user's temporary account tier without a payment system.

## Implementation Summary

- App auth remains opaque bearer-token based. `POST /api/auth/login` and `POST /api/auth/refresh` now accept optional `client_fingerprint`; refresh rejects mismatches for fingerprint-bound sessions.
- Users now have `subscription_tier` (`free`/`pro`) with `GET /api/account/policy`, local-only `POST /operator/users/tier`, and `relay user tier <username> <free|pro>`.
- Daemon state now includes a separate Ed25519 connectivity identity, signed pairing invitations, persisted invitation state, pending pairing responses, and the trusted Android roster. The public CLI surface is now `tunnel pair`, `tunnel pair devices`, and `tunnel pair revoke <fingerprint>`; invitation, pending-response, and confirm operations remain daemon-local implementation details behind the one-step pairing command.
- Pairing transcript helpers sign and verify daemon invitations and Android responses and compute the documented SAS.
- Relay has live-only connectivity app/computer WebSockets at `/api/connectivity/ws` and `/connectivity/computer/ws`; visibility is derived from computer-reported trusted Android rosters, `pair_completed`, and app-session fingerprints.
- A Go-only Android pairing helper exists under `internal/connectivity/pairtest`.
- `internal/e2e/connectivity_pairing_test.go` exercises the Go Step 2 core flow against a real Relay process and PostgreSQL when local e2e is enabled.

## Verification Performed

- `go test ./cmd/relay`
- `go test ./internal/relay/auth ./internal/relay/operator ./internal/relay/handler`
- `go test ./internal/relay/store/postgres`
- `go test ./internal/connectivity/pairing ./internal/connectivity/pairtest ./internal/tunnel/daemon ./cmd/tunnel`
- `go test ./internal/protocol ./internal/relay/connectivity ./internal/relay/handler ./internal/tunnel/daemon`
- `go test ./internal/connectivity/pairing ./internal/tunnel/daemon`
- `go test ./internal/relay/connectivity ./internal/relay/handler`
- `go test ./cmd/tunnel`
- `go test ./...`
- `go test ./internal/e2e -run TestConnectivityPairingCoreFlow -count=1` (compiled; full scenario is gated by `AGENTUNNEL_RUN_LOCAL_E2E=1` and `AGENTUNNEL_TEST_DATABASE_URL`)

## Known Gaps

- Terminal QR rendering is implemented by `tunnel pair`; JSON invitation output remains available with `tunnel pair --json` for automated tests and tooling.
- Production Android code remains unimplemented and unvalidated; the current evidence is Go-only.
- Session transport, preview, interactive attach, direct UDP, fallback QUIC tunnel, and STUN remain out of scope.

## Follow-Up For Step 3 Or Step 4

- Step 3 and later flows should use the top-level `tunnel pair` surface rather than exposing daemon-local pairing internals.
- Step 4 can rely on daemon-local connectivity identity and trusted Android roster, but should not assume terminal/session transport exists on connectivity WebSockets.
