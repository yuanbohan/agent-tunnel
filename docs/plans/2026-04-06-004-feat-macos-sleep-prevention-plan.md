---
title: feat: Add macOS sleep prevention to agentunnel startup
type: feat
status: completed
date: 2026-04-06
origin: docs/brainstorms/2026-04-06-agentunnel-macos-sleep-prevention-requirements.md
---

# feat: Add macOS sleep prevention to agentunnel startup

## Overview

Add default-on macOS idle-sleep prevention to `agentunnel` so long-running sessions keep the machine awake for the full lifetime of the `agentunnel` process. The CLI should surface this in the startup confirmation, continue startup if sleep prevention cannot be enabled, and document that the behavior is currently macOS-only.

## Problem Frame

`agentunnel` is designed to supervise long-running local terminal sessions plus relay connectivity, but today it does nothing to stop normal macOS idle sleep. That makes the product less reliable for the exact sessions it is meant to keep alive. The requirements document defines this as a default-on macOS capability with process-lifetime semantics and a visible, non-blocking failure mode (see origin: `docs/brainstorms/2026-04-06-agentunnel-macos-sleep-prevention-requirements.md`).

This is a small code change with an externally visible CLI contract: startup output changes, process lifecycle gains a helper subprocess on macOS, and docs must explicitly describe platform scope and failure behavior.

## Requirements Trace

- R1-R4. On macOS, startup must attempt idle-sleep prevention by default and keep it active for the lifetime of the `agentunnel` process.
- R5-R7. Failure to start sleep prevention must not block startup, but the startup confirmation must clearly show whether sleep prevention is active or failed.
- R8-R10. This phase is macOS-only, and the operator-facing docs must describe both the default-on scope and the non-blocking failure semantics.

## Scope Boundaries

- No user-facing flag or environment variable for enabling or disabling sleep prevention in v1.
- No cross-platform implementation parity beyond compiling safely and being explicit about unsupported platforms.
- No attempt to manage manual sleep, lid-close behavior, display sleep policy, or broader power-management settings.
- No changes to relay protocol, launcher support, or PTY ownership semantics.

## Context & Research

### Relevant Code and Patterns

- `cmd/agentunnel/main.go` already owns startup orchestration, startup banner text, and process-lifetime cleanup; it is the correct integration point for sleep-prevention lifecycle.
- `cmd/agentunnel/main_test.go` already uses injection seams (`resolveLauncher`, `prepareLocalTerminal`, `startSession`, `newConnector`) and exact stderr assertions, so it is the right place to pin startup output and non-blocking failure behavior.
- `session/process.go` owns the PTY child only. Keeping sleep prevention outside `session/` avoids coupling PTY management to a macOS-only host concern.
- `README.md`, `CLAUDE.md`, `AGENTS.md`, and `docs/architecture.md` are the docs called out by repo guidance and must stay aligned when startup behavior changes.

### Institutional Learnings

- No `docs/solutions/` entries exist in this repository today.

### External References

- Darwin `caffeinate(8)` local system manual: `caffeinate` with no extra flags prevents idle sleep, and `-w <pid>` keeps the assertion active until the target process exits.

## Key Technical Decisions

- Use a small `cmd/agentunnel`-local helper with OS-specific files instead of introducing a new shared package. This feature is entrypoint-specific and does not justify a repo-wide abstraction.
- On macOS, launch `/usr/bin/caffeinate -i -w <agentunnel pid>` as a helper subprocess. `-i` matches the requirements' idle-sleep scope, and `-w` matches the desired `agentunnel` process-lifetime semantics without wrapping the PTY child directly.
- Start sleep prevention only after local terminal preparation and PTY session startup succeed, but before the startup banner is printed. This keeps the banner truthful while avoiding orphan helper processes on earlier startup failures.
- Keep sleep-prevention status as a small startup-status value set with three user-facing outcomes: active, failed, or unsupported. This keeps banner formatting deterministic and testable.
- On non-macOS builds, compile a no-op helper that reports `unsupported` rather than pretending the feature exists. The phase remains macOS-only, but unsupported runs stay explicit and safe.
- Use a second semicolon-delimited clause in the startup banner for sleep status so relay state and sleep state remain independently readable without adding extra log lines.

## Open Questions

### Resolved During Planning

- What system mechanism should macOS use? Use `/usr/bin/caffeinate` directly rather than PATH lookup so startup behavior does not depend on shell environment.
- Should sleep prevention be tied to the launcher child PID or the `agentunnel` PID? Tie it to the `agentunnel` PID so the assertion matches the product's process-lifetime requirement.
- How should unsupported platforms behave in code? Build a no-op helper that reports `unsupported`, letting the binary compile cleanly while keeping the startup banner honest.
- How should banner wording stay concise? Keep the existing relay clause and append a short sleep clause such as `sleep prevented`, `sleep prevention failed`, or `sleep unsupported`.

### Deferred to Implementation

- The exact internal helper API shape can stay flexible as long as it exposes a status plus cleanup path that `runWithArgs` can own.
- The final banner punctuation can be tuned during implementation if tests pin the resulting wording and the line stays concise.

## High-Level Technical Design

> *This illustrates the intended approach and is directional guidance for review, not implementation specification. The implementing agent should treat it as context, not code to reproduce.*

```text
agentunnel startup
  -> parse args and build relay/session metadata
  -> prepare local terminal
  -> start PTY session
  -> start sleep-prevention helper
       darwin: /usr/bin/caffeinate -i -w <agentunnel pid>
       other OS: no-op helper returns unsupported status
       helper start error: record failed status, continue startup
  -> print startup banner with:
       relay state + sleep state
  -> keep helper tied to process lifetime
  -> on shutdown or early-return cleanup:
       stop helper if agentunnel still owns it
```

## Implementation Units

- [x] **Unit 1: Add a startup-local sleep-prevention helper with Darwin and fallback implementations**

**Goal:** Introduce a small helper that encapsulates sleep-prevention lifecycle and status reporting without leaking macOS-specific behavior into unrelated packages.

**Requirements:** R1-R5, R8

**Dependencies:** None

**Files:**
- Create: `cmd/agentunnel/sleep_prevention.go`
- Create: `cmd/agentunnel/sleep_prevention_darwin.go`
- Create: `cmd/agentunnel/sleep_prevention_other.go`
- Create: `cmd/agentunnel/sleep_prevention_test.go`
- Test: `cmd/agentunnel/sleep_prevention_test.go`

**Approach:**
- Define a package-local status model that covers `active`, `failed`, and `unsupported`, plus any small metadata needed for banner rendering.
- Keep process spawning behind a narrow seam so tests can validate Darwin behavior without launching real system helpers.
- In the Darwin implementation, invoke `/usr/bin/caffeinate` with `-i -w <agentunnel pid>` and suppress helper stdio so the subprocess cannot corrupt terminal output.
- In the non-Darwin implementation, return an explicit `unsupported` status with a no-op cleanup path.
- Ensure cleanup is idempotent so startup failures and normal shutdown can both safely call it.

**Execution note:** Start with unit tests around status mapping and helper-start outcomes before wiring it into `runWithArgs`.

**Patterns to follow:**
- `cmd/agentunnel/main_test.go` dependency-injection style for startup seams
- `launcher/registry.go` small focused helper style with narrow responsibility

**Test scenarios:**
- Happy path: Darwin helper start requests `/usr/bin/caffeinate` with `-i -w <current pid>` and returns `active`.
- Happy path: fallback helper on non-Darwin returns `unsupported` with no error.
- Error path: Darwin helper start failure returns `failed` status while still producing a cleanup function safe to call.
- Edge case: repeated cleanup calls after helper creation do not panic or return a conflicting outcome.
- Integration: helper status strings remain deterministic enough for startup-banner tests to rely on them.

**Verification:**
- `cmd/agentunnel` can obtain a single sleep-prevention status and cleanup handle without importing macOS details into `session/` or `connector/`.

- [x] **Unit 2: Integrate sleep-prevention lifecycle and banner output into `agentunnel` startup**

**Goal:** Make `runWithArgs` start sleep prevention at the right point in startup, preserve non-blocking failure behavior, and expose the new state in the existing startup confirmation.

**Requirements:** R1-R7

**Dependencies:** Unit 1

**Files:**
- Modify: `cmd/agentunnel/main.go`
- Modify: `cmd/agentunnel/main_test.go`
- Test: `cmd/agentunnel/main_test.go`

**Approach:**
- Add a package-level seam for starting sleep prevention so tests can force active, failed, and unsupported states without spawning real subprocesses.
- Start the helper after local terminal preparation and PTY startup succeed, then include its status in the startup banner before local terminal attach begins.
- Keep sleep-prevention failure out of the fatal startup path; the only user-facing change should be the banner status, not a returned error.
- Defer helper cleanup from the point where `agentunnel` successfully takes ownership so early returns after helper startup do not leak the subprocess.
- Extend the startup banner formatter to combine relay state and sleep state in one concise line while preserving the current relay-first semantics.

**Patterns to follow:**
- `cmd/agentunnel/main.go` startup sequencing and cleanup ownership
- `cmd/agentunnel/main_test.go` exact stderr assertions for startup output contracts

**Test scenarios:**
- Happy path: relay connected plus successful sleep prevention prints one startup line containing both `relay connected` and `sleep prevented`.
- Happy path: relay reconnecting plus successful sleep prevention prints one startup line containing both reconnecting relay state and `sleep prevented`.
- Error path: sleep-prevention start failure still returns nil startup error and prints `sleep prevention failed`.
- Happy path: unsupported-platform helper status still allows startup and prints `sleep unsupported`.
- Edge case: local terminal preparation failure still exits before PTY startup and does not attempt sleep prevention.
- Edge case: PTY startup failure still returns the PTY error and does not leak a sleep-prevention helper.
- Edge case: a later `waitForExit` error still runs helper cleanup exactly once.
- Regression: launcher args, session metadata, relay URL/token wiring, and relay-state banner semantics remain unchanged apart from the new sleep clause.

**Verification:**
- Startup output becomes deterministic for all sleep-status states, and sleep prevention never turns a previously successful startup path into a fatal error.

- [x] **Unit 3: Align operator and architecture docs with the new startup contract**

**Goal:** Document that macOS sleep prevention is default-on, process-scoped, and non-blocking when unavailable.

**Requirements:** R8-R10

**Dependencies:** Unit 1, Unit 2

**Files:**
- Modify: `README.md`
- Modify: `CLAUDE.md`
- Modify: `AGENTS.md`
- Modify: `docs/architecture.md`

**Approach:**
- Update the quick-start and expected startup-output sections in `README.md` so operators see the new sleep clause alongside relay status.
- Update `CLAUDE.md` and `AGENTS.md` product-boundary language so future edits preserve the macOS-only scope and non-blocking failure semantics.
- Update `docs/architecture.md` to describe sleep prevention as a host-level lifecycle owned by `agentunnel`, distinct from PTY ownership and relay connectivity.
- Keep docs explicit that this phase only prevents idle sleep on macOS and does not promise broader power-management behavior.

**Patterns to follow:**
- Existing startup-contract wording in `README.md`
- Documentation alignment expectations already stated in `CLAUDE.md`

**Test scenarios:**
- Test expectation: none -- this unit is documentation-only and relies on review for correctness.

**Verification:**
- Repo docs consistently describe when sleep prevention starts, what the user sees at startup, and why startup continues when the helper cannot be enabled.

## System-Wide Impact

- **Interaction graph:** `cmd/agentunnel` gains one extra host-level subprocess on macOS, but `session/`, `connector/`, and `protocol/` remain unchanged.
- **Error propagation:** Sleep-prevention startup failures are converted into banner-visible status instead of returned errors; PTY and relay errors keep their existing paths.
- **State lifecycle risks:** Helper cleanup must be owned from `runWithArgs` so early startup exits and normal shutdown do not leak a lingering `caffeinate` process.
- **API surface parity:** The externally visible CLI surface changes through startup stderr output only; no flags, env vars, or relay APIs change.
- **Integration coverage:** Startup tests must cover the combination of relay state and sleep state so banner wording changes stay intentional.
- **Unchanged invariants:** Relay-first startup behavior, supported launcher set, PTY ownership, and post-startup reconnect semantics remain as implemented today.

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| `caffeinate` subprocess management leaks on uncommon startup failures | Start the helper only after PTY startup succeeds, and own cleanup with an idempotent deferred stop path in `runWithArgs`. |
| Banner wording becomes too noisy once relay and sleep states are combined | Keep a fixed two-clause format and pin exact stderr strings in `cmd/agentunnel/main_test.go`. |
| Non-macOS builds accidentally regress because the feature is coded inline without build boundaries | Use build-tagged files with an explicit fallback implementation and test the unsupported path. |
| Future contributors broaden the feature into unsupported power-management promises | Update `README.md`, `CLAUDE.md`, `AGENTS.md`, and `docs/architecture.md` to restate the idle-sleep-only, macOS-only boundary. |

## Documentation / Operational Notes

- There is no rollout flag; once implemented, macOS startup will attempt sleep prevention by default.
- Execution should include at least one manual macOS sanity check to confirm the helper process appears and the startup line matches the documented wording.
- Automated tests can prove helper lifecycle and banner behavior, but they cannot fully prove real machine sleep behavior; that remains a manual verification item during implementation.

## Sources & References

- **Origin document:** `docs/brainstorms/2026-04-06-agentunnel-macos-sleep-prevention-requirements.md`
- Related code: `cmd/agentunnel/main.go`, `cmd/agentunnel/main_test.go`, `session/process.go`, `README.md`, `CLAUDE.md`, `AGENTS.md`, `docs/architecture.md`
- External docs: Darwin `caffeinate(8)` local system manual
