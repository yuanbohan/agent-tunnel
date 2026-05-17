---
title: step 07: Hardening, operations, and documentation
type: handoff
status: not_started
date: 2026-04-28
step: 7
github_issue: https://github.com/yuanbohan/agent-tunnel/issues/91
related_plan: docs/plans/2026-04-28-001-feat-quic-connectivity-program-plan.md
related_guide: docs/plans/2026-04-28-002-feat-quic-connectivity-review-guide.md
---

# Step 7: Hardening, Operations, And Documentation

## Purpose

Prepare the new stack for production operation and keep shipped documentation
aligned with reality.

## Major Modules

- Observability and metrics
- Daemon doctor/status diagnostics
- Deployment documentation
- Manual schema operation notes
- Security and failure-mode review
- Root documentation updates
- Compatibility-line documentation

## In Scope

- Make operators able to debug pairing, Relay realtime, STUN, fallback tunnel, local broker, and path state.
- Update public docs only when behavior actually exists.

## Out Of Scope

- Adding new product behavior
- Rewriting already accepted step architecture

## Acceptance Checklist

- [ ] Diagnostics identify common failure modes.
- [ ] Docs match shipped behavior.
- [ ] Operational notes cover ports, env vars, manual SQL, rollback, and metrics.
- [ ] The new connectivity path is ready for production review.

## Implementation Summary

Not started.

## Verification Performed

Not started.

## Known Gaps

Not started.

## Follow-Up

Not started.
