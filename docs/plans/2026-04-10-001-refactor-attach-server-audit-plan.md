---
title: refactor: Audit and simplify attach server implementation
type: refactor
status: completed
date: 2026-04-10
origin: docs/brainstorms/2026-04-09-session-attach-terminal-mirror-requirements.md
---

# refactor: Audit and simplify attach server implementation

## Overview

Audit the current attach-only server implementation against the settled attach contract and the newer end-to-end explanation in `docs/tui-attach-flow.md`. The work is intentionally bounded to three outcomes:

- remove legacy or dead internal surfaces that no longer serve the attach-only product
- simplify redundant server-side abstractions around input, resize, and websocket control handling
- add missing corner-case coverage before cleanup changes land

This is not a protocol redesign. The public attach contract is already in place and the current suite is green. The goal is to make the implementation smaller, clearer, and more explicitly pinned to the documented lifecycle.

## Problem Frame

The attach migration is largely complete, but the codebase still carries some transitional weight and unpinned lifecycle details.

Current state from repo research:

- Production replay/history surfaces appear removed, but some internal wrappers still look transitional rather than essential.
- `protocol/message.go` now contains overlapping input/control shapes (`Message`, `AgentFrame`, `ClientInputMessage`) even though the active production path is narrower than that type surface suggests.
- `relay/registry.go` still exposes both `WriteInput` and `WriteAttachInput`, although attach-scoped input is now the only live client path in production.
- `relay/server.go` uses `readWSJSON()` for the initial agent register frame and client attach input, and that helper does not enforce websocket text-message type. That is looser than the documented "JSON text frames only" contract in `docs/protocol.md`.
- `relay/server.go` defines `wsAgentPeer.SendBinary` and `relay.AgentPeer` requires `SendBinary`, but the current relay-to-agent production path appears to use JSON only.
- `session/terminal_mirror_test.go` covers plain text, alternate buffer, scrollback exclusion, and resize, but does not yet pin several fidelity claims already documented or implied by the origin requirements: styled output, wide glyphs, cursor-hidden state, and empty-screen attach behavior.
- End-to-end attach tests are strong on the happy path, but they do not yet characterize several lifecycle edges that the new doc makes explicit: pending attach loss before `snapshot_done`, malformed control traffic, and message-type validation at websocket boundaries.

`go test ./...` passes as of this planning pass on 2026-04-10, which makes characterization-first cleanup feasible.

## Requirements Trace

- R1. Keep the current attach-only public contract unchanged: `GET /api/sessions`, `GET /api/sessions/:id/attach/ws`, `/agent/ws`, snapshot plus live bytes, and reconnect-via-fresh-attach behavior (origin R1-R24; see `docs/protocol.md` and `docs/tui-attach-flow.md`).
- R2. Remove or rename server-side legacy surfaces that no longer provide product value or are only kept alive by test scaffolding.
- R3. Simplify redundant internal abstractions when one ownership path is sufficient, especially around input forwarding, resize subscriptions, and websocket control handling.
- R4. Add characterization coverage for the documented corner cases around snapshot handoff, reconnect, control-frame validation, and terminal mirror fidelity before behavior changes land.
- R5. Keep this work bounded to server-side and shared-protocol implementation. No client feature expansion, no protocol expansion, and no new delivery guarantees.

## Scope Boundaries

- No new attach semantics or protocol versioning.
- No replay/history/durable delivery work.
- No mobile/client implementation work.
- No local-terminal size ownership changes.
- No redesign of relay versus connector responsibilities.

## Context & Research

### Relevant Code and Patterns

- `connector/connector.go` owns the agent-side snapshot handoff, live-byte forwarding, activity publication, and reconnect state. It is the right place to pin no-gap attach behavior and malformed inbound-control handling.
- `relay/server.go` owns both websocket entrypoints and the HTTP pre-upgrade contract. It is the right place to tighten message-type validation and attach-start failure behavior.
- `relay/registry.go` owns live session state, attach membership, reconnect lifecycle, and owner/client routing. It is the right place to collapse dead input wrappers and harden pending-attach cleanup behavior.
- `protocol/message.go` is the current type-surface hotspot for redundant input/control envelopes.
- `session/hub.go` already supports keyed resize listeners via `AddResizeListener`; `OnResize` is now a compatibility wrapper over that newer path.
- `session/terminal_mirror.go` is the fidelity boundary. Its current tests are the right foundation, but they do not yet cover all documented TUI-state expectations.
- Existing package tests already pin the happy path well:
  - `connector/connector_test.go`
  - `relay/server_test.go`
  - `relay/registry_test.go`
  - `protocol/relay_types_test.go`
  - `session/terminal_mirror_test.go`

### Institutional Learnings

- No `docs/solutions/` entries exist in this repository.

### External References

- None. This plan is grounded in current repo code plus:
  - `docs/brainstorms/2026-04-09-session-attach-terminal-mirror-requirements.md`
  - `docs/protocol.md`
  - `docs/architecture.md`
  - `docs/tui-attach-flow.md`

### Audit Findings To Carry Forward

- `relay/ws_logging.go` reads and unmarshals websocket payloads without checking message type, so binary JSON would currently be accepted where the docs say JSON text frames only.
- `relay.AgentPeer.SendBinary` and `wsAgentPeer.SendBinary` look production-dead in the current attach model.
- `protocol.Message`, `EncodeInputText`, `EncodeInputKey`, `ClientInputMessage.AgentMessage`, and `relay.Registry.WriteInput` are now used primarily by tests and transitional wrappers rather than the active attach pipeline.
- `session/terminal_mirror_test.go` is still missing coverage for some of the exact fidelity properties the requirements document called out during planning.
- There is no end-to-end test today that proves cleanup of a pending attach when the client disappears before `attach_ready` / `snapshot_done`.

## Key Technical Decisions

- Treat this as a characterization-first cleanup.
  Rationale: the public attach contract is already the source of truth. The main risk is accidentally removing a still-relied-on wrapper or silently changing lifecycle behavior during simplification.

- Use `docs/tui-attach-flow.md` as the audit lens, while keeping the 2026-04-09 requirements document as the formal origin.
  Rationale: the requirements document defines the contract boundary; the newer flow doc makes the attach lifecycle and reconnect expectations easier to map onto code and tests.

- Prefer deleting dead wrappers over adding new abstraction layers around them.
  Rationale: this repo is small enough that the cleanest steady state is fewer envelopes and fewer owner-send entrypoints, not more adapter code.

- Tighten protocol-boundary validation where the implementation is looser than the documented contract.
  Rationale: allowing binary JSON to act as control input is the kind of permissive edge behavior that drifts away from the docs unless a test pins it explicitly.

## Open Questions

### Resolved During Planning

- Should this work change the external attach protocol? No. Keep the current public contract and audit the implementation against it.
- Does this plan need external research? No. The uncertainty is repo-local and contract-local, not framework-local.
- Should Unit 1 include startup-config compatibility cleanup outside the attach flow? No. Keep this audit focused on attach, relay, connector, protocol, and mirror behavior.

### Deferred to Implementation

- Whether `protocol.Message` should disappear entirely or survive as a tiny internal helper depends on how much duplication remains once `Registry.WriteInput` is removed.
- Whether `session.Hub.OnResize` should be deleted or retained as a stable alias depends on how disruptive that rename is across tests and local-only call sites.
- Whether websocket write-path deduplication between `wsAgentPeer` and `wsAttachPeer` is worth the extra abstraction should be decided only after dead-surface cleanup reduces noise.

## Implementation Units

- [x] **Unit 1: Characterize and remove dead or legacy attach surfaces**

**Goal:** Prune wrappers, compatibility artifacts, and stale internal interfaces that no longer describe the active attach-only product.

**Requirements:** R1, R2, R5

**Dependencies:** None

**Files:**
- Modify: `protocol/message.go`
- Modify: `relay/registry.go`
- Modify: `relay/server.go`
- Modify: `session/hub.go`
- Test: `protocol/relay_types_test.go`
- Test: `relay/registry_test.go`

**Approach:**
- Confirm which current protocol/input helpers are truly production-dead versus merely under-tested.
- Remove or inline dead surfaces such as owner-send wrappers or unused interface methods where no live production call path remains.
- Keep cleanup scoped to active attach/server semantics rather than unrelated CLI ergonomics.

**Execution note:** Characterization-first. Update or add tests that prove the active production call graph before deleting wrappers that are currently exercised mainly through test scaffolding.

**Patterns to follow:**
- Minimal helper-constructor style already used in `protocol/message.go`
- Keyed listener pattern already used by `connector/connector.go`

**Test scenarios:**
- Happy path: the active attach input path still routes through the surviving owner-send entrypoint after dead-wrapper cleanup.
- Regression: no production interface still requires relay-to-agent binary send support unless an active production caller exists.
- Regression: `go test ./...` remains green after dead-surface pruning.

**Verification:**
- The production server-side call graph reflects the attach-only model directly, without extra wrappers that exist only to support tests or transitional naming.

- [x] **Unit 2: Simplify server-side attach, input, and resize paths without changing the contract**

**Goal:** Collapse redundant ownership paths so one code path exists for control-frame decoding, input forwarding, and resize observation.

**Requirements:** R1, R3, R5

**Dependencies:** Unit 1

**Files:**
- Modify: `relay/server.go`
- Modify: `relay/ws_logging.go`
- Modify: `relay/attach_client_ws.go`
- Modify: `connector/connector.go`
- Modify: `session/hub.go`
- Test: `relay/server_test.go`
- Test: `connector/connector_test.go`
- Test: `session/hub_test.go`

**Approach:**
- Tighten websocket boundary handling so control messages follow the documented text-frame contract rather than being accepted solely because their bytes decode as JSON.
- Simplify internal input routing where multiple helpers now describe the same attach-scoped path.
- Standardize on one resize-listener API and update local call sites accordingly.
- Only extract shared websocket write helpers if doing so makes tracker behavior and close-reason behavior clearer, not more abstract.

**Execution note:** Start with failing protocol-boundary tests for the message-type and attach-lifecycle cases, then simplify internals under that protection.

**Patterns to follow:**
- Current attach lifecycle in `relay/server.go` and `relay/registry.go`
- Submit-gap handling in `connector/connector.go`

**Test scenarios:**
- Happy path: text websocket frames carrying `input_text` and `input_key` still route correctly after simplification.
- Error path: binary websocket frames presented where JSON text frames are required are rejected or ignored according to the chosen contract, without silently becoming valid control input.
- Edge case: attach-start failure after websocket upgrade still yields a deterministic close reason and does not leave stale pending attach state behind.
- Edge case: disconnect or `attach_close` before `snapshot_done` does not leak attach membership or permit later live-byte delivery.
- Regression: resize notifications still reach the status line, connector, and attached clients after listener API cleanup.

**Verification:**
- There is one clearly owned server-side path for control-frame decoding, one for attach input forwarding, and one for resize observation.

- [x] **Unit 3: Fill contract and fidelity test gaps around attach lifecycle and terminal state**

**Goal:** Pin the documented corner cases that the current suite does not fully exercise.

**Requirements:** R1, R4, R5

**Dependencies:** Unit 1

**Files:**
- Test: `session/terminal_mirror_test.go`
- Test: `connector/connector_test.go`
- Test: `relay/server_test.go`
- Test: `relay/registry_test.go`
- Modify: `session/terminal_mirror.go`
- Modify: `connector/connector.go`
- Modify: `relay/server.go`
- Modify: `relay/registry.go`

**Approach:**
- Add the missing mirror fidelity cases already called out by the origin requirements: styled text, wide glyphs, cursor-hidden state, and empty-screen snapshot behavior.
- Add lifecycle cases around pending attach loss, malformed inbound control traffic, and attach-start races that are currently only implied by implementation.
- Use those tests to decide whether small contract-tightening fixes are needed in relay/connector/registry code.

**Execution note:** Test-first. Add the missing lifecycle and fidelity cases before behavior cleanup so the implementation is pinned to the current documented contract.

**Patterns to follow:**
- Existing round-trip tests in `session/terminal_mirror_test.go`
- End-to-end attach tests in `relay/server_test.go`
- Transport race coverage style in `connector/connector_test.go`

**Test scenarios:**
- Happy path: an empty current screen still produces a valid attach sequence of `attached` then `snapshot_done`, and later live bytes still render correctly.
- Happy path: truecolor and style attributes survive snapshot round-trip into a fresh headless terminal.
- Happy path: wide characters and hidden cursor state survive snapshot round-trip.
- Edge case: malformed or unknown agent control frames are ignored without breaking a healthy connection.
- Edge case: malformed or wrong-type client control frames do not become accepted attach input.
- Edge case: client disconnect before `attach_ready` or before `snapshot_done` leaves no stale pending/attached state and no later live-byte fanout.
- Edge case: attach-start races against a session transition to `reconnecting` produce the documented close/error outcome.
- Regression: `go test ./...` remains green while the new cases pin the stricter attach contract.

**Verification:**
- The suite explicitly covers the attach lifecycle and fidelity edges described in `docs/tui-attach-flow.md`, rather than relying on incidental behavior.

## System-Wide Impact

- **Interaction graph:** `session.Hub` feeds both local terminal and connector; `connector` and `relay/server.go` share responsibility for attach boundaries; `relay/registry.go` is the single source of live session and attach membership.
- **Error propagation:** websocket boundary validation errors must not silently turn into accepted control input; attach-start failures must surface deterministic close reasons.
- **State lifecycle risks:** pending attach cleanup, slow-client detaches, and reconnect transitions can leak or duplicate state if cleanup remains split across too many wrappers.
- **API surface parity:** `docs/protocol.md` and `docs/tui-attach-flow.md` already define the contract; internal cleanup should converge on those docs rather than create a second interpretation.
- **Integration coverage:** the most valuable tests cross package boundaries: attach open -> snapshot -> snapshot_done -> live bytes -> detach/reconnect.
- **Unchanged invariants:** stable `session_id` across relay reconnects, relay content opacity, and current-screen-only recovery all remain unchanged.

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| Removing a wrapper that is only indirectly used | Characterize production call sites first and keep cleanup staged through green package tests |
| Tightening control-frame validation breaks an untested but currently tolerated path | Add explicit server tests for text-vs-binary control messages before changing helper behavior |
| Simplifying input/resize paths obscures the no-gap attach invariant | Keep attach lifecycle tests end to end in connector and relay packages while refactoring |
| Expanded mirror-fidelity tests expose a real `xterm-go` divergence | Treat the tests as characterization and fix behind the existing mirror abstraction rather than widening the protocol |

## Documentation / Operational Notes

- If Unit 2 changes any user-visible close reason or control-frame acceptance rule, update `docs/protocol.md` and `docs/tui-attach-flow.md` in the same change.
- If cleanup only removes dead internal surfaces, docs should remain stable and the final review should confirm that explicitly.

## Sources & References

- **Origin document:** `docs/brainstorms/2026-04-09-session-attach-terminal-mirror-requirements.md`
- Related docs: `docs/protocol.md`
- Related docs: `docs/architecture.md`
- Related docs: `docs/tui-attach-flow.md`
- Related code: `connector/connector.go`
- Related code: `relay/server.go`
- Related code: `relay/registry.go`
- Related code: `protocol/message.go`
- Related code: `session/terminal_mirror.go`
