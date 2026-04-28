---
title: feat: QUIC connectivity program review guide
type: feat
status: active
date: 2026-04-28
related_plan: docs/plans/2026-04-28-001-feat-quic-connectivity-program-plan.md
---

# feat: QUIC connectivity program review guide

This is the high-level review version of the QUIC connectivity program plan.
It intentionally omits concrete code paths and low-level test details. Use this
document to review whether the big feature split is sensible before creating
step issues and implementation branches.

## Target Shape

The end state is:

- Android connects to a trusted computer over QUIC/TLS.
- Direct UDP is preferred when possible.
- Relay fallback remains available, but Relay forwards only encrypted QUIC packets.
- Relay owns account auth, daemon presence, pairing transport, rendezvous, subscription tier, and fallback setup.
- The local `tunnel run` process remains the PTY owner and source of truth for terminal state.
- The daemon becomes the local mobile-facing gateway for sessions on that computer.
- Free/pro behavior is enforced only in the official app in this phase.
- Subscription status is temporarily managed by Relay operator actions. Real payment integration is a future replacement, not a dependency for this program.
- Legacy Relay attach compatibility is not a planning dimension for this review guide. The program focuses on building the new connectivity stack.

## Review Questions

Use these questions for high-level review:

- Can Step 1 fail fast before the team invests in Relay, daemon, and Android product work?
- Can each step merge without requiring the next step to be ready?
- Does each step have an independently testable outcome?
- Are auth, pairing, local session discovery, fallback transport, direct transport, Android UX, and hardening separated cleanly?
- Are the GitHub issues scoped so one PR can reasonably close one issue?

## Recommended GitHub Structure

Create:

- One umbrella issue: "Implement QUIC session connectivity program"
- Seven child issues, one per step below
- One branch and PR per child issue

Each child issue should include:

- Goal
- In scope
- Out of scope
- Major modules
- Acceptance checklist
- Handoff document path
- Depends on
- Enables

Each PR should:

- Link to the child issue it closes
- Link back to the umbrella issue
- Update that step's handoff document
- Avoid starting the next step's scope unless explicitly agreed during review

## Step 1: Interop Spike And Connectivity Primitives

**Purpose:** Prove the riskiest transport and security assumptions before production integration.

**Major modules:**

- QUIC/TLS interop between Go daemon side and Android side
- Device-key identity and certificate pinning
- Pairing SAS algorithm
- Length-framed control/raw-byte message codec
- WSS-carried QUIC packet experiment
- Minimal reconnect/leak stability harness

**In scope:**

- Prove `quic-go` and Android `quiche` are viable together.
- Prove Relay fallback can carry opaque encrypted QUIC packets.
- Produce reusable primitives for later steps.

**Out of scope:**

- Production daemon behavior
- Production Relay routes
- Pairing UX
- Session list, preview, terminal attach
- STUN/direct UDP

**Independent acceptance:**

- Go and Android can complete pinned QUIC/TLS handshake.
- Bidirectional control and daemon-to-app unidirectional streams work.
- Relay-like WSS carrier sees only opaque packet bytes.
- The step handoff clearly says whether the program can proceed or must change transport library strategy.

**Why this split is important:** If this fails, the program should stop or switch libraries before touching auth, daemon, Relay, or Android product code.

## Step 2: App Identity, Subscription Surface, Pairing, And Visibility

**Purpose:** Establish account/device trust and daemon visibility without carrying terminal traffic yet.

**Major modules:**

- App session device fingerprint binding
- Temporary operator-managed subscription tier surface
- Daemon long-term identity
- Pairing invitations and SAS confirmation
- Trusted Android device roster
- Relay-assisted pairing transport
- Live daemon visibility for paired devices
- Revoke/list paired device management

**In scope:**

- Android identity is tied to account login.
- Daemon and Android can pair through Relay.
- Daemon decides trust locally.
- Relay can show paired devices which daemons are online.
- Relay operators can mark a user as `free` or `pro` until real payment integration exists.

**Out of scope:**

- Session transport
- Terminal preview
- Interactive attach
- Direct UDP
- Payment or purchase flow; this is explicitly a future FIXME, not a dependency

**Independent acceptance:**

- A test client can pair with a daemon through Relay.
- Revoking a device removes visibility and closes active trust state.
- Relay restart/daemon reconnect can rebuild live visibility from daemon-local trust.
- Android can fetch `free` or `pro` policy state.
- An operator can upgrade or downgrade a user's temporary subscription tier without a payment system.

**Why this split is important:** Pairing and account identity are prerequisites for every later transport, but they do not need session bytes to be implemented.

## Step 3: Daemon Local Broker And `tunnel run` Registration

**Purpose:** Make the daemon aware of local sessions while keeping `tunnel run` as the terminal owner.

**Major modules:**

- Daemon connectivity core lifecycle
- `tunnel run` auto-start or connect-to-daemon flow
- Long-lived local session registration socket
- Local session roster
- Latest preview cache
- Tmux launch-health separation

**In scope:**

- `tunnel run` registers itself with the daemon.
- Daemon knows which local sessions exist.
- Daemon has a latest preview per session.
- Missing tmux degrades remote-launch health without blocking local broker registration.

**Out of scope:**

- Mobile transport
- Pairing UI
- Direct UDP

**Independent acceptance:**

- Local `tunnel run` sessions appear in daemon-local roster.
- Session disappears when the local connection closes.
- Preview is generated locally, not by Relay.

**Why this split is important:** It isolates the local computer architecture change before remote mobile traffic is introduced.

## Step 4: Fallback-Only QUIC Session Transport

**Purpose:** Implement the full mobile session protocol over encrypted Relay fallback before dealing with NAT/direct UDP variability.

**Major modules:**

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

**In scope:**

- The new app-to-daemon session protocol works over Relay fallback.
- Relay cannot parse terminal/session content.
- A simulated app can list sessions, subscribe previews, attach interactively, send input, release, and reconnect.

**Out of scope:**

- Direct UDP
- STUN
- UDP relay
- Payment enforcement beyond app-visible tier state

**Independent acceptance:**

- End-to-end fallback path works against a real daemon.
- Relay logs/metrics show tunnel setup and packet counts, not terminal plaintext.
- Reconnect gives fresh state without missed-byte replay promises.

**Why this split is important:** Fallback gives a deterministic transport path that can be tested before adding direct networking complexity.

## Step 5: Direct UDP, STUN, And Degradation

**Purpose:** Add direct-first connection attempts, self-hosted STUN, and automatic fallback behavior.

**Major modules:**

- STUN Binding service
- Candidate discovery and filtering
- Rendezvous hint exchange
- Direct QUIC attempt manager
- Direct deadline and fallback transition
- Path diagnostics and metrics
- Direct-vs-relay path badge data

**In scope:**

- Try direct UDP first when possible.
- Fall back automatically to Step 4 Relay tunnel when direct fails.
- Measure direct success, fallback reasons, and latency.

**Out of scope:**

- UDP relay
- Manual user path selection
- Different encryption model for fallback

**Independent acceptance:**

- Direct works in controlled local/cone-NAT tests.
- Blocked UDP or symmetric NAT falls back cleanly.
- Diagnostics can explain whether the path is direct or relay.

**Why this split is important:** Direct connectivity depends on network conditions and deployment. It should build on an already-working fallback path.

## Step 6: Android Companion Integration And Subscription UX

**Purpose:** Implement production Android behavior against the proven protocol.

**Major modules:**

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

**In scope:**

- Production Android app behavior matches the documented UX/state machines.
- Free/pro behavior is enforced in the app.
- Reconnect rebuilds state from daemon snapshots and subscriptions.

**Out of scope:**

- Daemon-side subscription enforcement
- Billing purchase flow
- New transport semantics
- Go repo implementation details

**Independent acceptance:**

- Free user can unlock only one session per opened daemon card.
- Pro user can see previews for all live sessions in the opened card.
- Only the focused terminal receives input.
- Account switch closes transports and clears local unlocked state.

**Why this split is important:** Android work can be reviewed as product behavior once fallback/direct protocol foundations exist.

## Step 7: Hardening, Operations, And Documentation

**Purpose:** Prepare the new stack for production operation and keep shipped documentation aligned with reality.

**Major modules:**

- Observability and metrics
- Daemon doctor/status diagnostics
- Deployment documentation
- Manual schema operation notes
- Security and failure-mode review
- Root documentation updates
- Compatibility-line documentation

**In scope:**

- Make operators able to debug pairing, Relay realtime, STUN, fallback tunnel, local broker, and path state.
- Update public docs only when behavior actually exists.

**Out of scope:**

- Adding new product behavior
- Rewriting already accepted step architecture

**Independent acceptance:**

- Diagnostics identify common failure modes.
- Docs match shipped behavior.

**Why this split is important:** Hardening should happen after behavior exists, but before the feature is treated as a broad production surface.

## Dependency Summary

Strict dependencies:

- Step 1 must happen first.
- Step 2 depends on Step 1.
- Step 4 depends on Steps 1, 2, and 3.
- Step 5 depends on Step 4.
- Step 7 depends on enough user-visible behavior from Steps 4-6.

Parallelizable after Step 1:

- Step 2 and Step 3 can mostly proceed independently.
- Step 6 Android planning can begin after Step 4 protocol contracts stabilize, while direct-path UI details wait for Step 5.

Do not parallelize:

- Step 4 before Step 2 pairing/visibility is stable.
- Step 5 before Step 4 fallback is stable.

## Suggested Issue Tree

Umbrella:

- Implement QUIC session connectivity program

Child issues:

1. Prove QUIC/TLS interop and connectivity primitives
2. Add app identity, subscription tier, pairing, and daemon visibility
3. Add daemon local broker and `tunnel run` registration
4. Add fallback-only QUIC session transport over Relay tunnel
5. Add direct UDP, self-hosted STUN, and automatic fallback
6. Integrate Android companion UX and subscription behavior
7. Harden operations and update docs

## Review Recommendation

For this round, review only:

- Whether these seven steps are the right boundaries.
- Whether Step 1 is the correct first branch.
- Whether Step 2 and Step 3 are independent enough after Step 1.
- Whether fallback-before-direct is the right sequencing.
- Whether Android should be Step 6 or partially overlap earlier.
- Whether one issue per step is the right GitHub management unit.

Leave code-level package/file review for the implementation PRs.
