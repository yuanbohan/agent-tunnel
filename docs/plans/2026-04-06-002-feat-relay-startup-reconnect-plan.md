---
title: feat: Relay-aware startup and reconnect UX
type: feat
status: completed
date: 2026-04-06
origin: docs/brainstorms/2026-04-06-relay-startup-reconnect-requirements.md
---

# feat: Relay-aware startup and reconnect UX

## Overview

Align `agentunnel` startup and reconnect behavior with the desired product semantics from `docs/brainstorms/2026-04-06-relay-startup-reconnect-requirements.md`: on launch, give relay registration a bounded chance to succeed first; if that window expires, still enter the local terminal session; once the session is running, never let relay outages interrupt local terminal work; show reconnect state as a compact, low-noise status rather than verbose logs; and reduce the startup banner to the minimum useful confirmation.

## Problem Frame

Today `agentunnel` documents the relay as mandatory, but its actual runtime behavior is softer and less explicit: the local PTY session starts immediately, relay connection happens in the background, and the startup banner does not clearly distinguish "remote access is ready" from "local-only work has started while relay is unavailable." That mismatch is small in code size but important in product meaning.

The desired behavior is intentionally asymmetric:

- at startup, relay availability gets a bounded first chance
- after local session start, local terminal work is always primary
- relay outages should degrade remote visibility, not break the user's active terminal session

## Origin Document

- Primary source: `docs/brainstorms/2026-04-06-relay-startup-reconnect-requirements.md`

## Requirements Trace

- R1-R5. Startup must attempt relay registration first, but only for a bounded window; timeout still enters the local terminal in an explicit reconnecting state.
- R6-R10. Once local work has started, relay loss must not terminate or block the local session; reconnect continues in the background with progressive backoff and stable local session identity.
- R11-R15. Success output must be concise; reconnect state must be low-noise, visible, and non-disruptive.
- R16-R18. Docs and runtime semantics must explicitly distinguish startup gating from post-startup continuity.

## Scope Boundaries

- No relay-side durable session persistence.
- No relay-side reconnect grace window in this change.
- No new frontend, web UI, or mobile UI work.
- No relay protocol changes are required for this phase.
- No user-facing configuration knobs for startup timeout or reconnect backoff in v1.
- No attempt to make the transient reconnect status renderer perfect for every terminal mode; the goal is a safe, low-noise default with a conservative fallback.

## Context & Research

### Relevant Code and Patterns

- `cmd/agentunnel/main.go` owns the current startup flow, startup banner, connector construction, and PTY/session startup sequencing.
- `cmd/agentunnel/main_test.go` already verifies startup ordering and the exact stderr banner, making it the right place to pin the new startup semantics.
- `connector/connector.go` already owns relay registration, background reconnect, and stable `protocol.SessionInfo` reuse across reconnect attempts; it is the right lifecycle boundary for initial connect vs steady-state reconnect.
- `connector/connector_test.go` already proves reconnect preserves buffered output and re-registers the same session, which provides a direct pattern for new startup-timeout and reconnect-state tests.
- `session/local_terminal.go` owns local raw-mode setup, stdout writes, stdin forwarding, and resize handling. It is the right place for a small terminal-status presenter because it already owns terminal-facing behavior.
- `session/local_terminal_test.go` covers resize-driven terminal behavior and is the right place to add redraw/cleanup tests for any transient status line support.
- `README.md`, `CLAUDE.md`, and `docs/architecture.md` are the operator-facing and architecture docs that currently describe relay dependency semantics and must stay aligned.

### Institutional Learnings

- No `docs/solutions/` entries exist in this repository today.

### External References

- None. The work is a repo-internal lifecycle and UX adjustment, and the current codebase already provides the relevant local patterns.

## System-Wide Impact

- End users of the CLI get a more honest startup contract and less noisy status output.
- Mobile and other relay clients will still observe session disappearance during disconnects in this phase, but reconnect will reuse the same local session identity when registration returns.
- The change is concentrated in `agentunnel`-side lifecycle management; the relay remains content-opaque and live-only.

## Key Technical Decisions

- Split connector lifecycle into two phases: an initial bounded connect attempt used by startup gating, and the existing long-lived reconnect loop used after the local session is running.
- Keep the generated `protocol.SessionInfo` and `SessionID` stable for the full lifetime of the local process so reconnect remains continuity of the same local run (see origin: `docs/brainstorms/2026-04-06-relay-startup-reconnect-requirements.md`).
- Use a fixed default startup wait window of 10 seconds in v1. This is long enough to give relay registration a real first chance without making the CLI feel hung.
- Use a fixed reconnect backoff schedule in v1 rather than adding flags or env vars: 3s, 5s, 10s, 20s, 60s, then repeat at 300s until success or process exit.
- Keep reconnect state presentation local to `agentunnel` and terminal-facing code rather than adding any new relay or protocol surface.
- Render reconnect state as a transient terminal status owned by `session/` instead of repeated normal logs. If transient redraw is unsafe or unavailable, fall back to sparse state-change notices rather than spamming the terminal.
- Shorten the startup banner to launcher and relay state only. Drop explanatory prose such as "local terminal is interactive" because terminal interactivity is already inherent in the program mode.

## Open Questions

### Resolved During Planning

- Should startup wait forever for relay? No; use a bounded wait window, then enter the local terminal in reconnecting state.
- Should initial startup failure exit the process? No; once the wait window expires, local work should still start.
- Should runtime relay loss terminate the local session? No; relay loss only affects remote availability.
- Should timeout and backoff be configurable in v1? No; keep v1 simpler with internal defaults.
- Does this require relay protocol changes? No; current reconnect and registration wiring is sufficient for this phase.

### Deferred to Implementation

- The exact ANSI strategy for transient status redraw may need light iteration to avoid disturbing PTY output on all terminals; implementation should keep a conservative fallback path.
- The exact startup success string can be finalized during implementation as long as it stays concise and tests pin the result.

## High-Level Technical Design

> *This illustrates intended structure and sequencing for review. It is directional guidance, not implementation specification.*

```text
launch agentunnel
  -> build SessionInfo once
  -> connector tries relay registration for up to 10s
       -> success: mark relay connected
       -> timeout/error: mark relay reconnecting
  -> start local PTY session either way
  -> print concise startup confirmation
  -> start terminal status presenter
  -> run connector steady-state loop:
       connected -> disconnected -> backoff -> reconnect -> connected
  -> status presenter updates only on state transitions
  -> local stdin/stdout path remains uninterrupted throughout
```

## Implementation Units

- [x] **Unit 1: Refactor connector lifecycle into startup gating plus steady-state reconnect**

**Goal:** Make relay connection usable in two distinct modes: a bounded startup attempt before local session start, and a long-running reconnect loop after the session is active.

**Requirements:** R1-R10, R16, R18

**Dependencies:** None

**Files:**
- Modify: `connector/connector.go`
- Modify: `connector/connector_test.go`

**Approach:**
- Extract a connector API that can perform one bounded registration attempt and report whether registration succeeded before startup timeout.
- Keep the existing reconnect behavior, but make its state transitions explicit enough that callers can observe `connected`, `disconnected`, `reconnecting`, and `reconnected` events without inspecting transport internals.
- Preserve stable reuse of the original `protocol.SessionInfo` and `SessionID` across reconnect attempts.
- Replace the fixed 1-second reconnect loop with the planned progressive backoff schedule while keeping cancellation responsive.
- Keep output buffering semantics compatible with the current reconnect tests unless there is a clear reason to tighten them in a follow-up change.

**Patterns to follow:**
- `connector/connector.go` as the single relay lifecycle boundary
- `connector/connector_test.go` reconnect tests, especially buffered output continuity

**Test scenarios:**
- Happy path: a startup-scoped connection attempt succeeds before timeout and reports connected status.
- Happy path: a startup-scoped connection attempt times out cleanly without blocking forever.
- Happy path: after a live disconnect, the connector retries with the expected progressive backoff sequence until reconnect succeeds.
- Happy path: reconnect uses the original session registration payload and `SessionID`.
- Edge case: connector cancellation during startup wait exits promptly.
- Edge case: connector cancellation during a backoff sleep exits promptly.
- Integration: output buffered during a disconnect is still delivered after reconnect.

**Verification:**
- The connector can be used both as a bounded startup dependency and as a background reconnect loop without splitting relay behavior across multiple packages.

- [x] **Unit 2: Change `agentunnel` startup flow to use bounded relay-first entry**

**Goal:** Make `cmd/agentunnel` give relay registration a real first chance, then start the local session in either connected or reconnecting mode with a shorter startup banner.

**Requirements:** R1-R5, R11, R16-R18

**Dependencies:** Unit 1

**Files:**
- Modify: `cmd/agentunnel/main.go`
- Modify: `cmd/agentunnel/main_test.go`

**Approach:**
- Move relay connection attempt earlier in `runWithArgs`, before child PTY startup, using the new bounded startup connect API.
- Keep local terminal preparation before PTY start, as the existing tests already protect that ordering.
- Start the PTY session after either:
  - successful relay registration within the startup window, or
  - startup timeout that transitions the process into reconnecting mode
- Replace the current verbose startup banner with a concise success message that reflects actual relay state at session entry.
- Continue running the steady-state connector loop after session start regardless of whether the initial startup window succeeded.

**Patterns to follow:**
- `cmd/agentunnel/main.go` orchestration and startup dependency injection seams
- `cmd/agentunnel/main_test.go` startup-order tests and stderr assertions

**Test scenarios:**
- Happy path: relay registration succeeds before timeout, then PTY session startup proceeds and stderr shows the concise connected banner.
- Happy path: relay registration times out, PTY session still starts, and stderr shows a concise reconnecting banner instead of failure.
- Edge case: local terminal preparation failure still stops the process before PTY startup.
- Edge case: PTY startup failure after startup timeout still returns the PTY error rather than swallowing it behind reconnect state.
- Edge case: shutdown during the bounded startup wait returns promptly.
- Regression: launcher args, label, session metadata, and relay URL/token wiring remain unchanged.

**Verification:**
- Startup semantics become explicit and testable, and no longer depend on a background side effect to decide whether remote access is available.

- [x] **Unit 3: Add a low-noise terminal reconnect status presenter**

**Goal:** Surface relay-unavailable and reconnecting state without interfering with normal terminal work.

**Requirements:** R6-R9, R12-R15

**Dependencies:** Unit 1, Unit 2

**Files:**
- Create: `session/status_line.go`
- Create: `session/status_line_test.go`
- Modify: `cmd/agentunnel/main.go`

**Approach:**
- Introduce a small terminal-status helper under `session/` that owns a single transient status line lifecycle and can react to relay state changes.
- Keep the status renderer decoupled from PTY output fanout; it should consume connection-state updates rather than inspect PTY content.
- Prefer transient redraw semantics for the reconnect line so the status remains visible near the bottom without creating repeated scrollback entries.
- Add a conservative fallback path that emits sparse state-change notices if the transient renderer cannot safely operate in the current terminal context.
- Ensure status cleanup happens on shutdown and state transitions so stale reconnect messages do not linger after connectivity returns.

**Patterns to follow:**
- `session/local_terminal.go` terminal ownership boundaries
- `session/local_terminal_test.go` resize-driven terminal behavior and cancellation handling

**Test scenarios:**
- Happy path: entering reconnecting state renders a compact status message.
- Happy path: reconnect success clears or replaces the reconnecting status without leaving stale terminal artifacts.
- Happy path: repeated reconnect attempts update the same transient status rather than emitting repeated full log lines.
- Edge case: terminal resize while reconnecting redraws status safely.
- Edge case: shutdown while reconnecting cleans up transient status state.
- Edge case: fallback mode emits only state-transition notices and does not spam every retry tick.

**Verification:**
- Relay connectivity state becomes visible to the user without degrading input responsiveness or normal PTY output readability.

- [x] **Unit 4: Align docs and lifecycle tests with the new semantics**

**Goal:** Make repository docs and tests state the same startup/reconnect contract that the code now implements.

**Requirements:** R10-R18

**Dependencies:** Unit 1, Unit 2, Unit 3

**Files:**
- Modify: `README.md`
- Modify: `CLAUDE.md`
- Modify: `docs/architecture.md`
- Modify: `cmd/agentunnel/main_test.go`
- Modify: `connector/connector_test.go`

**Approach:**
- Update operator-facing docs to describe the two-phase behavior explicitly: bounded startup wait for relay, then local continuation with background reconnect.
- Update architecture docs to state that relay unavailability after session start only affects remote visibility, not PTY ownership or local interactivity.
- Keep docs honest about current v1 limitations: no relay-side grace retention and no protocol changes for this phase.
- Extend lifecycle tests so the repo demonstrates the intended connected vs reconnecting startup modes and non-disruptive runtime reconnect behavior.

**Patterns to follow:**
- Documentation alignment rules in `CLAUDE.md`
- Existing startup and reconnect tests in `cmd/agentunnel/main_test.go` and `connector/connector_test.go`

**Test scenarios:**
- Happy path: documentation examples and code-level startup behavior agree on what users see when startup connects immediately.
- Happy path: documentation and tests agree on what users see when startup enters reconnecting mode.
- Regression: relay reconnect after startup still preserves local session continuity and does not require user action.
- Regression: no documentation claims relay is a hard runtime dependency once the local session has begun.

**Verification:**
- Docs and tests describe one coherent product contract instead of a mix of "mandatory relay" language and soft-dependency runtime behavior.

## Risks & Mitigations

- **Risk:** The transient status line could corrupt terminal display in some shells or terminal modes.
  - **Mitigation:** Keep the renderer small, test resize/cleanup behavior, and provide a sparse-notice fallback path.
- **Risk:** Startup gating could accidentally block too long or feel hung.
  - **Mitigation:** Use a fixed 10-second timeout, make timeout transitions explicit in the banner/status, and keep cancellation responsive.
- **Risk:** Connector lifecycle refactoring could introduce subtle deadlocks between startup connect, steady-state reconnect, and outbound output buffering.
  - **Mitigation:** Preserve `connector/connector_test.go` reconnect coverage and add targeted startup-timeout tests before changing orchestration.

## Validation Strategy

- `go test ./cmd/agentunnel ./connector ./session`
- `go test ./...`
- Manual validation during execution should specifically check:
  - startup with relay immediately reachable
  - startup with relay unreachable for longer than 10 seconds
  - runtime relay disconnect during active typing/output
  - runtime reconnect after a disconnect
  - reconnect status behavior during terminal resize
