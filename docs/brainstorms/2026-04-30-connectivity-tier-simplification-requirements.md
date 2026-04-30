---
date: 2026-04-30
topic: connectivity-tier-simplification
---

# Connectivity Tier Simplification

## Summary

Free and Pro connectivity tiers should differ only by how many trusted computers the official Android app may actively use. Once a computer is active and trusted, session rows, previews, detail attach, reconnect, and path badges behave the same on both tiers.

---

## Problem Frame

The current phase-1 connectivity subscription model puts the Free/Pro distinction inside a single computer's session list. Free users get sticky first-attach behavior, locked rows, preview restrictions, and special row UI. That creates product complexity exactly where the user expects the app to be predictable: a computer's sessions should simply be usable once the computer is trusted.

The pain is mostly carrying cost and UX inconsistency. The app has to explain why a visible session row is locked, why previews differ by tier, why the first attach is special, and why reconnect has to restore per-card unlocked state. Those rules do not improve the core product promise; they make the mobile terminal experience feel conditional inside one trusted machine.

---

## Actors

- A1. Free user: pairs and uses one computer from the official Android app.
- A2. Pro user: pairs and uses multiple trusted computers from the official Android app.
- A3. Downgraded user: moves from Pro to Free while more than one trusted computer exists.
- A4. Official Android app: owns product policy for active trusted computer selection and connection fan-out.
- A5. Relay: exposes account tier and live daemon visibility, without deriving session-level entitlement.
- A6. Daemon: owns local pairing trust and session transport, without branching on Free vs Pro session policy.

---

## Key Flows

- F1. Free user pairs their first computer
  - **Trigger:** A Free user completes SAS-confirmed pairing for a computer while no active trusted computer exists for the account on Android.
  - **Actors:** A1, A4, A5, A6
  - **Steps:** The pairing succeeds, Android records the computer as the active trusted computer, the app connects to it when it is online, and the app renders all sessions under that computer with full session capability.
  - **Outcome:** The Free user can use every session on the single active trusted computer without session-level locks or preview gating.
  - **Covered by:** R1, R2, R5, R6, R7

- F2. Free user replaces their computer
  - **Trigger:** A Free user already has one active trusted computer and starts pairing a different computer.
  - **Actors:** A1, A4, A5, A6
  - **Steps:** Android treats the pairing as a transactional replace. The old active computer remains active while the new SAS-confirmed pairing is pending. If pairing succeeds, Android locally switches active trust to the new computer and locally stops using the old one. If pairing fails, is cancelled, or has a SAS mismatch, Android keeps the old active computer unchanged.
  - **Outcome:** Free users can change computers without losing access on failed replacement attempts.
  - **Covered by:** R3, R4, R8, R13

- F3. Pro user reaches the computer limit
  - **Trigger:** A Pro user already has 10 trusted computers and attempts to pair another.
  - **Actors:** A2, A4, A5, A6
  - **Steps:** Android prevents the new computer from becoming trusted under the Pro entitlement and tells the user to remove a computer first.
  - **Outcome:** Pro remains bounded to 10 trusted computers without introducing session-level limits.
  - **Covered by:** R9, R10

- F4. Pro user downgrades to Free
  - **Trigger:** A user with multiple trusted computers moves from Pro to Free.
  - **Actors:** A3, A4, A5, A6
  - **Steps:** Android enters downgrade resolution, shows the user their trusted computers, asks them to choose one active computer to keep, and does not automatically connect to multiple computers before that choice is made.
  - **Outcome:** The account becomes Free-compliant by retaining one active trusted computer in Android product state.
  - **Covered by:** R11, R12, R14

---

## Requirements

**Tier Semantics**
- R1. Free and Pro must have identical session behavior within a single active trusted computer.
- R2. The only tier entitlement difference in this model is active trusted computer count: Free allows 1 active trusted computer; Pro allows up to 10 trusted computers.
- R3. Free must support replacing the active computer through a transactional Replace Computer flow.
- R4. Replace Computer must switch to the new computer only after successful SAS-confirmed pairing; failure, cancellation, or SAS mismatch must leave the old active computer unchanged.

**Session Experience**
- R5. Session rows under an active trusted computer must render consistently across Free and Pro.
- R6. Preview availability under an active trusted computer must be consistent across Free and Pro.
- R7. Detail attach, terminal input, reconnect, and path badge behavior under an active trusted computer must be consistent across Free and Pro.
- R8. The new model must remove Free-only sticky first-attach, locked session rows, preview gating, and free-only session-row UI.

**Computer Limits**
- R9. Pro must allow pairing and using up to 10 trusted computers.
- R10. When a Pro user already has 10 trusted computers, the app must block adding another computer until the user removes one.
- R11. When a Pro user downgrades to Free while multiple trusted computers exist, the app must enter downgrade resolution instead of automatically connecting to all online trusted computers.
- R12. Downgrade resolution must require the user to choose exactly one active trusted computer before Free automatically connects to a computer.

**Connection Policy**
- R13. Free must automatically connect to the single active trusted computer when it is online.
- R14. Pro must automatically connect to all online trusted computers, bounded by the 10-computer entitlement.
- R15. The app must not use session count, preview state, attach state, or first attached session as entitlement inputs.

**Trust Boundary**
- R16. In the first version, Free Replace Computer only changes Android-local active trust state; it does not require revoking trust from the old daemon.
- R17. The product must leave an explicit TODO for future old-daemon trust revocation after successful Replace Computer.
- R18. Daemon and Relay must not need session-level Free/Pro entitlement logic for this model.

---

## Acceptance Examples

- AE1. **Covers R1, R5, R6, R7, R8.** Given a Free user has one active trusted computer with five live sessions, when the user opens that computer in Android, all five sessions can show rows, previews, detail attach, reconnect behavior, and path badges without locked rows or sticky first-attach selection.
- AE2. **Covers R2, R9, R10.** Given a Pro user has 10 trusted computers, when they try to pair an eleventh computer, the app blocks the addition and requires removing one computer first.
- AE3. **Covers R3, R4, R13, R16.** Given a Free user has Computer A active and starts replacing it with Computer B, when Computer B pairing succeeds, Android locally makes Computer B the active trusted computer and no longer auto-connects to Computer A.
- AE4. **Covers R3, R4.** Given a Free user has Computer A active and starts replacing it with Computer B, when the new pairing is cancelled, fails, or has a SAS mismatch, Computer A remains the active trusted computer.
- AE5. **Covers R11, R12, R14.** Given a Pro user with three trusted computers is downgraded to Free, when the app next evaluates connectivity policy, it does not auto-connect to all three; it requires the user to pick one active computer first.
- AE6. **Covers R15, R18.** Given any tier, when a session is created, previewed, attached, disconnected, or reconnected under an active trusted computer, entitlement decisions do not depend on that session's identity or order.

---

## Success Criteria

- A Free user experiences a single trusted computer as fully functional: all sessions under that computer behave the same way they would for Pro.
- A Pro user can use up to 10 trusted computers without any per-session entitlement behavior.
- The Android product model becomes explainable in one sentence: Free gets one computer, Pro gets up to ten; sessions are not tier-limited inside a trusted computer.
- Downstream planning can remove session-level gating without inventing new Free/Pro behavior, downgrade behavior, or replacement semantics.

---

## Scope Boundaries

- No session count limits.
- No sticky first-attach behavior.
- No locked session rows.
- No Free-only preview suppression.
- No Free-only row styling or tap-to-unlock UI.
- No payment provider work or new paid package design.
- No daemon-side session entitlement system.
- No Relay-owned terminal/session entitlement derivation.
- No mandatory old-daemon trust revocation in the first version.
- No claim that Android-local removal physically deletes trust from an offline or unreachable old daemon.

---

## Key Decisions

- **Move entitlement from sessions to computers:** This removes the hardest-to-explain UI behavior while preserving a clear Free vs Pro business distinction.
- **Keep single-computer session behavior identical across tiers:** Once a computer is active and trusted, the app should not make the user reason about which session is allowed.
- **Use transactional Replace Computer for Free:** Failed replacement attempts should not strand a Free user with no active computer.
- **Start with Android-local old-computer removal:** This keeps the first implementation simple and matches the current trust boundary, while explicitly preserving future room for old-daemon trust revocation.
- **Require downgrade resolution before multi-computer auto-connect:** A downgraded account should not briefly behave as Pro just because it had multiple trusted computers before the tier changed.

---

## Dependencies / Assumptions

- Relay already exposes account tier as `free` or `pro`; this remains enough for Android to choose the product policy.
- Connectivity visibility is currently derived from daemon-local trusted rosters and Android app-session device identity. Android-local active computer state is therefore a product selection layer, not proof that an old daemon has deleted trust.
- Daemon remains the owner of local pairing trust and session transport, and should continue to avoid Free/Pro session branching.
- Existing connectivity docs still describe sticky first-attach and must be updated or superseded during implementation planning.

---

## Outstanding Questions

### Resolve Before Planning

(none)

### Deferred to Planning

- [Affects R10, R11, R12][Technical] Where Android should persist active trusted computer state and downgrade-resolution state.
- [Affects R3, R4, R16][Technical] How Replace Computer distinguishes a pending replacement from a normal Pro-style pairing attempt in app state.
- [Affects R17][Technical] What future old-daemon trust revocation should look like when the old computer is online, offline, or later reconnects.
- [Affects R11, R12][Design] Exact downgrade-resolution UI copy and recovery path if the user dismisses the resolution screen.

---

## Next Steps

-> /ce-plan for structured implementation planning
