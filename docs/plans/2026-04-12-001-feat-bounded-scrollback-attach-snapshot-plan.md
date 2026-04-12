---
title: feat: Add bounded scrollback to attach snapshots
type: feat
status: completed
date: 2026-04-12
origin: docs/brainstorms/2026-04-09-session-attach-terminal-mirror-requirements.md
---

# feat: Add bounded scrollback to attach snapshots

## Overview

Allow the agent-side `xterm-go` mirror to retain a bounded amount of normal-buffer scrollback and include a bounded slice of that scrollback in attach snapshots. This gives newly attached or reconnecting clients more recent terminal context without reintroducing relay-side replay APIs, durable history, or protocol changes.

The change is intentionally narrower than "bring history back." It only affects the agent-owned in-memory mirror and the initial snapshot payload. The attach lifecycle, relay responsibilities, and post-`snapshot_done` live-byte stream remain unchanged.

## Problem Frame

The current implementation hard-disables history in two places:

- `internal/tunnel/session/terminal_mirror.go` constructs `xterm-go` with `xterm.WithScrollback(0)`.
- `internal/tunnel/session/terminal_mirror.go` serializes snapshots with `SerializeOptions{Scrollback: &0}`.

That means attach snapshots currently restore only the visible viewport, even though the pinned `github.com/gitpod-io/xterm-go` dependency supports both:

- retaining normal-buffer scrollback in memory
- serializing some or all of that scrollback into snapshot bytes

The user request is to let attach snapshots carry more recent history. Technically, `xterm-go` can do that. The real work is deciding the product boundary cleanly:

- keep history bounded and agent-local
- preserve the existing attach framing and no-gap handoff
- avoid implying relay-side replay or durable transcript recovery
- document that alternate-screen TUIs still do not gain synthetic history, because the alt buffer itself has no scrollback

This plan intentionally revises the earlier "current visible screen only" boundary from the 2026-04-09 origin work. The new contract remains live-only and non-durable, but it broadens snapshot recovery from viewport-only to bounded agent-local scrollback when the normal buffer has that history available.

This last constraint matters. Increasing normal-buffer scrollback helps shells and normal-buffer output immediately. While an app is actively running on the alternate screen, the visible attach state still lands on the current alt buffer. The alt buffer itself still has no scrollback, but `xterm-go` serializes the underlying normal buffer before switching back into the alt buffer, so bounded normal-buffer history can still be preserved underneath the current alt-screen view.

## Requirements Trace

- R1. The agent-side mirror must retain a bounded positive amount of normal-buffer scrollback instead of zero lines.
- R2. The initial attach snapshot must include a bounded amount of recent normal-buffer history when that history exists, while preserving the current `attached -> snapshot bytes -> snapshot_done -> live bytes` contract.
- R3. The relay must remain content-opaque and live-only. This work must not add replay endpoints, durable storage, or transcript semantics back to the relay.
- R4. Alternate-buffer behavior must remain honest to xterm semantics: active alternate-screen sessions still restore the current alt-buffer state, and any preserved history must come from the underlying normal buffer rather than synthetic alt-buffer replay.
- R5. Repository docs and tests must align on the new contract: bounded in-memory scrollback is available in snapshots, but durable history and replay APIs remain out of scope.

## Scope Boundaries

- No relay-side history retention, replay cache, or `/api/sessions/:id/frames`-style return.
- No protocol version bump and no new attach control messages.
- No attempt to synthesize alternate-screen history that the terminal engine does not keep.
- No user-facing CLI flag or environment variable in the first pass.
- No changes to PTY size ownership or structured input semantics.

## Context & Research

### Relevant Code and Patterns

- `internal/tunnel/session/terminal_mirror.go` is the single ownership point for mirror construction, resize handling, and snapshot serialization.
- `internal/tunnel/session/terminal_mirror_test.go` already uses round-trip snapshot restoration as the fidelity test pattern. That is the right pattern to extend for scrollback-bearing snapshots.
- `internal/tunnel/connector/connector.go` treats snapshot bytes as opaque terminal bytes and already preserves the attach lifecycle ordering. That path should stay structurally unchanged.
- `internal/tunnel/connector/connector_test.go` already restores attach snapshots into a fresh `xterm-go` terminal and verifies the `snapshot_done` boundary. It is the right integration seam for scrollback assertions.
- `docs/tui-attach-flow.md`, `docs/protocol.md`, `docs/api.md`, `docs/architecture.md`, `README.md`, `CLAUDE.md`, and `AGENTS.md` currently describe snapshot recovery as current-screen-only and must move together if the contract changes.
- `docs/plans/2026-04-09-002-feat-session-attach-terminal-mirror-plan.md` is the origin implementation plan for the current attach model and explicitly chose "no scrollback history" as the initial boundary.

### Institutional Learnings

- No `docs/solutions/` entries exist in this repository.

### External References

- None. Local repo patterns plus the pinned `xterm-go` dependency source are sufficient for this plan.

## Key Technical Decisions

- Treat this as a bounded snapshot enhancement, not a replay/history comeback.
  Rationale: the product can expose more recent context at attach time without changing relay ownership or reintroducing transcript APIs.

- Separate mirror retention from snapshot export as distinct internal knobs.
  Rationale: `xterm-go` has one control for how much normal-buffer scrollback the terminal retains and another for how much scrollback serialization emits. Keeping them conceptually separate avoids conflating memory cost with attach payload size.

- Keep the first pass internal-only rather than adding a public config surface.
  Rationale: the request is about product behavior, not operator tuning. A hardcoded bounded default keeps the change small and avoids introducing new environment or CLI contracts before external clients have validated the new snapshot size envelope.

- Preserve alternate-screen semantics exactly as `xterm-go` defines them.
  Rationale: `xterm-go`'s `BufferSet` gives the alternate buffer no scrollback. The plan should document that reality rather than promise history for full-screen TUIs that the engine cannot produce.

- Keep attach framing and websocket packet formats unchanged.
  Rationale: more history should arrive as more terminal bytes inside the existing snapshot phase, not as a new message type or side channel.

## Open Questions

### Resolved During Planning

- Can `xterm-go` retain and serialize more history than the current repo uses? Yes. The pinned dependency supports `WithScrollback(n)` for retention and `SerializeOptions.Scrollback` for bounded serialization.
- Does this require relay or protocol changes? No. The existing snapshot byte stream can already carry more terminal history.
- Will this produce history for active alternate-screen TUIs? Partially. The alternate buffer does not have its own scrollback, but `xterm-go` preserves bounded normal-buffer history underneath the current alt-screen state.
- Should this first pass add a new environment variable or CLI flag? No. Keep the first implementation internal and bounded.

### Deferred to Implementation

- Resolved during implementation: mirror retention and snapshot export both use an internal default of 256 lines.

## High-Level Technical Design

> *This illustrates the intended approach and is directional guidance for review, not implementation specification. The implementing agent should treat it as context, not code to reproduce.*

| Terminal state | Mirror retention | Snapshot output | Notes |
|---|---|---|---|
| Normal buffer active with recent scrollback | Retain last `N` lines in the agent mirror | Emit last `M` lines plus the current viewport, where `M` is bounded and `M <= N` | Improves attach context without changing protocol shape |
| Alternate buffer active | Keep the current alt-buffer state and the bounded underlying normal-buffer history | Restore the bounded normal buffer first, then switch into the current alt buffer | No synthetic alt-buffer scrollback; the visible screen still lands on the alt buffer |
| Reconnect after missed output | Build a fresh snapshot from current mirror state | Same attach lifecycle as today | Still not replay of every missed PTY byte |

## Implementation Units

- [x] **Unit 1: Add bounded scrollback retention and serialization to the terminal mirror**

**Goal:** Teach `TerminalMirror` to keep a bounded normal-buffer history and serialize a bounded slice of that history into snapshot bytes.

**Requirements:** R1, R2, R4

**Dependencies:** None

**Files:**
- Modify: `internal/tunnel/session/terminal_mirror.go`
- Test: `internal/tunnel/session/terminal_mirror_test.go`

**Approach:**
- Replace the current zero-scrollback construction with explicit internal defaults for normal-buffer retention.
- Replace the hardcoded `SerializeOptions{Scrollback: &0}` path with a bounded positive export limit.
- Keep `Snapshot()` atomic under the existing mutex so the no-gap handoff assumptions in the connector stay intact.
- Keep the public shape of `Snapshot()` unchanged unless implementation discovers that exposing scrollback metadata materially simplifies tests or future tuning.

**Execution note:** Start with mirror-level round-trip tests that pin normal-buffer scrollback restoration before touching connector behavior.

**Patterns to follow:**
- Snapshot round-trip pattern already used in `internal/tunnel/session/terminal_mirror_test.go`
- Small constructor/constants style already used in `internal/tunnel/session/terminal_mirror.go`

**Test scenarios:**
- Happy path: writing more normal-buffer lines than the viewport retains recent scrollback, and a fresh terminal restored from the snapshot can scroll back to those retained lines.
- Happy path: the current visible viewport remains identical after the snapshot round-trip even when additional scrollback is present.
- Edge case: the snapshot includes only the newest bounded history window, not unbounded normal-buffer output.
- Edge case: an active alternate-buffer snapshot still restores the current alt screen, while preserving bounded underlying normal-buffer history without inventing alt-buffer scrollback.
- Integration: styled text and wide characters still survive snapshot round-trip when scrollback is enabled.

**Verification:**
- A round-tripped snapshot preserves the current viewport plus a bounded recent normal-buffer history window, and alternate-buffer restores remain faithful to xterm semantics.

- [x] **Unit 2: Expand attach integration coverage for scrollback-bearing snapshots**

**Goal:** Prove that larger snapshots still preserve the current attach lifecycle and live-byte handoff.

**Requirements:** R2, R3, R4

**Dependencies:** Unit 1

**Files:**
- Modify: `internal/tunnel/connector/connector.go`
- Test: `internal/tunnel/connector/connector_test.go`

**Approach:**
- Keep `handleAttachOpen()` on the same control path: snapshot capture, `attach_ready`, snapshot bytes, `snapshot_done`.
- Extend connector tests so attach snapshots are restored into a fresh `xterm-go` terminal with non-zero client-side scrollback, then assert both recent history visibility and unchanged live-byte delivery after `snapshot_done`.
- Update any connector comments or small guard code only if the current tests reveal hidden assumptions about zero-scrollback snapshots.

**Patterns to follow:**
- Existing attach-open integration tests in `internal/tunnel/connector/connector_test.go`
- Current connector ownership split where snapshot bytes stay opaque and lifecycle control stays JSON

**Test scenarios:**
- Happy path: an attach to a session with recent normal-buffer history receives snapshot bytes that restore both the current viewport and bounded scrollback lines.
- Happy path: after `snapshot_done`, later PTY output still arrives as live bytes on the same attach with no duplicate delivery from the larger snapshot.
- Edge case: a session with less history than the configured bound still attaches successfully and restores all available history without special-case framing.
- Edge case: an attach taken while the alternate buffer is active still lands on the current alt-buffer state, while any preserved history remains bounded to the underlying normal buffer.
- Integration: two attach clients opening against the same session each receive their own scrollback-bearing snapshot and then the same later live bytes.

**Verification:**
- Connector tests continue to prove the same attach lifecycle ordering, with additional assurance that bounded snapshot history does not corrupt live-byte handoff.

- [x] **Unit 3: Re-document the attach contract as bounded in-memory scrollback, not current-screen-only**

**Goal:** Align repo docs and agent guidance with the new bounded-scrollback snapshot behavior while preserving the no-replay and no-relay-history boundaries.

**Requirements:** R3, R4, R5

**Dependencies:** Unit 2

**Files:**
- Modify: `README.md`
- Modify: `docs/api.md`
- Modify: `docs/protocol.md`
- Modify: `docs/architecture.md`
- Modify: `docs/tui-attach-flow.md`
- Modify: `CLAUDE.md`
- Modify: `AGENTS.md`

**Approach:**
- Replace "current-screen-only" wording with "current screen plus bounded in-memory scrollback when available" everywhere the attach contract is described.
- Keep all anti-goals that still matter: no relay-side history, no replay endpoint, no durable transcript storage, and no client-controlled PTY sizing.
- Add an explicit note that alternate-screen apps still recover their current alt-buffer state, while any bounded preserved history remains in the underlying normal buffer.
- Update client-facing wording so downstream mobile/web terminal emulators understand that the snapshot phase may contain scrollback lines before the current viewport, yet still uses the same byte stream and `snapshot_done` boundary.

**Patterns to follow:**
- Current contract-language consistency across `docs/protocol.md`, `docs/api.md`, and `docs/tui-attach-flow.md`
- Repo-level documentation alignment rules in `CLAUDE.md` and `AGENTS.md`

**Test scenarios:**
- Test expectation: none -- this unit is documentation-only, but the edited docs must agree on bounded in-memory scrollback, unchanged attach framing, and unchanged no-replay boundaries.

**Verification:**
- No repo document still claims that attach snapshots are strictly viewport-only, and no updated document accidentally implies durable history or replay APIs.

## System-Wide Impact

- **Interaction graph:** PTY output still flows into the local terminal, agent mirror, connector snapshot path, relay routing, and client emulator exactly as today. Only the content of snapshot bytes changes.
- **Error propagation:** No new control-message failures are introduced, but larger snapshot payloads can surface existing websocket backpressure or attach-latency limits sooner.
- **State lifecycle risks:** Each live session now carries bounded in-memory scrollback, increasing per-session memory use and attach snapshot size.
- **API surface parity:** The websocket contract shape stays the same, but every contract document and every external client expectation about snapshot contents must move together.
- **Integration coverage:** The highest-value cross-layer test remains attach-open -> snapshot restore -> `snapshot_done` -> live bytes, with both normal-buffer and alt-buffer cases represented.
- **Unchanged invariants:** The relay stays live-only and content-opaque, `snapshot_done` still marks the boundary between snapshot and live bytes, the local terminal still owns PTY size, and reconnect still means "take a fresh snapshot" rather than "replay every missed byte."

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| Larger snapshots materially slow attach or trigger websocket pressure under long-running sessions | Keep snapshot export bounded, add fixture-backed connector tests with multi-line history, and leave the bound internal so it can be tuned without creating a public contract |
| Reviewers or client implementers mistake bounded scrollback for a replay/history API | Update all contract docs together and explicitly distinguish agent-local bounded scrollback from durable or relay-side history |
| Users expect full-screen TUIs on the alternate screen to expose historical UI states | Document the alt-buffer limitation explicitly and pin it with tests so the implementation cannot drift into over-promising |
| External clients configure their own terminal emulator with little or no scrollback, masking the server-side improvement | Call out the client-side expectation in docs so downstream clients preserve replayed snapshot history instead of trimming it immediately |

## Documentation / Operational Notes

- Coordinate the contract update with any external client owners so their terminal emulator configuration keeps enough local scrollback to make the richer snapshot useful.
- When this lands, docs should describe the improvement as bounded in-memory scrollback recovery, not as transcript replay or history sync.

## Sources & References

- **Origin document:** `docs/brainstorms/2026-04-09-session-attach-terminal-mirror-requirements.md`
- Related plan: `docs/plans/2026-04-09-002-feat-session-attach-terminal-mirror-plan.md`
- Related code: `internal/tunnel/session/terminal_mirror.go`
- Related code: `internal/tunnel/session/terminal_mirror_test.go`
- Related code: `internal/tunnel/connector/connector.go`
- Related code: `internal/tunnel/connector/connector_test.go`
- Related docs: `docs/tui-attach-flow.md`
- Related docs: `docs/protocol.md`
- Dependency pin: `go.mod`
