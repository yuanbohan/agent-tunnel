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

Implement production Android behavior against the proven protocol.

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

## Out Of Scope

- Daemon-side tier enforcement
- Billing purchase flow
- New transport semantics
- Go repo implementation details

## Acceptance Checklist

- [ ] Free auto-connects only the one active trusted computer.
- [ ] Free Replace Computer keeps old trust active until new pairing SAS succeeds, then locally removes old trust.
- [ ] Pro auto-connects online trusted computers up to ten and blocks pairing the eleventh.
- [ ] Pro downgrade to Free requires choosing one active computer before multi-computer auto-connect.
- [ ] Free and Pro session rows, preview, detail attach, reconnect, and path badge behavior are identical inside one active computer.
- [ ] Only the focused terminal receives input.
- [ ] Account switch closes transports and clears account-derived local policy state.
- [ ] Direct/relay badge copy does not imply different encryption.

## Implementation Summary

Not started.

## Verification Performed

Not started.

## Known Gaps

Not started.

## Follow-Up For Step 7

Not started.
