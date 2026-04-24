---
date: 2026-04-23
topic: direct-attach-control-plane
---

# Direct Attach Control Plane

## Problem Frame

Tunnel's current remote attach model treats the Relay as both control plane and data plane: mobile clients discover sessions through the Relay, attach through Relay-scoped WebSockets, and send/receive terminal traffic through the Relay for the entire lifetime of the session.

That shape is acceptable at small scale, but it binds Relay connection count and Relay byte throughput directly to attach activity. It also makes the current Relay-specific attach transport look more permanent than it should be. The desired long-term model is different: mobile should attach directly to the computer when possible, terminal traffic should remain end-to-end encrypted, Relay should coordinate but not terminate that traffic, and Relay-backed forwarding should remain available only as an automatic fallback when direct connectivity fails.

---

## Actors

- A1. Mobile client: discovers sessions, establishes an attach path, renders terminal state, and sends remote input.
- A2. Tunnel session owner on the computer: owns the PTY, terminal mirror, and attachable session state.
- A3. Relay server: authenticates, exposes discovery and control APIs, coordinates connection setup, and blind-forwards encrypted traffic only when fallback is required.

---

## Key Flows

- F1. Direct attach succeeds
  - **Trigger:** A mobile client selects a live session.
  - **Actors:** A1, A2, A3
  - **Steps:** The mobile client discovers the session through Relay, uses Relay-mediated control-plane coordination to establish a direct path to the computer, proves possession of the out-of-band shared trust material, receives the session snapshot and subsequent live bytes over the direct path, and sends input back over that same direct path.
  - **Outcome:** Terminal traffic bypasses Relay entirely while preserving the current attach semantics from the user's perspective.
  - **Covered by:** R1, R2, R4, R5, R8, R9, R12

- F2. Direct attach fails and fallback takes over
  - **Trigger:** A mobile client cannot establish or maintain the direct path within the product's attach budget.
  - **Actors:** A1, A2, A3
  - **Steps:** The client and computer attempt direct connectivity through Relay-mediated control-plane coordination, determine that direct attach is unavailable, switch automatically to Relay-backed forwarding, and continue the attach without exposing plaintext terminal traffic to Relay.
  - **Outcome:** The session remains attachable without changing the user's security expectations.
  - **Covered by:** R3, R6, R7, R10, R11, R13

- F3. Relay-only control-plane operation
  - **Trigger:** A session is online regardless of whether any mobile client is currently attached.
  - **Actors:** A2, A3
  - **Steps:** The computer keeps the minimum Relay connectivity needed for auth, discovery presence, setup coordination, and control actions such as stop; it does not require Relay to remain in the terminal data path when direct attach is healthy.
  - **Outcome:** Relay load scales primarily with control-plane activity rather than with all terminal traffic.
  - **Covered by:** R1, R6, R10, R14

---

## Requirements

**Architecture model**
- R1. The long-term attach architecture must separate control-plane responsibilities from terminal data-plane responsibilities.
- R2. The preferred attach path for a mobile client must be a direct mobile-to-computer path rather than Relay-mediated terminal forwarding.
- R3. Relay-mediated terminal forwarding must remain available as an automatic fallback when direct attach cannot be established or maintained.
- R4. Session attach must become path-agnostic at the product level: snapshot delivery, live-byte delivery, structured input, and attach lifecycle semantics must not depend on whether the active path is direct or Relay-backed.
- R5. The PTY owner on the computer must remain the authority for terminal state, snapshot generation, structured input handling, and session lifecycle regardless of attach path.

**Security and trust**
- R6. Terminal snapshot bytes, live terminal bytes, and structured input must remain end-to-end encrypted between mobile and computer on both the direct path and the Relay-backed fallback path.
- R7. Relay must not have access to plaintext terminal traffic, plaintext structured input, or the end-to-end session keys in either direct mode or fallback mode.
- R8. The trust relationship that enables end-to-end encryption must be established out of band between the mobile client and the computer, not through Relay-managed secret exchange.
- R9. Relay may coordinate attach setup and fallback routing, but it must not become a trusted cryptographic endpoint for terminal traffic.

**Connection model and scale**
- R10. Relay connection design should optimize for one long-lived control-plane relationship per app/device context rather than binding Relay transport shape permanently to one attached session.
- R11. The product must not require a dedicated Relay data-plane WebSocket per viewed session as its long-term scaling model.
- R12. The direct path should be the default way to reduce Relay byte load and Relay attach connection pressure at `10k`-class concurrent usage, rather than first investing in a Relay-only multiplexed attach transport.

**Fallback and user experience**
- R13. Direct attach failure must fall back automatically to Relay-backed encrypted forwarding without requiring the user to choose between security modes.
- R14. Discovery, authorization, session stop, and other account-scoped control actions must continue to work through Relay even when the terminal data path is direct.
- R15. The user-visible attach experience should remain one conceptual feature, not two separate products called "direct attach" and "Relay attach."

**Near-term prioritization**
- R16. The repository should not prioritize a standalone redesign of today's Relay-scoped attach WebSocket transport if that redesign does not materially advance the direct-attach plus encrypted-fallback architecture.
- R17. Near-term work should prefer abstractions and contracts that can support both attach paths over optimizations that only improve the current Relay-specific data path.

---

## Acceptance Examples

- AE1. **Covers R2, R4, R6, R8.** Given a mobile client and computer that have already established out-of-band trust, when the user opens a live session and direct connectivity succeeds, the client receives snapshot bytes, live bytes, and input acknowledgements over the direct path with the same attach semantics the product already exposes today.
- AE2. **Covers R3, R6, R7, R13.** Given a mobile client and computer that cannot complete direct connectivity, when attach falls back to Relay forwarding, Relay only sees routing metadata and encrypted payload frames, while the mobile client still attaches successfully.
- AE3. **Covers R10, R11, R16, R17.** Given near-term planning for Relay load reduction, when choosing between "build Relay-only multiplexed attach transport" and "build path-agnostic control/data separation," the latter is preferred unless the former is required as a direct stepping stone.

---

## Success Criteria

- Relay's long-term role is clearly defined as control plane first, with data-plane forwarding retained only as an encrypted fallback.
- Planning can proceed without inventing whether terminal traffic should stay Relay-terminated, whether fallback changes the security model, or whether Relay-side attach multiplexing is itself the product goal.
- Future implementation work can judge each change by one standard: does it move the system toward direct attach plus encrypted fallback, rather than merely making the current Relay-only data path more elaborate?

---

## Scope Boundaries

- No requirement in this phase to fully redesign the current Relay attach transport only for its own sake.
- No requirement that Relay understand terminal payload semantics once end-to-end encryption is introduced.
- No requirement that direct attach remove Relay from discovery, auth, or control actions.
- No requirement yet on the specific NAT traversal mechanism details beyond supporting direct mobile-to-computer attach.
- No requirement yet on the exact cryptographic protocol details, key formats, or handshake transcript.
- No requirement yet on whether the control plane uses one new unified app socket, existing APIs plus new signaling frames, or another concrete wire shape.

---

## Key Decisions

- Prefer direct attach over Relay optimization: reducing Relay pressure should come primarily from moving terminal traffic off Relay, not from first making Relay's current attach path more sophisticated.
- Keep one security model across success and fallback: users should not silently move from end-to-end encrypted direct mode into Relay-visible plaintext fallback.
- Treat Relay forwarding as blind transport when fallback is active: this preserves the same trust boundary in both attach modes.
- Make attach semantics transport-independent: "attach to a session" is the product feature; direct vs Relay-backed is an implementation path choice.

---

## Dependencies / Assumptions

- Mobile and computer can establish trust out of band, such as by scanning a QR code, without Relay learning the shared secret material.
- The product can tolerate attach setup that first attempts direct connectivity and only later falls back to Relay forwarding.
- Relay continues to be the account-scoped authority for session discovery and control even when it is not the primary terminal data path.

---

## Outstanding Questions

### Resolve Before Planning

- None.

### Deferred to Planning

- [Affects R3, R13][Technical] What exact attach-state machine should govern direct-attempt, timeout, fallback activation, and later re-upgrade to direct path if connectivity improves?
- [Affects R6-R9][Needs research] What end-to-end encryption handshake best fits an out-of-band trust bootstrap while keeping Relay ignorant of session keys in both direct and fallback modes?
- [Affects R10-R11][Technical] What concrete Relay control-plane transport should carry signaling, presence, and control actions once terminal traffic is no longer inherently tied to `GET /api/sessions/:id/attach/ws`?
- [Affects R14][Technical] Which control actions remain Relay-only versus which actions may also ride the direct path for latency or resilience reasons?
- [Affects R16-R17][Technical] What minimum refactor is needed to decouple current attach semantics from the existing Relay-specific WebSocket contract without creating a throwaway intermediate protocol?

---

## Next Steps

-> `/ce-plan` for structured implementation planning
