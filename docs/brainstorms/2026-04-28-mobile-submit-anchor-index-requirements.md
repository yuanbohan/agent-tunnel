---
date: 2026-04-28
topic: submit-anchor-index
---

# Submit Anchor Index

## Problem Frame

When a user interacts with a web coding agent from the mobile client, the agent can produce long terminal output. After several turns, the user has trouble finding where they submitted an earlier prompt and quickly jumping back to that part of the session.

The first version should help the mobile client navigate to user-submitted interaction entry points without turning the relay into transcript storage, without parsing Codex-specific UI output, and without promising durable history beyond the existing agent-local bounded snapshot model.

---

## Actors

- A1. User: submits prompts from either the local terminal or an attached mobile client and later wants to jump back to those submissions.
- A2. Mobile client: renders the terminal attach stream, shows right-side jump dots, and scrolls to selected anchors.
- A3. Tunnel agent: owns the PTY, terminal mirror, attach snapshot, and bounded in-memory anchor state for the running session.
- A4. Relay: authenticates and routes attach traffic while remaining content-opaque.

---

## Key Flows

- F1. Submit and mark an anchor
  - **Trigger:** A user sends an input event that writes the `ENTER` carriage return to the PTY outside a bracketed-paste region, either from the local terminal or a remote attach.
  - **Actors:** A1, A2, A3, A4
  - **Steps:** The input reaches the Tunnel session Hub, the agent writes it to the PTY, and the agent records a submit anchor near the current terminal mirror position if the write succeeds.
  - **Outcome:** The running session has a bounded, agent-local anchor representing the start of that submitted interaction.
  - **Covered by:** R1, R2, R4

- F2. Reattach and restore jump dots
  - **Trigger:** The mobile client refreshes, reconnects, or attaches after output has grown.
  - **Actors:** A1, A2, A3, A4
  - **Steps:** The client opens a fresh attach, the agent sends the normal terminal snapshot, and the agent also makes the still-valid submit anchors available for that snapshot. The mobile client renders dots for anchors that map into retained terminal history.
  - **Outcome:** The user can jump to recent submit positions that survived within the bounded snapshot context.
  - **Covered by:** R3, R5, R6

- F3. Anchor expires with scrollback
  - **Trigger:** The session produces enough output that an older anchor falls outside the agent-local retained context.
  - **Actors:** A1, A2, A3
  - **Steps:** The agent drops or omits the expired anchor, and the mobile client no longer shows a jump dot for it on fresh attach.
  - **Outcome:** The product remains honest about bounded recovery and does not imply durable transcript history.
  - **Covered by:** R6, R9

---

## Requirements

**Anchor Semantics**
- R1. A jump dot must represent a local or remote submit Enter event, not every keypress, every text edit, or every terminal output chunk.
- R2. A jump dot must be described and treated as a "user submit anchor" or "turn entry anchor," not as a guaranteed marker for the exact Codex-rendered user message block.
- R3. The first version must treat local-terminal Enter, mobile Local Draft submit, and mobile Remote Streaming Enter consistently by recording anchors at the PTY input boundary, while ignoring carriage returns inside bracketed paste.

**Session Behavior**
- R4. The Tunnel agent must keep submit anchors in memory for the lifetime of the running session, bounded to the same kind of recent-context contract as the terminal mirror snapshot and capped to the latest 256 valid anchors.
- R5. A fresh attach or reattach must allow the mobile client to recover currently valid submit anchors along with the terminal snapshot context.
- R6. Anchors that no longer map into retained terminal context must not be shown as jump targets after refresh or reattach.
- R7. The mobile client must be able to render multiple anchors for one session and jump to the corresponding retained terminal location.

**Relay and Protocol Boundaries**
- R8. The relay must remain content-opaque. It may route anchor metadata if needed, but it must not parse terminal bytes, derive input locations from output, or store transcript history.
- R9. This feature must not introduce durable transcript replay, relay-side scrollback storage, or a claim that every historical input remains recoverable.
- R10. Existing attach recovery semantics remain intact: a reconnect is a fresh snapshot plus live bytes, not missed-byte replay.

**User Experience**
- R11. The mobile UI should make dots useful for navigation without implying exact semantic knowledge of Codex output. If precision is approximate, the UI should still land near the user's submitted turn.
- R12. The UI should avoid visual noise by showing submit-level anchors only, not per-character or per-key events.

---

## Acceptance Examples

- AE1. **Covers R1, R2, R7.** Given a user submits three prompts in one live session, when the user scrolls later, the mobile client shows three navigation dots and tapping one jumps near the corresponding submitted turn.
- AE2. **Covers R3.** Given a user submits once from the local terminal and once from mobile Remote Streaming with `input_key ENTER`, when the mobile client refreshes, both retained submits can appear as dots.
- AE3. **Covers R5, R6, R9.** Given an older submit anchor has fallen outside the retained terminal context, when the mobile client reattaches, that anchor is omitted rather than shown as a broken or stale jump target.
- AE4. **Covers R8, R10.** Given the relay handles the attach websocket, when anchors are delivered to the mobile client, the relay does not inspect terminal bytes or persist transcript content.

---

## Success Criteria

- A mobile user can quickly jump between recent submitted prompts in a long coding-agent session after refresh or reattach, regardless of whether those submits came from the local terminal or mobile attach input.
- The implementation preserves the current relay boundary: no relay-owned terminal emulation, transcript storage, or durable replay API.
- Downstream planning can proceed without inventing the v1 anchor meaning, input-source scope, or history boundary.

---

## Scope Boundaries

- No Codex-specific parsing of terminal output in Tunnel or Relay.
- No guarantee that the dot lands on the exact rendered user-message block inside Codex's TUI.
- No per-key anchors beyond submit Enter events in v1.
- No per-key, per-character, or draft-edit anchor dots.
- No durable history across process exit, agent restart with a new session, or retained scrollback expiry.
- No relay-side transcript, scrollback cache, or replay endpoint.
- No mobile-controlled PTY sizing change as part of this feature.

---

## Key Decisions

- Use submit Enter anchors for v1: this gives the user the navigation affordance they need while keeping local terminal and mobile input behavior consistent at the PTY input boundary.
- Keep anchors agent-local and bounded: this matches the existing terminal mirror ownership model and avoids changing Relay into a stateful transcript service.
- Treat Codex-rendered message anchors as a later semantic-marker problem: precise TUI message locations require cooperation from the TUI rather than inference from raw terminal bytes.

---

## Dependencies / Assumptions

- The mobile client sends Local Draft submissions as `input_text` with `submit: true`; Remote Streaming submissions may end with `input_key { "key": "ENTER" }`.
- The mobile terminal renderer can scroll to a line or equivalent retained terminal position after a fresh snapshot restore.
- The Tunnel agent can associate a submit event with a current or near-current terminal mirror position closely enough to be useful.
- The existing bounded scrollback snapshot remains the recovery envelope for refreshed mobile clients.

---

## Outstanding Questions

### Deferred to Planning

- [Affects R4, R5][Technical] What exact line-position representation should the agent expose so mobile can map an anchor into its terminal emulator after snapshot restore?
- [Affects R4, R6][Technical] Should anchor expiry be driven by terminal buffer markers, explicit snapshot range checks, or another mirror-owned mechanism?
- [Affects R5, R8][Technical] Should anchor metadata be delivered as a new attach control message, included near `snapshot_done`, or exposed through another attach-scoped mechanism?
- [Affects R11][Technical] What visual treatment should the mobile scrollbar dots use when anchors are dense or near each other?

---

## Next Steps

-> /ce-plan for structured implementation planning
