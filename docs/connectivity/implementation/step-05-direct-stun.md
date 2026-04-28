---
title: step 05: Direct UDP, STUN, and degradation
type: handoff
status: not_started
date: 2026-04-28
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

- [ ] Direct works in controlled local/cone-NAT tests.
- [ ] Blocked UDP or symmetric NAT falls back cleanly.
- [ ] Diagnostics explain whether the path is direct or relay.
- [ ] Direct-path data is available for Android path badge integration.

## Implementation Summary

Not started.

## Verification Performed

Not started.

## Known Gaps

Not started.

## Follow-Up For Step 6 And Step 7

Not started.
