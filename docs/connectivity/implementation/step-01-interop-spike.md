---
title: step 01: Interop spike and connectivity primitives
type: handoff
status: not_started
date: 2026-04-28
step: 1
github_issue: https://github.com/yuanbohan/agent-tunnel/issues/85
related_plan: docs/plans/2026-04-28-001-feat-quic-connectivity-program-plan.md
related_guide: docs/plans/2026-04-28-002-feat-quic-connectivity-review-guide.md
---

# Step 1: Interop Spike And Connectivity Primitives

## Purpose

Prove the riskiest transport and security assumptions before production daemon,
Relay, or Android product integration begins.

## Major Modules

- QUIC/TLS interop between Go daemon side and Android side
- Device-key identity and certificate pinning
- Pairing SAS algorithm
- Length-framed control/raw-byte message codec
- WSS-carried QUIC packet experiment
- Minimal reconnect/leak stability harness

## In Scope

- Prove `quic-go` and Android `quiche` are viable together.
- Prove Relay fallback can carry opaque encrypted QUIC packets.
- Produce reusable primitives for later steps.

## Out Of Scope

- Production daemon behavior
- Production Relay routes
- Pairing UX
- Session list, preview, terminal attach
- STUN/direct UDP

## Acceptance Checklist

- [ ] Go and Android complete pinned QUIC/TLS handshake.
- [ ] Bidirectional control stream works.
- [ ] Daemon-to-app unidirectional stream works.
- [ ] Relay-like WSS carrier sees only opaque packet bytes.
- [ ] Handoff states whether the program can proceed or must change transport library strategy.

## Implementation Summary

Not started.

## Verification Performed

Not started.

## Known Gaps

Not started.

## Follow-Up For Step 2

Not started.
