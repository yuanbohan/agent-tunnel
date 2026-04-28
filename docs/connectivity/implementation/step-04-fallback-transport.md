---
title: step 04: Fallback-only QUIC session transport
type: handoff
status: not_started
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
- [ ] Relay logs/metrics show tunnel setup and packet counts, not terminal plaintext.
- [ ] Reconnect gives fresh state without missed-byte replay promises.
- [ ] Android companion work has enough stable protocol contract to begin.

## Implementation Summary

Not started.

## Verification Performed

Not started.

## Known Gaps

Not started.

## Follow-Up For Step 5 And Step 6

Not started.
