---
title: feat: Add tunnel CLI help contract
type: feat
status: completed
date: 2026-04-12
origin: docs/brainstorms/2026-04-12-tunnel-cli-help-requirements.md
---

# feat: Add tunnel CLI help contract

## Overview

Add a first-class help contract for `tunnel` so users can discover the CLI without already knowing the launcher shape or setting runtime environment variables. The change should stay narrow: introduce explicit `--help` / `-h` fast paths, make bare `tunnel` print the same help text as a usage error, preserve the existing launcher passthrough boundary, and update the README where discoverability materially improves.

## Problem Frame

The origin requirements define a missing but externally visible CLI surface: `tunnel` currently supports `--version`, but it does not expose a readable help path and only returns a terse one-line usage error when no launcher command is present (see origin: `docs/brainstorms/2026-04-12-tunnel-cli-help-requirements.md`).

This work is small in code size but not purely local. It changes the public CLI contract, the success/error semantics visible to shells, and the documentation users rely on for first-run discovery. The plan therefore keeps the implementation conservative and focused on the existing `cmd/tunnel` argument boundary.

## Requirements Trace

- R1-R3. Add `tunnel --help` and `tunnel -h` as true fast paths that complete before base URL validation, token lookup, launcher resolution, or runtime startup.
- R4-R8. Print one repo-owned help message that explains invocation shape, supported flags, environment variables, the default hosted base URL, and concrete launcher examples.
- R9-R10. Make bare `tunnel` print the same help-oriented guidance while still exiting non-zero as incorrect usage.
- R11. Leave unrelated runtime validation behavior unchanged unless a small alignment is needed to support the help contract.
- R12. Mention the new help entrypoint in user-facing docs where it improves discoverability.

## Scope Boundaries

- No new subcommands.
- No migration to Cobra, urfave/cli, or another CLI framework.
- No redesign of all runtime error formatting.
- No change to relay startup, launcher resolution, PTY/session behavior, or attach semantics.
- No change to the current first-non-flag boundary where arguments after the launcher belong to the launched CLI rather than to `tunnel`.

## Context & Research

### Relevant Code and Patterns

- `cmd/tunnel/args.go` is the current CLI contract surface. It uses `flag.FlagSet` with `flag.ContinueOnError`, discards default flag output, supports `--version` as a fast path, validates `TUNNEL_BASE_URL`, and requires `TUNNEL_AUTH_TOKEN` for normal execution.
- `cmd/tunnel/args_test.go` already covers the most important parsing boundaries, including the current invariant that `tunnel codex --version` treats `--version` as a launcher argument rather than as a top-level `tunnel` flag.
- `cmd/tunnel/main.go` and `cmd/tunnel/main_test.go` already test that the version fast path exits before launcher resolution, connector startup, local terminal preparation, and session startup.
- `cmd/relay/config.go` and `cmd/migrate/main.go` use a small `usageError` helper pattern for user-facing CLI usage failures. That is the closest existing repo pattern for distinguishing "bad invocation" from runtime failures without introducing a framework.
- `README.md` already documents `tunnel --version`, `TUNNEL_BASE_URL`, `TUNNEL_AUTH_TOKEN`, and concrete tunnel launch examples, so it is the right place for minimal help-entrypoint documentation.

### Institutional Learnings

- No `docs/solutions/` entries exist in this repository.

### External References

- None. The repo already has sufficient local patterns for this narrow CLI contract change.

## Key Technical Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Help implementation style | Use an explicit repo-owned help path rather than relying on the flag package's default help output | The current parser discards default flag output, and the requirements call for custom content including env vars and examples. |
| Help text source | Keep one shared help/usage text generator for both explicit help and bare-command usage errors | This avoids drift between `--help` output and the no-command guidance path. |
| Exit semantics | `--help` / `-h` exit successfully; bare `tunnel` remains a usage error and exits non-zero | This matches the settled requirements and standard shell expectations. |
| Output stream split | Explicit help writes to stdout; bare-command usage guidance writes to stderr | This preserves the difference between user-requested help and incorrect invocation while reusing the same text body. |
| Launcher passthrough | Preserve the existing first-non-flag parsing boundary | `tunnel codex --help` and similar forms should continue to pass `--help` through to the launched CLI instead of being intercepted by `tunnel`. |
| Usage-error typing | Reuse the small typed usage-error pattern already present in `relay` and `migrate` | It fits the repo's existing CLI style and keeps the change smaller than introducing a richer command framework. |

## Open Questions

### Resolved During Planning

- Should this change use the flag package's built-in help output? No. The help text must be repo-owned so it can document env vars, defaults, and examples.
- Should arguments after the launcher be reparsed so `tunnel codex --help` triggers tunnel help? No. Keep the current first-non-flag boundary intact.
- Should bare `tunnel` exit successfully because it prints a full help message? No. It should print the same guidance text but remain a non-zero usage error.

### Deferred to Implementation

- Finalize the exact line breaks and heading density of the help text once it is visible beside the existing README examples. This is presentation detail, not a product decision.

## High-Level Technical Design

> *This illustrates the intended approach and is directional guidance for review, not implementation specification. The implementing agent should treat it as context, not code to reproduce.*

| Invocation | Tunnel interpretation | Output | Exit |
|---|---|---|---|
| `tunnel --help` | Explicit help fast path | Full help text to stdout | 0 |
| `tunnel -h` | Explicit help fast path | Full help text to stdout | 0 |
| `tunnel --version` | Existing version fast path | Version to stdout | 0 |
| `tunnel` | Usage error: missing launcher | Same full help text to stderr | non-zero |
| `tunnel codex --help` | Launcher passthrough | No tunnel help interception | normal runtime path |
| `tunnel --help` with unset/invalid env | Explicit help fast path | Full help text without env validation | 0 |

## Implementation Units

- [x] **Unit 1: Define the help and usage contract in `cmd/tunnel` argument parsing**

**Goal:** Add an explicit help mode, a shared help-text source, and a typed usage-error path while preserving the current launcher argument boundary.

**Requirements:** R1-R10

**Dependencies:** None

**Files:**
- Modify: `cmd/tunnel/args.go`
- Test: `cmd/tunnel/args_test.go`

**Approach:**
- Extend the parsed argument state so `cmd/tunnel` can distinguish three top-level outcomes cleanly: help fast path, version fast path, and normal execution.
- Introduce one shared help/usage text helper owned by `cmd/tunnel` rather than scattering strings across parse and runtime code.
- Add a local usage-error type or equivalent small helper so the no-command path can be treated as user-invocation error without changing unrelated runtime errors.
- Keep validation ordering conservative: top-level help must win before base URL validation and token lookup, while ordinary runtime paths must keep the current validation behavior.
- Preserve the current parse boundary where the first non-flag token starts launcher passthrough.

**Patterns to follow:**
- Existing argument parsing structure in `cmd/tunnel/args.go`
- Small typed usage-error pattern in `cmd/relay/config.go` and `cmd/migrate/main.go`

**Test scenarios:**
- Happy path: `parseRunArgs([]string{"tunnel", "--help"})` returns a help outcome without requiring `TUNNEL_AUTH_TOKEN`.
- Happy path: `parseRunArgs([]string{"tunnel", "-h"})` behaves the same as `--help`.
- Happy path: `parseRunArgs([]string{"tunnel", "--help"})` succeeds even when `TUNNEL_BASE_URL` is unset and `TUNNEL_AUTH_TOKEN` is unset.
- Edge case: explicit help wins before base URL validation when an invalid `TUNNEL_BASE_URL` is present in the environment.
- Edge case: `parseRunArgs([]string{"tunnel", "codex", "--help"})` keeps `--help` in `LauncherArgs` instead of treating it as a tunnel help request.
- Edge case: `parseRunArgs([]string{"tunnel", "--version"})` still uses the existing version fast path.
- Error path: `parseRunArgs([]string{"tunnel"})` returns a usage-oriented error for the missing launcher command.
- Error path: normal execution without `--help` or `--version` still rejects a missing `TUNNEL_AUTH_TOKEN`.

**Verification:**
- The parser can classify all top-level CLI outcomes needed by the requirements without broadening the scope of tunnel flag parsing.

- [x] **Unit 2: Wire help output and runtime fast-path behavior through `runWithArgs`**

**Goal:** Make explicit help and no-command usage guidance print the correct shared text to the correct stream without touching launcher or session startup.

**Requirements:** R1-R10

**Dependencies:** Unit 1

**Files:**
- Modify: `cmd/tunnel/main.go`
- Test: `cmd/tunnel/main_test.go`
- Test: `cmd/tunnel/args_test.go`

**Approach:**
- Route the new help outcome through `runWithArgs` the same way `--version` is already routed: write output, return successfully, and exit before runtime hooks are touched.
- Handle the missing-launcher usage error in one place so the same help text is emitted to stderr before the process exits non-zero.
- Avoid logger-formatted usage output. The top-level `cmd/tunnel` entrypoint should bypass `log.Fatal`-style timestamped formatting for usage/help-oriented failures so the terminal shows one clean help body rather than duplicated or noisy error lines.
- Keep the success/error split narrow: explicit help returns `nil`, usage error still returns an error, and existing runtime errors continue to bubble up unchanged.
- Avoid changing the normal startup path after argument parsing succeeds.

**Patterns to follow:**
- Early fast-path structure already used for `--version` in `cmd/tunnel/main.go`
- Runtime-isolation tests already present in `cmd/tunnel/main_test.go`

**Test scenarios:**
- Happy path: `runWithArgs([]string{"tunnel", "--help"}, ...)` writes the full help text to stdout, writes nothing to stderr, and returns `nil`.
- Happy path: `runWithArgs([]string{"tunnel", "-h"}, ...)` behaves the same as `--help`.
- Happy path: explicit help output includes the documented invocation shape, `--label`, `--base-url`, `--version`, the help flag, `TUNNEL_BASE_URL`, `TUNNEL_AUTH_TOKEN`, the `https://diaro.me` default, and at least one concrete launch example.
- Happy path: the help fast path does not call launcher resolution, connector creation, local terminal preparation, or session startup.
- Edge case: `runWithArgs([]string{"tunnel"}, ...)` writes the same help text body to stderr and returns a non-nil error.
- Edge case: the no-command path emits one clean usage/help body without an added log prefix or a duplicated second error rendering.
- Edge case: the no-command path does not touch launcher resolution, connector creation, local terminal preparation, or session startup.
- Edge case: `runWithArgs([]string{"tunnel", "--version"}, ...)` still writes only the version string and does not print help text.
- Error path: a real runtime invocation such as `runWithArgs([]string{"tunnel", "codex"}, ...)` still follows the existing startup path and error propagation behavior.

**Verification:**
- Help-oriented flows become pure CLI-output paths, while successful runtime startup and unrelated runtime failures behave exactly as they did before.

- [x] **Unit 3: Sync user-facing documentation with the new help entrypoint**

**Goal:** Mention the new help contract where users are already learning tunnel basics, without bloating the docs.

**Requirements:** R12

**Dependencies:** Unit 2

**Files:**
- Modify: `README.md`

**Approach:**
- Add a short discoverability note near the existing CLI install/version guidance or quick-start launch examples so users can find `tunnel --help` naturally.
- Keep the README brief and let the CLI remain the source of truth for the full help text.
- Reuse the same invocation shape and terminology as the CLI help output to avoid doc drift.

**Patterns to follow:**
- Existing concise CLI guidance style in `README.md`

**Test scenarios:**
- Test expectation: none -- this unit is documentation-only, but the README wording must match the final CLI help contract for `--help`, `-h`, and the top-level invocation shape.

**Verification:**
- A first-time user scanning the README can discover `tunnel --help` before needing to infer usage from examples alone.

## System-Wide Impact

- **Interaction graph:** the change is confined to `cmd/tunnel` argument parsing, CLI output wiring, and the README; no relay, connector, launcher, or session packages should observe new behavior.
- **Error propagation:** the only intended error-path change is that the missing-launcher invocation now prints full help guidance before returning a usage error.
- **State lifecycle risks:** there should be no new runtime state because help flows must return before any connector, terminal, or PTY resources are created.
- **API surface parity:** the externally visible contract surface is the `tunnel` CLI itself, especially `--help`, `-h`, bare invocation behavior, and the existing `--version`/launcher passthrough boundary.
- **Integration coverage:** `cmd/tunnel/main_test.go` is the key place to prove that help and no-command paths do not accidentally touch runtime startup hooks.
- **Unchanged invariants:** `--version` remains a fast path, `tunnel codex --help` remains launcher passthrough, `TUNNEL_AUTH_TOKEN` remains required for normal execution, and runtime startup semantics stay unchanged.

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| Help interception accidentally broadens tunnel flag parsing and steals `--help` from the launched CLI | Keep and test the existing first-non-flag boundary in `cmd/tunnel/args_test.go` |
| Help output and no-command guidance drift apart over time | Generate both from one shared help-text helper |
| Help path accidentally depends on base URL or token validation | Add explicit fast-path tests that run with unset or invalid environment values |
| README wording drifts from the CLI contract | Update only the discoverability note and keep the full contract in the CLI output itself |

## Documentation / Operational Notes

- No architecture or protocol docs need updates because this change is limited to local CLI discoverability.
- `README.md` is sufficient for user-facing documentation unless implementation reveals another user-facing install surface that already mirrors CLI basics.

## Sources & References

- **Origin document:** `docs/brainstorms/2026-04-12-tunnel-cli-help-requirements.md`
- Related code: `cmd/tunnel/args.go`
- Related code: `cmd/tunnel/args_test.go`
- Related code: `cmd/tunnel/main.go`
- Related code: `cmd/tunnel/main_test.go`
- Related code: `cmd/relay/config.go`
- Related code: `cmd/migrate/main.go`
- Related code: `README.md`
