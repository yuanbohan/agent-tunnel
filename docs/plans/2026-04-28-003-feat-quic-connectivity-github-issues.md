---
title: feat: QUIC connectivity GitHub issue map
type: feat
status: active
date: 2026-04-28
repository: yuanbohan/agent-tunnel
related_plan: docs/plans/2026-04-28-001-feat-quic-connectivity-program-plan.md
related_guide: docs/plans/2026-04-28-002-feat-quic-connectivity-review-guide.md
---

# feat: QUIC connectivity GitHub issue map

This document maps the QUIC connectivity program to GitHub issues and serves as
the pre-coding coordination record.

## Issue Structure

Umbrella issue:

- [#84](https://github.com/yuanbohan/agent-tunnel/issues/84): `feat: implement QUIC session connectivity program`

Child issues:

1. [#85](https://github.com/yuanbohan/agent-tunnel/issues/85): `feat(connectivity): prove QUIC/TLS interop and primitives`
2. [#86](https://github.com/yuanbohan/agent-tunnel/issues/86): `feat(connectivity): add app identity, account tier, pairing, and visibility`
3. [#87](https://github.com/yuanbohan/agent-tunnel/issues/87): `feat(connectivity): add daemon local broker and tunnel run registration`
4. [#88](https://github.com/yuanbohan/agent-tunnel/issues/88): `feat(connectivity): add fallback-only QUIC session transport`
5. [#89](https://github.com/yuanbohan/agent-tunnel/issues/89): `feat(connectivity): add direct UDP, self-hosted STUN, and fallback degradation`
6. [#90](https://github.com/yuanbohan/agent-tunnel/issues/90): `feat(connectivity): integrate Android companion UX and trusted-computer tier behavior`
7. [#91](https://github.com/yuanbohan/agent-tunnel/issues/91): `feat(connectivity): harden operations and update shipped docs`

## PR Rule

Each implementation PR should close exactly one child issue unless review
explicitly approves a different split.

Each PR should include:

- issue link
- handoff document link
- what changed
- what stayed out of scope
- verification
- next-step notes

## Umbrella Issue Body

```markdown
## Goal

Track the full QUIC session connectivity program across independently reviewed
steps.

The target architecture is daemon-mediated Android-to-computer connectivity:
direct QUIC/TLS where possible, encrypted Relay fallback when direct is not
available, and `tunnel run` remaining the PTY/session source of truth.

## Program Documents

- Detailed plan: `docs/plans/2026-04-28-001-feat-quic-connectivity-program-plan.md`
- High-level review guide: `docs/plans/2026-04-28-002-feat-quic-connectivity-review-guide.md`
- Handoff index: `docs/connectivity/implementation/README.md`

## Child Issues

- [ ] #85 Step 1: prove QUIC/TLS interop and primitives
- [ ] #86 Step 2: add app identity, account tier, pairing, and visibility
- [ ] #87 Step 3: add daemon local broker and `tunnel run` registration
- [ ] #88 Step 4: add fallback-only QUIC session transport
- [ ] #89 Step 5: add direct UDP, self-hosted STUN, and fallback degradation
- [ ] #90 Step 6: integrate Android companion UX and trusted-computer tier behavior
- [ ] #91 Step 7: harden operations and update shipped docs

## Scope Notes

- Do not use legacy Relay attach compatibility or retirement as a planning dimension for this program.
- Subscription is temporarily operator-managed through Relay; real payment integration is future work.
- Do not start Go implementation work from this umbrella issue. Work should happen through child issues.
```

## Child Issue Bodies

### Step 1

```markdown
## Goal

Prove the riskiest transport, protocol, and data-format assumptions before
production daemon, Relay, or Android product integration begins.

Step 1 uses a Go mobile simulator rather than a production Android client. It
validates the architecture, frame types, JSON control payloads, raw snapshot/live
payloads, stream ordering, and packet-carrier behavior without requiring the
Android repository, `quiche` JNI packaging, an emulator, or a physical device.

FIXME(Android): Real Android `quiche` JNI/emulator/device validation remains a
follow-up before claiming production Android compatibility.

## Major Modules

- QUIC/TLS interop between a Go daemon side and a Go mobile-simulator side
- Device-key identity and certificate pinning
- Pairing SAS algorithm
- Length-framed control/raw-byte message codec
- WSS-carried QUIC packet experiment
- Minimal reconnect/leak stability harness

## In Scope

- Prove the daemon/mobile protocol shape with `quic-go` on both sides.
- Validate the Android-facing data format with a Go mobile simulator.
- Prove Relay fallback can carry opaque encrypted QUIC packets.
- Produce reusable primitives for later steps.

## Out Of Scope

- Production daemon behavior
- Production Relay routes
- Real Android app, `quiche` JNI packaging, emulator, or physical-device runs
- Pairing UX
- Session list, preview, terminal attach
- STUN/direct UDP

## Acceptance Checklist

- [ ] Go daemon and Go mobile simulator complete pinned QUIC/TLS handshake.
- [ ] Bidirectional control stream works in the Go mobile-simulator harness.
- [ ] Daemon-to-app unidirectional stream works in the Go mobile-simulator harness.
- [ ] Go mobile simulator validates `hello`, `session_index`, `interactive_request`, `interactive_granted`, `snapshot_begin`, `snapshot_chunk`, `snapshot_end`, and `live_bytes`.
- [ ] Relay-like WSS carrier sees only opaque packet bytes.
- [ ] Handoff states Step 1 validates protocol/data primitives only and leaves real Android validation as TODO/FIXME.

## Handoff

Update `docs/connectivity/implementation/step-01-interop-spike.md` before PR review.

## Depends On

None.

## GitHub

- Umbrella: #84
- Issue: #85
```

### Step 2

```markdown
## Goal

Establish account/device trust and daemon visibility without carrying terminal
traffic.

## Major Modules

- App session device fingerprint binding
- Temporary operator-managed account tier surface
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
- [ ] Operator can upgrade or downgrade a user's temporary account tier without a payment system.

## Handoff

Update `docs/connectivity/implementation/step-02-auth-pairing.md` before PR review.

## Depends On

Step 1 (#85).

## GitHub

- Umbrella: #84
- Issue: #86
```

### Step 3

```markdown
## Goal

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

## Handoff

Update `docs/connectivity/implementation/step-03-local-broker.md` before PR review.

## Depends On

Step 1 (#85).

## GitHub

- Umbrella: #84
- Issue: #87
```

### Step 4

```markdown
## Goal

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

## Handoff

Update `docs/connectivity/implementation/step-04-fallback-transport.md` before PR review.

## Depends On

Steps 1, 2, and 3 (#85, #86, #87).

## GitHub

- Umbrella: #84
- Issue: #88
```

### Step 5

```markdown
## Goal

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

## Handoff

Update `docs/connectivity/implementation/step-05-direct-stun.md` before PR review.

## Depends On

Step 4 (#88).

## GitHub

- Umbrella: #84
- Issue: #89
```

### Step 6

```markdown
## Goal

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
- Reconnect rebuilds state from daemon snapshots and subscriptions.

## Out Of Scope

- Daemon-side tier enforcement
- Billing purchase flow
- New transport semantics
- Go repo implementation details

## Acceptance Checklist

- [ ] Free auto-connects only the one active trusted computer.
- [ ] Free Replace Computer keeps old trust active until new pairing SAS succeeds.
- [ ] Pro auto-connects online trusted computers up to ten and blocks pairing the eleventh.
- [ ] Pro downgrade to Free requires choosing one active computer.
- [ ] Free and Pro session rows, preview, detail attach, reconnect, and path badge behavior are identical inside one active computer.
- [ ] Only the focused terminal receives input.
- [ ] Account switch closes transports and clears account-derived local policy state.
- [ ] Direct/relay badge copy does not imply different encryption.

## Handoff

Update `docs/connectivity/implementation/step-06-android-companion.md` before PR review.

## Depends On

Step 4 (#88) for fallback behavior. Step 5 (#89) for direct/relay path badge behavior.

## GitHub

- Umbrella: #84
- Issue: #90
```

### Step 7

```markdown
## Goal

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

## Handoff

Update `docs/connectivity/implementation/step-07-hardening-operations.md` before PR review.

## Depends On

Enough user-visible behavior from Steps 4-6 (#88, #89, #90) to harden and document the shipped stack.

## GitHub

- Umbrella: #84
- Issue: #91
```
