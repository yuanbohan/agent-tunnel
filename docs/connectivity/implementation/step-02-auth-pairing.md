---
title: step 02: App identity, subscription surface, pairing, and visibility
type: handoff
status: not_started
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
- Temporary operator-managed subscription tier surface
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

- [ ] A test client pairs with a daemon through Relay.
- [ ] Revoking a device removes visibility and active trust state.
- [ ] Relay restart/daemon reconnect rebuilds live visibility from daemon-local trust.
- [ ] Android can fetch `free` or `pro` policy state.
- [ ] Operator can upgrade or downgrade a user's temporary subscription tier without a payment system.

## Implementation Summary

Not started.

## Verification Performed

Not started.

## Known Gaps

Not started.

## Follow-Up For Step 3 Or Step 4

Not started.
