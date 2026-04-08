---
date: 2026-04-08
topic: input-submit-protocol
---

# Atomic Text Submit Protocol

## Problem Frame

The relay protocol already distinguishes between plain text input and special-key input, which is enough for remote typing and remote keypresses. It is not enough for a mobile draft-and-send experience where the client has explicit local text state and a separate submit action.

Today a client can approximate submit by sending `input_text` followed by `input_key("ENTER")`, but that is the wrong product contract for this interaction. The client intent is not "type these bytes, then maybe later press Enter." The intent is "submit this draft now." That distinction matters because the protocol should expose draft updates, raw Enter keypresses, and explicit submit actions as different semantics.

This brainstorm defines an additive input mode, `input_submit`, so clients can submit text atomically while preserving the current architecture boundary: the relay stays content-opaque, and `agentunnel`, as the PTY owner, remains responsible for the final PTY write behavior.

| Client Intent | Wire Event | PTY Effect | Notes |
|---|---|---|---|
| Update local draft or send plain text without executing | `input_text{text}` | write `text` bytes only | Does not imply submit |
| Press the Enter key remotely | `input_key{key:"ENTER"}` | write carriage return only | Represents a keypress, not a draft send |
| Submit the current draft now | `input_submit{text}` | write `text` plus one trailing carriage return as one serialized operation | Represents explicit send/execute intent |

## Requirements

**Input Intent Model**
- R1. The protocol must define `input_submit` as a distinct client input event alongside `input_text` and `input_key`.
- R2. `input_text` must remain the non-submitting text path for normal typing, pasted text, IME-committed text, and local draft updates.
- R3. `input_key("ENTER")` must remain the protocol meaning for a remote Enter keypress, independent of any local draft-send UX.
- R4. Clients must be able to choose between `input_text`, `input_key("ENTER")`, and `input_submit` based on intent rather than inventing their own submit macros.

**Submit Semantics**
- R5. `input_submit{text}` must mean "submit this text now" rather than "type this text."
- R6. The effective submit terminator for `input_submit` must be a carriage return (`\r`), with the same PTY behavior as the existing `input_key("ENTER")` path.
- R7. The text body in `input_submit` must be UTF-8 text and may include embedded newline characters such as `\n`.
- R8. `input_submit` must append exactly one protocol-defined trailing carriage return beyond the provided text body.
- R9. `input_submit{text:""}` must be valid and must have the same PTY effect as submitting an empty draft followed by one Enter keypress.
- R10. The submit text and its appended carriage return must reach the PTY as one serialized submit operation for that session, with no interleaving remote input between them.

**Architecture and Compatibility**
- R11. The relay must forward `input_submit` as a distinct structured event and must not decompose it into separate forwarded text and key events.
- R12. The PTY owner must remain the boundary that turns `input_submit` into the final PTY byte write behavior.
- R13. `input_submit` must be additive: existing `input_text` and `input_key` behavior remains valid.
- R14. The core docs must explain the distinction between draft text, Enter keypress, and explicit submit clearly enough that client authors can choose the correct event shape without guessing.

## Success Criteria

- A client author can tell when to use `input_text`, when to use `input_key("ENTER")`, and when to use `input_submit`.
- The protocol no longer requires clients to emulate draft submit by splitting it into two separate messages.
- Planning and implementation can proceed without inventing newline, empty-submit, or ownership semantics for `input_submit`.

## Scope Boundaries

- This work does not change the meaning of `input_text`.
- This work does not remove or redefine `input_key("ENTER")`.
- This work does not add relay-side transcript semantics, acknowledgements, or draft-state synchronization.
- This work does not expand special-key coverage beyond the existing structured-input direction.
- This work does not remove the temporary legacy raw `input` compatibility path.

## Key Decisions

- Submit is distinct from keypress: `input_submit` represents an explicit send action, while `input_key("ENTER")` continues to represent a remote Enter keypress.
- Submit uses carriage return semantics: `input_submit` appends `\r`, not `\n`, and it must match the current PTY behavior of `ENTER`.
- Empty submit is valid: `input_submit{text:""}` is equivalent to pressing Enter on an empty draft.
- Multi-line submit is allowed: the body text may contain embedded `\n`; the protocol still appends one trailing submit carriage return separately.
- Relay stays content-opaque: the relay forwards submit intent, while `agentunnel` performs the final serialized PTY write.
- `docs/protocol.md` remains canonical: protocol documentation should continue to live in `docs/protocol.md` rather than splitting across multiple sources of truth.

## Dependencies / Assumptions

- The existing `ENTER` behavior in the PTY-owner path remains carriage return based.
- Session input handling continues to preserve per-session ordering once an input event is handed to the owning `agentunnel` session.
- Mobile and other external clients can distinguish local draft editing from explicit submit actions in their UX.

## Outstanding Questions

### Deferred to Planning
- [Affects R10][Technical] What is the smallest implementation change that makes the serialized `text + \r` PTY write explicit and testable in the PTY-owner path?
- [Affects R11][Technical] What validation should the relay apply to `input_submit` beyond session existence and JSON shape?
- [Affects Scope Boundaries][Technical] When should the temporary legacy raw `input` path be removed after `input_submit` rolls out?

## Next Steps

→ `/prompts:ce-plan` for structured implementation planning
