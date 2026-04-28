---
title: step 06: Android companion integration and subscription UX
type: handoff
status: not_started
date: 2026-04-28
step: 6
github_issue: https://github.com/yuanbohan/agent-tunnel/issues/90
related_plan: docs/plans/2026-04-28-001-feat-quic-connectivity-program-plan.md
related_guide: docs/plans/2026-04-28-002-feat-quic-connectivity-review-guide.md
---

# Step 6: Android Companion Integration And Subscription UX

## Purpose

Implement production Android behavior against the proven protocol.

## Major Modules

- Android login-bound device identity
- Pairing UI and SAS confirmation
- Daemon card list
- Lazy daemon-card connection
- Free-tier sticky first-attach behavior
- Pro-tier preview subscriptions
- Terminal view and input focus discipline
- Reconnect state rebuild
- Account switch cleanup
- Direct/relay path badge copy

## In Scope

- Production Android app behavior matches the documented UX/state machines.
- Free/pro behavior is enforced in the app.
- Reconnect rebuilds state from daemon snapshots and subscriptions.

## Out Of Scope

- Daemon-side subscription enforcement
- Billing purchase flow
- New transport semantics
- Go repo implementation details

## Acceptance Checklist

- [ ] Free user can unlock only one session per opened daemon card.
- [ ] Pro user can see previews for all live sessions in the opened card.
- [ ] Only the focused terminal receives input.
- [ ] Account switch closes transports and clears local unlocked state.
- [ ] Direct/relay badge copy does not imply different encryption.

## Implementation Summary

Not started.

## Verification Performed

Not started.

## Known Gaps

Not started.

## Follow-Up For Step 7

Not started.
