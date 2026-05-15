---
title: step 06: Android companion integration and tier UX
type: handoff
status: not_started
date: 2026-04-28
step: 6
github_issue: https://github.com/yuanbohan/agent-tunnel/issues/90
related_plan: docs/plans/2026-04-28-001-feat-quic-connectivity-program-plan.md
related_guide: docs/plans/2026-04-28-002-feat-quic-connectivity-review-guide.md
---

# Step 6: Android Companion Integration And Tier UX

## Purpose

Implement production Android behavior against the proven protocol. Android uses Relay for auth, account policy, pairing, computer presence, rendezvous, fallback tunnel setup, and computer launch; after launch, daemon transport is the session roster/detail/interactive authority.

## Major Modules

- Android login-bound device identity
- Pairing UI and SAS confirmation
- Daemon card list
- Free active trusted computer state
- Free transactional Replace Computer
- Pro trusted-computer limit
- Pro downgrade-to-Free resolution
- Terminal view and input focus discipline
- Reconnect state rebuild
- Account switch cleanup
- Direct/relay path badge copy

## In Scope

- Production Android app behavior matches the documented UX/state machines.
- Free / Pro trusted-computer behavior is enforced in the app.
- Reconnect rebuilds state from daemon snapshots and session subscriptions.
- New Session is scoped to trusted computer sections and waits for daemon transport `session_index` or `session_upsert` after Relay returns `session_ready`.

## Out Of Scope

- Daemon-side tier enforcement
- Billing purchase flow
- New transport semantics
- Go repo implementation details
- Reintroducing Relay session list/detail/attach/monitoring/notification paths

## Acceptance Checklist

- [ ] Free auto-connects only the one active trusted computer.
- [ ] Free Replace Computer keeps old trust active until new pairing SAS succeeds, then locally removes old trust.
- [ ] Pro auto-connects online trusted computers up to ten and blocks pairing the eleventh.
- [ ] Pro downgrade to Free requires choosing one active computer before multi-computer auto-connect.
- [ ] Free and Pro session rows, preview, detail attach, reconnect, and path badge behavior are identical inside one active computer.
- [ ] Relay `session_ready.session_id` is used only to correlate launch with daemon transport session state.
- [ ] Android uses daemon transport, not Relay session endpoints, as its post-launch session authority.
- [ ] Only the focused terminal receives input.
- [ ] Account switch closes transports and clears account-derived local policy state.
- [ ] Direct/relay badge copy does not imply different encryption.

## Implementation Summary

Android implementation remains not started in this repository. Upstream Go contract evidence for the New Session handoff is in place:

- Relay `POST /api/computers/:computerID/sessions` returns `session_ready.session_id` as a control-plane launch correlation key.
- The official mobile companion waits for daemon transport `session_index` or `session_upsert` carrying that same broker-known `session_id`.
- Relay session list/detail/attach/stop APIs have been removed from the Go repo contract; Android deletion work should not preserve a hidden compatibility path.

## Verification Performed

- `go test ./internal/tunnel/daemon`
- `go test ./internal/relay/... ./internal/connectivity/...`
- Daemon transport coverage proves a broker session known before transport connect appears in initial `session_index`, and a broker session registered after transport connect emits `session_upsert`.
- Relay launch coverage remains scoped to request correlation, `accepted`, `launch_ready`, timeout, ownership, and cleanup; Relay fallback/carrier coverage keeps encrypted tunnel payloads opaque.

## Known Gaps

Android still needs to implement the bounded wait and user-visible error handling for launch success followed by delayed daemon transport convergence.

## Follow-Up For Step 7

Not started.
