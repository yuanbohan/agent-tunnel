---
title: fix: Stabilize startup update prompt terminal layout
type: fix
status: active
date: 2026-04-22
origin: docs/brainstorms/2026-04-18-tunnel-binary-update-requirements.md
---

# fix: Stabilize startup update prompt terminal layout

## Overview

Fix the interactive `tunnel run` update prompt so its title, version lines, question, and two menu options render flush-left and consistently spaced in a real terminal. The current implementation switches the terminal to raw mode before writing prompt text, then writes newline-only output. In raw mode, newline bytes are not guaranteed to return the cursor to column zero, which can produce the staircase indentation shown in the reported screenshot.

## Problem Frame

The binary update requirements intentionally specify a minimal English startup prompt with exact content shape and two arrow-menu actions (see origin: `docs/brainstorms/2026-04-18-tunnel-binary-update-requirements.md`). The current prompt satisfies the content contract but not the terminal layout contract in practice: after `term.MakeRaw`, output processing can stop translating `\n` to carriage-return plus newline, so each prompt line may start wherever the previous line ended.

This is a local CLI rendering bug. It should not change update eligibility, cadence, installer behavior, rollback behavior, command copy, or the surrounding `tunnel run` startup flow.

## Requirements Trace

- R11. Keep the startup update prompt in English.
- R12. Preserve the minimal prompt: current version, latest version, and confirmation question.
- R13. Preserve the approved copy shape for title, version lines, and question.
- R14-R15. Preserve the two-option arrow-menu interaction with `Update now` and `Skip and continue`.
- Reported screenshot. Prompt lines must not drift right due to raw-terminal newline handling.

## Scope Boundaries

- Do not change update-check cadence, manifest fetching, install behavior, rollback behavior, or re-exec behavior.
- Do not add release notes, command hints, colors, boxes, or extra prompt copy.
- Do not change non-interactive behavior; non-interactive `tunnel run` must remain silent and updater-free.
- Do not replace the prompt with a third-party UI library for this narrow fix.

## Context & Research

### Relevant Code and Patterns

- `cmd/tunnel/startup_update.go` owns `promptStartupUpdate` and `renderStartupUpdateOptions`.
- `promptStartupUpdate` calls `term.MakeRaw` before writing the static prompt header and options.
- `renderStartupUpdateOptions` currently uses ANSI clear-line sequences and newline-only writes.
- `cmd/tunnel/startup_update_test.go` already has good coverage for update gating, skip/install/re-exec decisions, and warnings, but not for the byte-level prompt layout contract.
- `README.md` documents the intended prompt shape and is a useful reference for the expected visible layout.

### Institutional Learnings

- No relevant `docs/solutions/` entries exist in this repository.

### External References

- None. The issue is explained by local terminal-mode behavior and existing code.

## Key Technical Decisions

- Normalize prompt output explicitly: prompt rendering should not depend on terminal output post-processing after raw mode is enabled.
- Keep the existing hand-rolled prompt: this is a two-option terminal interaction with small surface area, and the rest of the updater flow already depends on this local abstraction.
- Test the renderer at the byte/string boundary: most of the regression can be caught without requiring a real TTY or running the full updater.

## Open Questions

### Resolved During Planning

- Should this fix change the prompt copy? No. The requirements and README shape are still correct; only terminal layout should change.
- Should this use an external prompt library? No. The bug is localized, and the existing implementation is small enough to harden directly.

### Deferred to Implementation

- Whether the implementation chooses to enter raw mode after the static header or keeps the current ordering and writes explicit carriage returns is an implementation detail. The required outcome is that every visible prompt line starts at column zero under raw-terminal conditions.
- Exact helper names for line-writing and option rendering should follow the final code shape.

## Implementation Units

- [x] **Unit 1: Characterize prompt layout output**

**Goal:** Add focused tests that capture the intended startup update prompt layout before modifying renderer behavior.

**Requirements:** R11-R15, reported screenshot

**Dependencies:** None

**Files:**
- Modify: `cmd/tunnel/startup_update_test.go`
- Test: `cmd/tunnel/startup_update_test.go`

**Approach:**
- Add renderer-level coverage for the static prompt header plus initial option render. The expected visible layout should match the README example: title at column zero, blank line, `Current`, `Latest`, blank line, question, and the two options at predictable columns.
- Add coverage that specifically protects against newline-only staircase output in raw mode. The test does not need a real terminal; it can assert that prompt-rendered line breaks include an explicit carriage return strategy or pass through a helper that normalizes terminal line starts.
- Add coverage for rerendering after arrow-key movement so clearing and redrawing options does not depend on the terminal cursor already being at column zero.
- Keep existing startup update behavior tests intact; they verify gating and install/re-exec decisions separately.

**Patterns to follow:**
- Function-level tests and injected seams already used in `cmd/tunnel/startup_update_test.go`.
- README prompt example in `README.md`.

**Test scenarios:**
- Happy path: initial prompt render for current `v0.1.5` and latest `v0.1.6` produces the approved visible line order and left-aligned text.
- Edge case: rendering starts after an assumed nonzero cursor column and still emits output that returns each visible line to column zero.
- Edge case: rerendering from `Update now` to `Skip and continue` clears and redraws both option rows from column zero.
- Regression: option labels and selected default remain `Update now` first and `Skip and continue` second.

**Verification:**
- The new tests fail against the current newline-only raw-mode renderer and pass once terminal line starts are normalized.

- [x] **Unit 2: Normalize raw-terminal prompt rendering**

**Goal:** Update the prompt renderer so all prompt lines and option rerenders start at column zero in raw terminal mode.

**Requirements:** R11-R15, reported screenshot

**Dependencies:** Unit 1

**Files:**
- Modify: `cmd/tunnel/startup_update.go`
- Modify: `cmd/tunnel/startup_update_test.go`
- Test: `cmd/tunnel/startup_update_test.go`

**Approach:**
- Introduce a small rendering helper or equivalent local convention for terminal prompt lines that explicitly resets the cursor to the start of the line when writing line breaks under raw mode.
- Apply that convention to the static header, blank lines, question line, initial option render, and option rerenders.
- For option rerendering, keep the current two-line redraw behavior but make clearing/redrawing independent of the cursor's current column. The implementation should move to the intended rows and clear from a known column before writing each option.
- Preserve raw input handling, arrow-key behavior, Enter selection, Ctrl-C exit behavior, and error propagation.

**Patterns to follow:**
- Existing minimal `promptStartupUpdate` / `renderStartupUpdateOptions` split in `cmd/tunnel/startup_update.go`.
- Existing tests that stub `startupUpdatePrompt` for broader updater behavior; keep detailed renderer tests local to the renderer.

**Test scenarios:**
- Happy path: prompt render remains visually equivalent to the README example with `Update now` selected by default.
- Happy path: Down-arrow rerender marks `Skip and continue` selected without leaving duplicated or shifted option text.
- Edge case: output remains left-aligned when terminal raw mode disables newline translation.
- Error path: writer errors from header or option rendering still propagate as prompt errors.
- Regression: `maybeHandleStartupUpdate` skip/install/re-exec behavior remains unchanged.

**Verification:**
- `cmd/tunnel/startup_update_test.go` covers the prompt layout regression and the existing updater gate behaviors still pass.
- A manual interactive `tunnel run ...` with an available newer version shows the prompt left-aligned like the README example.

## System-Wide Impact

- **Interaction graph:** Only the local `tunnel run` startup prompt renderer changes. Update discovery, updater state, install, rollback, and relay startup remain unchanged.
- **Error propagation:** Writer and terminal input errors should continue to surface through `promptStartupUpdate` as they do today.
- **State lifecycle risks:** None. This plan does not touch `~/.tunnel/settings.json` or `~/.tunnel/updater.json` semantics.
- **API surface parity:** No relay API or protocol surface changes.
- **Integration coverage:** Existing updater gate tests remain the protection for flow behavior; new renderer tests protect terminal layout.
- **Unchanged invariants:** Non-interactive runs remain silent; prompt copy and option labels remain unchanged; selected default remains `Update now`.

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| Fixing line breaks in one part of the prompt leaves rerendered options shifted | Cover initial render and rerender paths separately. |
| Tests overfit to ANSI implementation details | Assert the visible line-start contract and only the minimal ANSI behavior needed for clearing/rerendering. |
| Prompt layout fix accidentally changes update flow behavior | Keep flow tests in `cmd/tunnel/startup_update_test.go` unchanged and add renderer tests beside them. |

## Documentation / Operational Notes

- No docs update is expected if the fixed prompt matches the existing README example.
- If implementation changes the visible prompt shape, update `README.md`, `docs/brainstorms/2026-04-18-tunnel-binary-update-requirements.md`, and `docs/plans/2026-04-18-003-feat-tunnel-binary-update-plan.md`; this plan assumes that will not be necessary.

## Sources & References

- Origin document: `docs/brainstorms/2026-04-18-tunnel-binary-update-requirements.md`
- Related plan: `docs/plans/2026-04-18-003-feat-tunnel-binary-update-plan.md`
- Related code: `cmd/tunnel/startup_update.go`
- Related tests: `cmd/tunnel/startup_update_test.go`
- Related docs: `README.md`
