---
title: feat: Add atomic input_submit relay protocol
type: feat
status: completed
date: 2026-04-08
origin: docs/brainstorms/2026-04-08-input-submit-protocol-requirements.md
---

# feat: Add atomic input_submit relay protocol

## Overview

Add `input_submit` as a third structured client-input event so mobile and other remote clients can submit a text draft atomically without splitting the action into `input_text` plus `input_key("ENTER")`. The relay will accept and forward `input_submit` unchanged, and `agentunnel`, as the PTY owner, will turn it into one serialized PTY write of `text + '\r'`.

## Problem Frame

The current structured-input contract distinguishes plain text from special keys, which is enough for remote typing but not for a draft-and-send mobile UX. A client can approximate submit today by sending text and then a separate Enter key event, but that forces the client to synthesize protocol behavior that should be expressed directly by the wire contract.

This change adds the missing submit intent while preserving the existing ownership boundaries from `docs/brainstorms/2026-04-08-input-submit-protocol-requirements.md`: the relay remains content-opaque, `input_key("ENTER")` keeps its current keypress meaning, and `agentunnel` remains the only layer that converts submit intent into PTY bytes.

## Requirements Trace

- R1. The protocol defines `input_submit` as a distinct client input event.
- R2. `input_text` remains the non-submitting path for plain text and draft updates.
- R3. `input_key("ENTER")` remains the protocol meaning for a remote Enter keypress.
- R4. Clients can choose the correct event shape based on intent instead of inventing submit macros.
- R5. `input_submit{text}` means explicit draft submit, not plain typing.
- R6. `input_submit` appends one carriage return with the same PTY effect as the current `ENTER` path.
- R7. `input_submit.text` remains UTF-8 text and may include embedded `\n`.
- R8. The protocol appends exactly one submit carriage return beyond the provided text body.
- R9. Empty submit remains valid and behaves like pressing Enter on an empty draft.
- R10. Submit text and its trailing carriage return reach the PTY as one serialized session-local write.
- R11. The relay forwards `input_submit` as one structured event rather than decomposing it.
- R12. `agentunnel` remains the PTY-owner boundary that decides the final byte write.
- R13. Existing `input_text`, `input_key`, and legacy raw `input` compatibility remain intact.
- R14. Core docs clearly explain draft text vs Enter keypress vs explicit submit.

## Scope Boundaries

- No change to the meaning of `input_text`.
- No change to the meaning of `input_key("ENTER")`.
- No relay-side synthesis of PTY byte sequences from structured input.
- No retained input history, submit acknowledgement, or draft synchronization protocol.
- No immediate removal of the temporary legacy raw `input` path.
- No broader special-key expansion beyond the already-supported structured-input model.

## Context & Research

### Relevant Code and Patterns

- `protocol/message.go` is the shared wire contract for client-to-relay and relay-to-agent structured input.
- `relay/server.go` already validates client websocket input types and forwards them through `Registry.WriteInput(...)`.
- `relay/registry.go` is already generic over `protocol.Message`, so relay routing does not need a new transport abstraction for `input_submit`.
- `connector/connector.go` is the sole relay-to-local boundary that turns forwarded messages into `session.Hub.WriteInput(...)` calls.
- `session/remote_input.go` already centralizes PTY-owner-side translation for `input_text` and `input_key`, including `ENTER -> '\r'`.
- `protocol/relay_types_test.go`, `relay/server_test.go`, `connector/connector_test.go`, and `session/remote_input_test.go` already contain the right contract-test shapes to extend.
- `README.md`, `docs/architecture.md`, and `docs/protocol.md` are the operator-facing and client-facing docs called out by repo guidance for protocol changes.

### Institutional Learnings

- No `docs/solutions/` directory or prior institutional learnings exist in this repository today.

### External References

- None. The repo already has direct local patterns for structured input transport, PTY-owner translation, and protocol testing, so external research would add little practical value here.

## Key Technical Decisions

- Forward submit as one structured event: The relay will accept `input_submit` and forward it unchanged over `/agent/ws` so message intent stays explicit end to end.
- Encode submit in the PTY-owner layer: `agentunnel` will turn `input_submit{text}` into one PTY write of `text` plus a trailing `\r`, keeping PTY byte behavior close to the PTY owner.
- Reuse existing Enter semantics: The trailing submit byte sequence must match the current `input_key("ENTER")` mapping rather than defining a second newline convention.
- Keep relay validation minimal and content-opaque: Relay validation should mirror `input_text` rather than inspecting text content; empty string and embedded `\n` are valid submit payloads.
- Keep `docs/protocol.md` canonical: Existing repo references and doc expectations already point to `docs/protocol.md`, so execution should update that file as the single protocol source of truth.

## Open Questions

### Resolved During Planning

- How should `input_submit` remain atomic? By forwarding one `input_submit` event and converting it into one `session.Hub.WriteInput(...)` call containing `text + '\r'`.
- What relay validation should apply to `input_submit`? Accept the known type, require `session_id`, and otherwise avoid content inspection so empty text and embedded newlines remain valid.
- Which protocol document should be canonical? `docs/protocol.md`, because current repo guidance and architecture links already point there.

### Deferred to Implementation

- The exact removal point for legacy raw `input` should remain a later migration decision after clients and `agentunnel` both use structured submit in practice.

## High-Level Technical Design

> *This illustrates the intended approach and is directional guidance for review, not implementation specification. The implementing agent should treat it as context, not code to reproduce.*

```text
client `/api/updates/ws`
  -> send one of:
       - `input_text{text}`
       - `input_key{key, modifiers}`
       - `input_submit{text}`
  -> relay validates type + session identity
  -> relay forwards the same structured event over `/agent/ws`
  -> connector receives the event
  -> PTY-owner input translator maps:
       - `input_text`   -> UTF-8 bytes
       - `input_key`    -> supported key bytes
       - `input_submit` -> UTF-8 text bytes + trailing `\r`
  -> connector performs one `Hub.WriteInput(...)` call for that translated payload
  -> PTY stdin
```

## Implementation Units

- [x] **Unit 1: Extend the shared protocol contract with `input_submit`**

**Goal:** Teach the shared protocol types and round-trip tests about the new structured submit event without disturbing existing input or output contracts.

**Requirements:** R1, R5, R6, R7, R8, R9, R11, R13

**Dependencies:** None

**Files:**
- Modify: `protocol/message.go`
- Modify: `protocol/relay_types_test.go`

**Approach:**
- Add `input_submit` to the documented message-type set for both `protocol.Message` and `protocol.ClientInputMessage`.
- Introduce encode helpers for client-side and relay-to-agent submit events, mirroring the existing `input_text` / `input_key` helper style.
- Extend `ClientInputMessage.AgentMessage()` so relay forwarding preserves `input_submit` as a distinct message type.
- Keep field reuse minimal: `input_submit` should carry `text` only, without creating a second submit-specific payload shape.

**Patterns to follow:**
- `protocol/message.go` helper naming and JSON field tagging
- `protocol/relay_types_test.go` round-trip coverage for `input_text`, `input_key`, and legacy raw `input`

**Test scenarios:**
- Happy path: `input_submit` client JSON round-trips with `session_id`, `type`, and `text` preserved.
- Happy path: relay-to-agent `input_submit` JSON round-trips with `type="input_submit"` and `text` preserved.
- Edge case: `input_submit{text:""}` round-trips without being coerced into another message type.
- Edge case: `input_submit` with embedded `\n` preserves the original text body exactly.
- Integration: `ClientInputMessage.AgentMessage()` maps `input_submit` to an agent `Message` with the same `text`.
- Integration: legacy raw `input`, `input_text`, and `input_key` round-trips continue unchanged.

**Verification:**
- The shared protocol package can represent `input_submit` end to end without overloading existing text or key event types.

- [x] **Unit 2: Accept and forward `input_submit` through the relay**

**Goal:** Let `/api/updates/ws` ingest `input_submit` and route it to the owning agent session without decomposing it or inspecting its contents.

**Requirements:** R1, R4, R5, R7, R9, R11, R13

**Dependencies:** Unit 1

**Files:**
- Modify: `relay/server.go`
- Modify: `relay/server_test.go`

**Approach:**
- Extend the client websocket type switch to recognize `input_submit` as a valid structured input event.
- Validate `input_submit` like `input_text`: require a non-empty `session_id`, but allow empty `text` and embedded newlines.
- Continue routing through `Registry.WriteInput(...)` so session ownership and relay fanout behavior remain unchanged.
- Preserve message boundaries by forwarding one `protocol.Message{Type:"input_submit"}` to the agent peer, not a synthesized text-plus-key pair.

**Patterns to follow:**
- Existing validation and safe-ignore structure in `relay/server.go`
- Existing websocket forwarding tests in `relay/server_test.go`

**Test scenarios:**
- Happy path: a client sends `input_submit{text:"hello"}` and the owning agent peer receives one forwarded `input_submit` message with `text:"hello"`.
- Happy path: a client sends `input_submit{text:""}` and the owning agent peer still receives one forwarded `input_submit` message.
- Edge case: a client sends `input_submit` with embedded `\n` and the forwarded message preserves the exact text body.
- Edge case: an `input_submit` for an unknown `session_id` does not reach any agent peer.
- Error path: unauthenticated `/api/updates/ws` access remains rejected with `401`.
- Integration: existing `input_text` and `input_key` forwarding tests remain green and unchanged in behavior.

**Verification:**
- Relay-side structured input routing accepts submit intent without becoming responsible for PTY-byte composition.

- [x] **Unit 3: Encode submit intent into one PTY write inside `agentunnel`**

**Goal:** Convert forwarded `input_submit` events into one serialized PTY-owner write containing the original text body plus a trailing carriage return.

**Requirements:** R3, R5, R6, R7, R8, R9, R10, R12, R13

**Dependencies:** Unit 1, Unit 2

**Files:**
- Modify: `session/remote_input.go`
- Modify: `session/remote_input_test.go`
- Modify: `connector/connector.go`
- Modify: `connector/connector_test.go`

**Approach:**
- Add a PTY-owner-side helper for submit encoding so the submit byte composition lives next to the existing text and key translation helpers.
- Implement submit encoding as `text bytes + ENTER byte sequence`, with the helper explicitly reusing the current carriage-return semantics rather than duplicating magic values across packages.
- Extend the connector message switch with an `input_submit` branch that performs one `hub.WriteInput(...)` call using the translated submit payload.
- Keep the connector’s existing `input_text`, `input_key`, and legacy raw `input` behavior unchanged.

**Execution note:** Start with the submit-encoding tests in `session/remote_input_test.go` before wiring the new connector branch so the byte-level contract is fixed first.

**Patterns to follow:**
- `session/remote_input.go` as the PTY-owner translation boundary
- `connector/connector.go` as the relay-to-local dispatch switch
- `connector/connector_test.go` table-driven input-routing tests

**Test scenarios:**
- Happy path: `input_submit{text:"hello"}` becomes exactly `hello\r` in one write to the hub.
- Happy path: `input_submit{text:"line1\nline2"}` becomes exactly `line1\nline2\r` in one write to the hub.
- Happy path: `input_submit{text:""}` becomes exactly `\r` in one write to the hub.
- Edge case: `input_key("ENTER")` still maps to `\r` without going through the submit helper.
- Edge case: `input_text{"hello"}` still maps to `hello` without any appended carriage return.
- Integration: connector routing of `input_submit` writes one payload to the hub rather than separate text and Enter writes.
- Integration: legacy raw `input`, `input_text`, and `input_key` routing continue to behave exactly as before.

**Verification:**
- `agentunnel` can accept submit intent and guarantee a single PTY-owner write for the full `text + '\r'` payload.

- [x] **Unit 4: Reconcile docs and eliminate protocol-source drift**

**Goal:** Align the canonical protocol docs and operator-facing docs with the new submit contract while preventing drift across the formal documentation set.

**Requirements:** R3, R4, R5, R6, R7, R9, R11, R14

**Dependencies:** Unit 1, Unit 2, Unit 3

**Files:**
- Modify: `docs/protocol.md`
- Modify: `docs/architecture.md`
- Modify: `README.md`

**Approach:**
- Update `docs/protocol.md` as the canonical client contract to describe `input_submit`, including empty submit, embedded newline allowance, and the requirement that the relay forwards it unchanged.
- Update `docs/architecture.md` so the input flow explicitly mentions `input_submit` and the PTY-owner-side serialized submit write.
- Update `README.md` only where externally visible protocol behavior changed, keeping the operator-facing summary concise.

**Patterns to follow:**
- Documentation alignment rules in `CLAUDE.md`
- Existing protocol wording style in `docs/protocol.md`

**Test scenarios:**
- Test expectation: none -- this unit is documentation and cleanup work rather than new runtime behavior.

**Verification:**
- A client author reading repo docs sees one unambiguous protocol source of truth and can distinguish draft text, Enter keypress, and submit intent without guessing.

## System-Wide Impact

- **Interaction graph:** `input_submit` adds one new client-input variant across `protocol`, `relay`, `connector`, `session`, and the public docs surface.
- **Error propagation:** Relay-side failures should keep following the existing safe-ignore posture for malformed or unknown websocket input while preserving unauthorized-access behavior.
- **State lifecycle risks:** The primary lifecycle risk is accidental decomposition of submit into multiple writes, which would weaken the atomicity guarantee the new protocol event exists to provide.
- **API surface parity:** The accepted event list must stay aligned across `protocol/message.go`, `relay/server.go`, `docs/protocol.md`, `docs/architecture.md`, and `README.md`.
- **Integration coverage:** The important cross-layer proof is that one client `input_submit` event becomes one forwarded agent message and one PTY-owner write with the expected `text + '\r'` payload.
- **Unchanged invariants:** `input_text` remains non-submitting text, `input_key("ENTER")` remains a keypress, and the relay remains content-opaque rather than synthesizing terminal bytes.

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| Submit gets decomposed into separate text and Enter operations during implementation | Keep `input_submit` as a first-class message type through relay forwarding and cover the connector path with single-write tests |
| `input_submit` drifts from current Enter semantics | Implement submit encoding by reusing the existing Enter mapping semantics and characterize the exact `\r` outcome in tests |
| Docs drift across `docs/protocol.md`, `docs/architecture.md`, and `README.md` | Keep `docs/protocol.md` as canonical and update the other docs in the same change set |
| The new submit path breaks existing structured input or legacy raw input handling | Extend existing table-driven protocol, relay, connector, and PTY translation tests rather than adding an isolated unproven path |

## Documentation / Operational Notes

- This is a public protocol change for remote clients, so `README.md`, `docs/protocol.md`, and `docs/architecture.md` all need to remain aligned in the same PR.
- There is no rollout-specific runtime migration inside the relay or agent process; compatibility risk is primarily client-contract drift during the transition from draft-plus-Enter macros to explicit submit events.

## Sources & References

- **Origin document:** `docs/brainstorms/2026-04-08-input-submit-protocol-requirements.md`
- Related code: `protocol/message.go`
- Related code: `relay/server.go`
- Related code: `connector/connector.go`
- Related code: `session/remote_input.go`
- Related prior plan: `docs/plans/2026-04-06-001-feat-structured-special-key-input-plan.md`
