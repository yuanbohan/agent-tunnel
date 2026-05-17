---
title: QUIC connectivity implementation handoffs
type: handoff-index
status: active
date: 2026-04-28
related_plan: docs/plans/2026-04-28-001-feat-quic-connectivity-program-plan.md
related_guide: docs/plans/2026-04-28-002-feat-quic-connectivity-review-guide.md
issue_map: docs/plans/2026-04-28-003-feat-quic-connectivity-github-issues.md
---

# QUIC connectivity implementation handoffs

This directory records step-by-step delivery state for the QUIC connectivity
program. Each step should update its own handoff before the PR is reviewed.

The handoff files are intentionally separate from the program plan:

- The plan explains how the program is split.
- The review guide explains the split at a high level.
- These handoffs record what actually changed, what was verified, and what the
  next step should know.

## Step Files

1. `step-01-interop-spike.md`
2. `step-02-auth-pairing.md`
3. `step-03-local-broker.md`
4. `step-04-fallback-transport.md`
5. `step-05-direct-stun.md`
6. `step-06-android-companion.md`
7. `step-07-hardening-operations.md`

## GitHub Issues

- Umbrella: https://github.com/yuanbohan/agent-tunnel/issues/84
- Step 1: https://github.com/yuanbohan/agent-tunnel/issues/85
- Step 2: https://github.com/yuanbohan/agent-tunnel/issues/86
- Step 3: https://github.com/yuanbohan/agent-tunnel/issues/87
- Step 4: https://github.com/yuanbohan/agent-tunnel/issues/88
- Step 5: https://github.com/yuanbohan/agent-tunnel/issues/89
- Step 6: https://github.com/yuanbohan/agent-tunnel/issues/90
- Step 7: https://github.com/yuanbohan/agent-tunnel/issues/91

## Update Rule

Before a step PR is ready for review, update that step's handoff with:

- what was implemented
- what was deliberately left out
- verification performed
- known gaps or risks
- exact follow-up notes for the next step

Do not use these files to start the next step early. They are handoff records,
not permission to mix scopes.
