---
title: feat: Structured special-key input over relay
type: feat
status: completed
date: 2026-04-06
---

# feat: Structured special-key input over relay

## Overview

Add structured client input so mobile clients can send normal text and special keys separately while preserving the current relay architecture. The relay will accept `input_text` and `input_key` events from `/api/updates/ws` and forward them to the owning `agentunnel` session. `agentunnel`, as the PTY owner, will translate supported key events into real PTY input bytes and write them to stdin.

## Problem Frame

The current client input path only supports raw base64 PTY bytes. That works for desktop-style raw terminal input, but it is a poor fit for mobile clients that need to express IME-committed text, navigation keys, and control-key shortcuts without manufacturing terminal escape sequences themselves. The system already has stable patterns for forwarding output and live session routing; this change needs to extend that model to structured input without turning the relay into a terminal interpreter, while also removing the standalone resize event and carrying size metadata on every output frame.

## Requirements Trace

- R1. Mobile and other external clients can send normal text without constructing raw PTY byte payloads.
- R2. Mobile and other external clients can send special keys as symbolic events and have them reach the real PTY correctly.
- R3. The relay remains a live broker and does not own terminal key-to-byte mapping logic.
- R4. Existing output replay, sequence assignment, and session removal behavior remain unchanged.
- R5. The protocol preserves a safe compatibility path while structured input is rolled out.
- R6. The first implementation supports at least `ENTER`, `BACKSPACE`, `TAB`, `ESCAPE`, `UP`, `DOWN`, `LEFT`, `RIGHT`, `HOME`, `END`, `PAGE_UP`, `PAGE_DOWN`, `DELETE`, and `Ctrl+A-Z`.
- R7. Every retained output frame includes a timestamp so clients can render time-aware output history, and live output events expose the same timestamp for consistency.
- R8. Every output frame uploaded by `agentunnel` includes `cols` and `rows`, and the protocol no longer uses a standalone resize event.

## Scope Boundaries

- No relay-side inference of PTY semantics from output text.
- No `session_state` or `action_required` protocol in this change.
- No retained input history or input replay.
- No multi-controller arbitration; concurrent client inputs continue to resolve by relay arrival order.
- No full `Alt+...` or function-key coverage in v1 beyond explicitly supported combinations.
- No bracketed paste protocol or paste-specific event type in v1; pasted content continues through `input_text`.
- No separate resize stream; size changes are represented only by subsequent output frames with updated `cols` and `rows`.

## Context & Research

### Relevant Code and Patterns

- `protocol/message.go` defines the shared wire types for the agent websocket and client update stream.
- `relay/server.go` already separates client-facing `/api/updates/ws` handling from agent-facing `/agent/ws` forwarding and validates session existence through `Registry`.
- `relay/registry.go` is the existing ownership boundary for routing input to the live session owner.
- `connector/connector.go` is the single place where relay protocol messages are translated into local `session.Hub` writes.
- `session/hub.go` already owns raw PTY input writes and is the right sink for translated bytes.
- `relay/server_test.go`, `connector/connector_test.go`, and `protocol/relay_types_test.go` already exercise the current output, resize, and raw-input contracts and should be extended rather than bypassed.

### Institutional Learnings

- No `docs/solutions/` entries exist in this repository today.

### External References

- None. The repository already has strong local patterns for the affected transport and session layers, and this work is primarily an internal protocol evolution rather than framework-specific design.

## Key Technical Decisions

- Use structured client input with two event shapes, `input_text` and `input_key`, instead of requiring mobile clients to emit raw terminal bytes.
- Keep key-to-byte mapping on the `agentunnel` side, not in the relay, because `agentunnel` owns the real PTY and terminal behavior.
- Add a dedicated client-input wire type for `/api/updates/ws` inbound messages rather than overloading the existing relay-to-client update envelope further.
- Extend the agent websocket protocol to carry structured forwarded input events in addition to the current raw `input` message during the migration window.
- Treat unsupported or malformed key events as safely ignored on the PTY-owner side, with logging that avoids recording input text contents.
- Have the relay stamp each output frame with a UTC timestamp when it appends the retained frame, and propagate that same timestamp through replay and live output fanout.
- Remove the standalone `resize` event from the protocol and require agent-uploaded output frames to include `cols` and `rows` directly.
- Preserve the current output, retained-frame, and session-removal contracts exactly unless a protocol field must change for structured input support and frame timestamps.

## Open Questions

### Resolved During Planning

- Where should special-key mapping live: in the relay or in `agentunnel`? It should live in `agentunnel`.
- Should this plan add `session_state` or richer session semantics? No; this change is limited to structured input.
- Should raw `input` support disappear immediately? No; keep it temporarily for compatibility while new clients and `agentunnel` roll out.
- Should ordinary characters like `"c"` use `input_key`? No; plain text, pasted text, and IME-committed text should use `input_text`.
- Where should frame timestamps come from? The relay should assign them when it records each output frame so replay and live fanout can share one authoritative value.
- Should the protocol keep a separate resize event? No; every output frame should carry `cols` and `rows`, and standalone resize should be removed.

### Deferred to Implementation

- The exact byte sequence for navigation keys such as `HOME`, `END`, and `PAGE_UP` may need light validation against the active PTY environment during implementation; the plan fixes the ownership boundary, not every terminal-mode nuance.
- The exact migration-removal point for legacy raw `input` can be decided after both the mobile client and `agentunnel` support structured input in production-like testing.

## High-Level Technical Design

> *This illustrates the intended approach and is directional guidance for review, not implementation specification. The implementing agent should treat it as context, not code to reproduce.*

```text
client `/api/updates/ws`
  -> send `input_text{text}` or `input_key{key, modifiers}`
  -> relay validates session + payload shape
  -> relay forwards structured input over `/agent/ws`
  -> connector receives forwarded event
  -> agent-side input translator maps:
       - `input_text` -> UTF-8 bytes
       - `input_key` -> PTY bytes for supported keys
  -> `session.Hub.WriteInput(...)`
  -> PTY stdin
  -> PTY output returns through existing output/frames path
  -> agent uploads each output frame with `cols` + `rows`
  -> relay appends retained frame with `seq` + `ts`
  -> relay reuses that `ts` for live output websocket fanout
```

## Implementation Units

- [x] **Unit 1: Define structured input protocol types**

**Goal:** Add explicit wire types for structured client input and forwarded agent input without disturbing the existing output and replay contracts.

**Requirements:** R1, R2, R3, R5, R7, R8

**Dependencies:** None

**Files:**
- Modify: `protocol/message.go`
- Modify: `protocol/relay_types_test.go`
- Modify: `docs/protocol.md`

**Approach:**
- Introduce a dedicated client-input envelope for `/api/updates/ws` inbound traffic with `session_id`, `type`, and the structured input fields needed by `input_text` and `input_key`.
- Extend the agent websocket message model so `/agent/ws` can carry forwarded `input_text` and `input_key` events in addition to existing `register`, `output`, and legacy raw `input`.
- Extend retained frame and relay-to-client output contracts with a timestamp field, keeping field naming and semantics aligned between replay and live fanout.
- Update the agent-uploaded `output` contract so every frame includes `cols` and `rows`, removing the need for a standalone resize message.
- Define a fixed symbolic key vocabulary for the first release and document that plain text belongs on `input_text`, not `input_key`.

**Patterns to follow:**
- `protocol/message.go` field naming and JSON tagging conventions
- `protocol/relay_types_test.go` round-trip JSON tests for protocol stability

**Test scenarios:**
- Happy path: `input_text` JSON round-trips with `session_id`, `type`, and `text` preserved exactly.
- Happy path: `input_key` JSON round-trips with `key`, `ctrl`, `alt`, and `shift` preserved exactly.
- Happy path: agent-uploaded `output` JSON round-trips with `data`, `cols`, and `rows`.
- Happy path: retained output frame JSON round-trips with `seq`, `data_b64`, `cols`, `rows`, and `ts`.
- Edge case: omitted optional modifier flags decode to the documented default behavior.
- Edge case: relay-to-client `output` JSON adds `ts` without changing existing `session_id`, `type`, `seq`, `data`, `cols`, or `rows` semantics.
- Integration: legacy raw `input` JSON still round-trips on the agent websocket during the migration window.

**Verification:**
- Shared protocol types can represent the new structured input events and the existing output/update events without field-name drift or ambiguous JSON contracts.

- [x] **Unit 2: Accept and forward structured input through the relay**

**Goal:** Teach the relay to ingest `input_text` and `input_key` from `/api/updates/ws`, validate them, and forward them to the owning agent session.

**Requirements:** R1, R2, R3, R4, R5, R7, R8

**Dependencies:** Unit 1

**Files:**
- Modify: `relay/server.go`
- Modify: `relay/history.go`
- Modify: `relay/registry.go`
- Modify: `relay/server_test.go`
- Modify: `relay/registry_test.go`

**Approach:**
- Parse inbound client websocket messages using the new client-input envelope rather than the relay-to-client update envelope.
- Preserve the existing session lookup and owner routing through `Registry`, but add forwarding paths for structured text and structured key events in addition to the current raw byte path.
- Replace relay size tracking from standalone resize messages with per-output-frame `cols` and `rows` supplied by the agent.
- When output is appended, stamp the retained frame once in the relay and reuse that timestamp in both `/api/sessions/:id/frames` responses and live `output` websocket events.
- Keep invalid credentials, unknown sessions, and malformed payloads on the current safe-failure posture: reject unauthorized access, return `404` for missing sessions on HTTP replay, and ignore or drop malformed websocket input without corrupting session state.
- Ensure relay logging records event type and session identity only; do not log `input_text.text` or synthesized PTY bytes.

**Patterns to follow:**
- `relay/server.go` auth and websocket handling shape
- `relay/registry.go` owner-validated input routing
- `relay/server_test.go` websocket forwarding tests

**Test scenarios:**
- Happy path: a client sends `input_text` over `/api/updates/ws` and the owning agent websocket receives the forwarded structured text event.
- Happy path: a client sends `input_key` for `TAB` and the owning agent websocket receives the same symbolic key event.
- Happy path: an output chunk stored by the relay is replayed from `/api/sessions/:id/frames` with a non-empty `ts`, and the corresponding live `output` event carries the same `ts`.
- Happy path: an agent-uploaded output frame with `cols=132` and `rows=43` is replayed and fanned out with the same size metadata.
- Edge case: a structured input event for an unknown `session_id` does not reach any live agent session.
- Edge case: malformed `input_key` payloads are ignored without breaking the client websocket loop.
- Error path: unauthenticated `/api/updates/ws` access still returns `401`.
- Integration: legacy raw `input` forwarding continues to work while structured input support is present, and no separate resize event is required to populate frame size metadata.

**Verification:**
- Relay transport behavior remains output-opaque while structured input events are routed to exactly one live owner session.

- [x] **Unit 3: Translate structured input into PTY bytes inside `agentunnel`**

**Goal:** Convert forwarded `input_text` and supported `input_key` events into the raw bytes expected by the local PTY.

**Requirements:** R1, R2, R3, R5, R6, R8

**Dependencies:** Unit 1, Unit 2

**Files:**
- Modify: `connector/connector.go`
- Modify: `connector/connector_test.go`
- Create: `session/remote_input.go`
- Create: `session/remote_input_test.go`

**Approach:**
- Add a small PTY-owner-side input translation helper under `session/` so key mapping stays near PTY concerns rather than inside the relay layer.
- Route connector-received `input_text` through that helper into UTF-8 bytes and then to `session.Hub.WriteInput(...)`.
- Route connector-received `input_key` through a supported-key mapper that covers the v1 key set: navigation keys, `ENTER`, `BACKSPACE`, `TAB`, `ESCAPE`, `DELETE`, and `Ctrl+A-Z`.
- Remove standalone resize uploads from the connector path and ensure each uploaded output frame carries the current terminal `cols` and `rows`.
- Continue to accept legacy raw `input` in the connector during the migration window so existing clients keep working.
- Treat unsupported symbolic keys as ignored inputs with non-sensitive logging instead of fallback byte invention.

**Execution note:** Start with agent-side characterization tests for the translated bytes before wiring the connector changes, because this unit defines the compatibility contract between symbolic keys and PTY input.

**Patterns to follow:**
- `connector/connector.go` as the relay-to-local-protocol boundary
- `session/hub.go` as the single path for PTY stdin writes
- `session/local_terminal.go` and `session/hub.go` as the current sources of terminal size state

**Test scenarios:**
- Happy path: `input_text` `"hello"` becomes the expected byte sequence written into `session.Hub`.
- Happy path: `input_key` `TAB` becomes a tab byte and reaches `session.Hub`.
- Happy path: `input_key` `ENTER` becomes carriage return and reaches `session.Hub`.
- Happy path: `input_key` `ctrl=true, key="C"` becomes ETX and reaches `session.Hub`.
- Happy path: `input_key` `UP` becomes the expected PTY navigation sequence and reaches `session.Hub`.
- Happy path: an uploaded output frame includes the current `cols` and `rows` without requiring a prior resize message.
- Edge case: lowercase or text-style character input continues to use `input_text` and does not require the key mapper.
- Edge case: unsupported symbolic keys are ignored without writing bytes into the hub.
- Integration: legacy raw `input` still reaches the hub unchanged while structured input support is enabled.

**Verification:**
- `agentunnel` can accept forwarded structured input and reliably convert the supported key set into PTY stdin writes without relay-side byte synthesis.

- [x] **Unit 4: Lock the contract with end-to-end tests and docs**

**Goal:** Update the repo documentation and end-to-end contract tests so the new input path is clear and stable for future clients.

**Requirements:** R1, R2, R4, R5, R6, R7, R8

**Dependencies:** Unit 1, Unit 2, Unit 3

**Files:**
- Modify: `relay/server_test.go`
- Modify: `connector/connector_test.go`
- Modify: `docs/protocol.md`
- Modify: `docs/architecture.md`
- Modify: `README.md`

**Approach:**
- Extend existing relay and connector tests so the repository proves the full path from client structured input to agent-side PTY write intent.
- Update protocol docs to distinguish:
  - client-to-relay structured input
  - relay-to-agent forwarded structured input
  - relay-to-client output updates
- Document that output frames include a relay-assigned UTC timestamp available in both replay and live output messages.
- Document that size metadata is carried on every output frame and that the protocol no longer defines a standalone resize event.
- Update architecture docs to explicitly state that key-to-byte mapping is owned by `agentunnel` as the PTY owner.
- Update operator-facing docs only where the externally visible protocol changed.

**Patterns to follow:**
- Current documentation alignment rules in `CLAUDE.md`
- Existing relay contract tests in `relay/server_test.go`

**Test scenarios:**
- Happy path: a structured `TAB` input sent over the client websocket reaches the agent-side translator and results in the expected PTY write.
- Happy path: a structured `input_text` event round-trips through the documented path without affecting output replay behavior.
- Edge case: retained output frames still return the same inclusive range semantics after the input protocol changes, with `ts` present on every returned frame.
- Integration: relay output updates, session removal, and structured input forwarding coexist on the same live system without event-type collision.
- Integration: a client can display matching timestamps whether it consumes live output directly or reconstructs the same output from retained frames.
- Integration: a client can derive terminal size entirely from output frames without observing any standalone resize event.

**Verification:**
- The docs, tests, and code all describe the same transport boundary: relay forwards structured input, `agentunnel` performs PTY mapping, and retained output behavior stays intact.

## System-Wide Impact

- **Interaction graph:** This work touches all transport seams between external clients, relay routing, the agent websocket, and local PTY input writes.
- **Error propagation:** Input-shape errors should stop at the websocket boundary; they should not mutate relay session state or close the agent session unless the underlying socket itself fails.
- **State lifecycle risks:** Mixed support for legacy raw `input` and new structured input can create drift if one path is updated without the other; replay and live output timestamps must also stay sourced from the same retained-frame append event.
- **API surface parity:** `/api/updates/ws` client input and `/agent/ws` forwarded input must evolve together; mismatched message types would break remote control.
- **Integration coverage:** Unit tests alone are not enough; relay forwarding and connector translation both need cross-layer tests.
- **Unchanged invariants:** Output seq assignment, retained frame replay semantics, and `session_removed` events remain unchanged apart from adding `ts` metadata to output frames and sourcing size from each output frame instead of a standalone resize stream.

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| Ambiguous ownership could pull key mapping into the relay again during implementation | State clearly in code and docs that the PTY owner translates symbolic keys to bytes |
| Terminal navigation keys differ from plain control characters and may drift from expected PTY behavior | Add focused mapping tests and keep the mapper isolated so byte sequences can be corrected without touching relay logic |
| Structured and legacy input paths diverge during rollout | Keep both paths covered by tests until legacy raw input removal is explicitly scheduled |
| Live output and replay could expose different timestamps for the same frame if they are generated independently | Generate the timestamp once at retained-frame append time and reuse it for both replay and live output fanout |
| Output frames could carry stale size metadata if terminal size state is not sampled correctly at upload time | Make output upload consume current hub size directly and add tests that prove size changes appear on subsequent output frames |
| Input logging could capture sensitive typed text | Restrict logs to event metadata and avoid logging text payloads or mapped bytes |

## Documentation / Operational Notes

- `docs/protocol.md` and `docs/architecture.md` must change together because the protocol and the ownership boundary are both part of the public repo contract.
- `README.md` only needs the externally visible client-input behavior and should not grow terminal-mapping detail that belongs in protocol docs.
- Output timestamp semantics should be documented once and reused consistently: relay-assigned UTC timestamp per output frame.
- If the Android client depends on the final symbolic key list, keep the documented key vocabulary stable once this plan is implemented.

## Sources & References

- Related code: `protocol/message.go`
- Related code: `relay/server.go`
- Related code: `relay/registry.go`
- Related code: `connector/connector.go`
- Related code: `session/hub.go`
- Related code: `docs/protocol.md`
- Related code: `docs/architecture.md`
