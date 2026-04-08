---
title: feat: Align relay protocol with input_text submit flag
type: feat
status: completed
date: 2026-04-08
origin: docs/protocol.md
---

# feat: Align relay protocol with input_text submit flag

## Overview

Update the relay protocol implementation to match `docs/protocol.md`: submit intent lives on `input_text.submit`, client input is limited to `input_text` and `input_key`, live output uses `data_b64`, and obsolete pre-release protocol shapes are deleted instead of preserved.

## Problem Frame

The current code on `feat/input-submit-protocol` still reflects an earlier wire contract:

- submit intent is represented by standalone `input_submit`
- raw bytes can still arrive through `input`
- live output still uses `data` in shared types

The updated [docs/protocol.md](docs/protocol.md) defines a narrower protocol:

- `input_text { submit: true }` is the submit shape
- `input_key` keeps its existing semantics
- forwarded client input messages are only `input_text` and `input_key`
- output bytes use `data_b64`

Because this feature is still pre-release, carrying dual protocol paths adds avoidable complexity. The plan assumes we should delete the old shapes now and align code, tests, and docs to a single contract.

## Requirements Trace

- R1. `input_text` is the canonical structured text message for both plain text and submit intent.
- R2. `input_text.submit` is optional and defaults to `false`.
- R3. `input_text { submit: false }` preserves plain-text behavior with no implicit Enter.
- R4. `input_text { submit: true }` appends exactly one trailing `\r` at the PTY-owner boundary as one serialized submit operation.
- R5. Existing `input_key` values and semantics remain unchanged.
- R6. The relay forwards `input_text` with the `submit` flag intact and does not decompose `submit: true` into separate text and key messages.
- R7. Standalone `input_submit` is removed from shared protocol types, relay handling, connector dispatch, and tests.
- R8. Raw-byte client `input` is removed from shared protocol types, relay handling, connector dispatch, and tests.
- R9. Agent output frames and relay live output updates use `data_b64` as the only output byte field in this revision.
- R10. Replay frames remain on `data_b64` unchanged.
- R11. Shared protocol types, relay behavior, connector behavior, and tests align with `docs/protocol.md`.
- R12. `README.md` and `docs/architecture.md` are updated where the external contract changed.

## Scope Boundaries

- No change to the supported `input_key` vocabulary or key-to-byte mapping behavior.
- No change to replay endpoint shape for `/api/sessions/:id/frames`, which already uses `data_b64`.
- No change to relay best-effort output semantics, timestamps, or terminal size metadata.
- No new draft-state synchronization or submit acknowledgement protocol.
- No compatibility layer for removed pre-release message types or fields.

## Context & Research

### Relevant Code and Patterns

- `protocol/message.go` still models the old contract: standalone `input_submit`, raw-byte `input`, `output.data`, and no `submit` field on text messages.
- `relay/server.go` currently validates `input`, `input_text`, `input_submit`, and `input_key` separately and decodes live agent output from `protocol.Message.Data`.
- `connector/connector.go` currently routes both `input` and `input_submit` as separate branches instead of expressing submit through `input_text.submit`.
- `session/remote_input.go` remains the PTY-owner boundary for translating structured input intent into actual PTY bytes.
- `relay/history.go` and replay responses already use `data_b64`, so the live-output rename should converge toward the existing replay shape.
- `protocol/relay_types_test.go`, `relay/server_test.go`, `connector/connector_test.go`, and `session/remote_input_test.go` are the contract tests that need to move with the protocol.
- The earlier [docs/plans/2026-04-08-001-feat-input-submit-protocol-plan.md](docs/plans/2026-04-08-001-feat-input-submit-protocol-plan.md) captures the superseded standalone-`input_submit` direction and remains historical context only.

### Institutional Learnings

- No `docs/solutions/` entries exist in this repository today.

### External References

- None. The source of truth for this plan is the local protocol document in `docs/protocol.md`.

## Key Technical Decisions

- Treat `docs/protocol.md` as authoritative over the already-landed implementation.
- Represent submit intent as a flag on `input_text`, not as a separate message type.
- Remove standalone `input_submit`, raw-byte `input`, and live `output.data` now rather than preserving migration shims.
- Keep submit byte synthesis at the PTY owner: relay forwards intent, and `agentunnel` performs the final `text + '\r'` write.
- Use `data_b64` as the only output byte field for agent output and live relay updates, matching the existing replay shape.

## Open Questions

### Resolved During Planning

- Should compatibility paths remain for old message shapes? No. Delete them in this pre-release revision.
- Where should submit intent live? On `input_text.submit`.
- Should existing `input_key` behavior move as part of this change? No.
- What should happen to live output field naming? Agent output and live client updates should use `data_b64` only.

### Deferred to Implementation

- None.

## High-Level Technical Design

> *This illustrates the intended approach and is directional guidance for review, not implementation specification. The implementing agent should treat it as context, not code to reproduce.*

```text
client `/api/updates/ws`
  -> send:
       - `input_text{text, submit?}`
       - `input_key{key, modifiers}`
  -> relay validates only documented input types
  -> relay forwards the same shape to `/agent/ws`
  -> connector receives:
       - `input_text{submit:false}` -> plain UTF-8 bytes
       - `input_text{submit:true}`  -> UTF-8 bytes + trailing `\r`
       - `input_key{...}`           -> supported key bytes
  -> PTY stdin

agent output
  -> emit `output{data_b64, cols, rows}`
  -> relay records retained history
  -> relay fans out live `output{data_b64, ...}`
```

## Implementation Units

- [ ] **Unit 1: Reshape shared protocol types around `input_text.submit` and `data_b64`**

**Goal:** Update the shared wire types so they represent only the revised protocol surface.

**Requirements:** R1, R2, R7, R8, R9, R11

**Dependencies:** None

**Files:**
- Modify: `protocol/message.go`
- Modify: `protocol/relay_types_test.go`

**Approach:**
- Add `submit` to the shared text-message shapes used between client and relay, and between relay and agent.
- Remove standalone `input_submit` and raw-byte `input` helpers, message variants, and round-trip tests from the shared protocol layer.
- Rename live-output byte fields from `data` to `data_b64` in shared output structs and helpers.

**Patterns to follow:**
- `protocol/message.go` helper naming and JSON tagging style
- existing JSON round-trip tests in `protocol/relay_types_test.go`

**Test scenarios:**
- Happy path: `input_text` JSON round-trips with `submit` omitted and decodes as non-submitting text.
- Happy path: `input_text { submit: true }` JSON round-trips with `text` preserved exactly.
- Happy path: live `output` JSON round-trips with `data_b64`, `cols`, and `rows`.
- Edge case: `submit` defaults to `false` when omitted from JSON.
- Edge case: removed `input_submit` and raw-byte `input` shapes no longer have shared protocol helpers or round-trip coverage.
- Integration: replay-frame JSON remains on `data_b64` with no field drift.

**Verification:**
- Shared protocol types expose only the documented input message shapes and output fields.

- [ ] **Unit 2: Align relay validation, forwarding, and live output handling**

**Goal:** Teach the relay to accept only the revised input contract and emit live output updates using `data_b64`.

**Requirements:** R1, R2, R5, R6, R7, R8, R9, R10, R11

**Dependencies:** Unit 1

**Files:**
- Modify: `relay/server.go`
- Modify: `relay/server_test.go`
- Modify: `relay/registry_test.go`

**Approach:**
- Remove raw `input` and standalone `input_submit` handling from client WebSocket validation and forwarding.
- Validate `input_text` while preserving the `submit` flag intact from client to agent.
- Continue forwarding `input_key` unchanged.
- Decode agent `output` from `data_b64` only and emit live client updates with the same field name.

**Patterns to follow:**
- existing safe-ignore websocket validation in `relay/server.go`
- output fanout and replay consistency patterns in `relay/registry.go` and `relay/history.go`
- websocket forwarding tests in `relay/server_test.go`

**Test scenarios:**
- Happy path: a client sends `input_text { submit: false }` and the owning agent receives the same shape unchanged.
- Happy path: a client sends `input_text { submit: true }` and the owning agent receives the same shape unchanged.
- Happy path: a client sends `input_key` and the owning agent receives the same key payload unchanged.
- Happy path: an agent sends `output { data_b64: ... }` and live client updates expose `data_b64`.
- Edge case: a client omits `submit` and relay behavior treats it as non-submitting text.
- Edge case: removed `input_submit` or raw `input` messages are not forwarded by relay input handling.
- Edge case: `output` frames missing `data_b64` do not reach retained history or live output fanout.
- Integration: replay responses remain unchanged on `data_b64` while live output uses the same field name.

**Verification:**
- Relay transport behavior matches `docs/protocol.md` and no longer carries obsolete input or output shapes.

- [ ] **Unit 3: Merge submit semantics into the PTY-owner text path**

**Goal:** Remove dedicated submit and raw-input branches from the PTY-owner path and drive submit behavior from `input_text.submit`.

**Requirements:** R3, R4, R5, R6, R7, R8, R11

**Dependencies:** Unit 1, Unit 2

**Files:**
- Modify: `connector/connector.go`
- Modify: `connector/connector_test.go`
- Modify: `session/remote_input.go`
- Modify: `session/remote_input_test.go`

**Approach:**
- Change text translation helpers so text input can express both plain text and submit intent without changing existing key handling.
- Route canonical submit behavior through `input_text.submit` in the connector.
- Delete connector handling for standalone `input_submit` and raw-byte `input`.
- Keep `input_key` behavior byte-for-byte unchanged.

**Execution note:** Start with characterization-style tests that prove existing `input_key` behavior is unchanged, then move submit semantics into the text path.

**Patterns to follow:**
- current PTY-owner translation boundary in `session/remote_input.go`
- table-driven connector routing tests in `connector/connector_test.go`

**Test scenarios:**
- Happy path: `input_text { text: "hello", submit: false }` writes exactly `hello`.
- Happy path: `input_text { text: "hello", submit: true }` writes exactly `hello\r` in one hub write.
- Happy path: `input_text { text: "", submit: true }` writes exactly `\r`.
- Edge case: omitted `submit` behaves the same as `submit: false`.
- Edge case: `input_key("ENTER")` continues to map to `\r` unchanged.
- Edge case: removed `input_submit` and raw `input` messages no longer produce PTY writes.
- Integration: canonical submit no longer depends on a separate runtime message type.

**Verification:**
- Submit behavior is expressed through the text path without changing existing `input_key` mappings or leaving obsolete input branches behind.

- [ ] **Unit 4: Realign repo docs around the revised protocol**

**Goal:** Make repo-facing docs reflect the final pre-release protocol surface.

**Requirements:** R9, R11, R12

**Dependencies:** Unit 1, Unit 2, Unit 3

**Files:**
- Modify: `README.md`
- Modify: `docs/architecture.md`

**Approach:**
- Update `README.md` and `docs/architecture.md` so they describe submit intent in terms of `input_text.submit`.
- Remove any remaining repo-doc references to standalone `input_submit`, raw `input`, or live `output.data`.
- Leave `docs/protocol.md` as the canonical protocol source.

**Patterns to follow:**
- documentation alignment rules in `CLAUDE.md`
- existing plan-file conventions in `docs/plans/`

**Test scenarios:**
- Test expectation: none -- this unit is documentation alignment rather than runtime behavior.

**Verification:**
- A reader can identify `docs/protocol.md` as canonical and the rest of the repo docs no longer describe removed protocol shapes.

## System-Wide Impact

- **Interaction graph:** The change touches shared protocol types, relay WebSocket validation, connector dispatch, PTY-owner translation, and protocol-facing docs.
- **Error propagation:** The main risk is deleting old branches without updating every caller and test that still references them.
- **State lifecycle risks:** Submit atomicity remains the critical invariant, but the source of submit intent moves from message type to message field.
- **API surface parity:** `docs/protocol.md`, `protocol/message.go`, `relay/server.go`, and websocket tests must agree on `submit`, accepted input types, and `data_b64`.
- **Integration coverage:** The most important cross-layer proofs are `input_text.submit` preserving atomic submit behavior and `data_b64` flowing through live output and replay consistently.
- **Unchanged invariants:** `input_key` semantics, replay shape, output sequencing, timestamps, and relay content opacity remain unchanged.

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| Removing old protocol branches leaves hidden references in code or tests | Search for `input_submit`, raw `input`, and `output.data` usage up front and rewrite or delete each caller in the same change set |
| Live output rename drifts from the replay shape | Keep replay on its existing `data_b64` shape and make live-output tests assert the same field name explicitly |
| `input_key` behavior changes accidentally during the text-path refactor | Characterize and preserve `input_key` tests before moving submit semantics into `input_text` |

## Documentation / Operational Notes

- `docs/protocol.md` is the source of truth for this follow-up.
- This is a pre-release protocol cleanup, so the plan prefers deleting obsolete surfaces over carrying compatibility shims.
- No extra production monitoring is implied by the plan itself; rollout sensitivity is contract alignment across shared types, relay handling, and docs.

## Sources & References

- Source document: `docs/protocol.md`
- Related prior plan: `docs/plans/2026-04-08-001-feat-input-submit-protocol-plan.md`
- Related code: `protocol/message.go`
- Related code: `relay/server.go`
- Related code: `connector/connector.go`
- Related code: `session/remote_input.go`
