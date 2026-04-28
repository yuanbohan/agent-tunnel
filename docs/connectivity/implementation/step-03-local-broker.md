---
title: step 03: Daemon local broker and tunnel run registration
type: handoff
status: not_started
date: 2026-04-28
step: 3
github_issue: https://github.com/yuanbohan/agent-tunnel/issues/87
related_plan: docs/plans/2026-04-28-001-feat-quic-connectivity-program-plan.md
related_guide: docs/plans/2026-04-28-002-feat-quic-connectivity-review-guide.md
---

# Step 3: Daemon Local Broker And `tunnel run` Registration

## Purpose

Make the daemon aware of local sessions while keeping `tunnel run` as the
terminal owner.

## Major Modules

- Daemon connectivity core lifecycle
- `tunnel run` auto-start or connect-to-daemon flow
- Long-lived local session registration socket
- Local session roster
- Latest preview cache
- Tmux launch-health separation

## In Scope

- `tunnel run` registers itself with the daemon.
- Daemon knows which local sessions exist.
- Daemon has a latest preview per session.
- Missing tmux degrades remote-launch health without blocking local broker registration.

## Out Of Scope

- Mobile transport
- Pairing UI
- Direct UDP

## Acceptance Checklist

- [ ] Local `tunnel run` sessions appear in daemon-local roster.
- [ ] Session disappears when the local connection closes.
- [ ] Preview is generated locally, not by Relay.
- [ ] Current `tunnel run` startup and local terminal behavior are unchanged.

## Implementation Summary

Not started.

## Verification Performed

Not started.

## Known Gaps

Not started.

## Follow-Up For Step 4

Not started.
