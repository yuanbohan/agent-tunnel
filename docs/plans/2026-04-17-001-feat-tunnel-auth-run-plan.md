---
title: feat: Add tunnel auth flow and explicit run subcommand
type: feat
status: active
date: 2026-04-17
origin: docs/brainstorms/2026-04-17-tunnel-auth-run-requirements.md
---

# feat: Add tunnel auth flow and explicit run subcommand

## Overview

Reshape the `tunnel` CLI so built-in commands own the top-level namespace and local command execution moves under `tunnel run <command>`. Add a first-pass terminal-native `tunnel auth` flow that logs in with relay username/password, creates one machine-local agent token automatically, stores the current local login in `~/.tunnel/auth.json`, keeps `TUNNEL_AUTH_TOKEN` as the higher-priority runtime override, and exposes a local-only JSON `tunnel auth status` surface that explains which auth source is currently effective.

## Problem Frame

The origin requirements settle two product-level changes that planning must preserve:

- `tunnel <command>` is no longer acceptable because it permanently collides with repo-owned subcommands
- first-run authentication must become a repo-owned terminal workflow instead of an external manual environment-variable prerequisite

The current `cmd/tunnel` implementation is already Cobra-based, but it still behaves like a passthrough launcher: the root command accepts arbitrary args, validates `TUNNEL_AUTH_TOKEN`, and launches immediately. The relay backend already exposes the exact app login and agent-token creation APIs needed for a terminal-native flow, so the implementation problem is local CLI restructuring and safe machine-local auth persistence, not backend invention (see origin: `docs/brainstorms/2026-04-17-tunnel-auth-run-requirements.md`).

## Requirements Trace

- R1-R5. Replace the passthrough root behavior with a root command that owns subcommands, adds `run` and `auth`, and turns legacy `tunnel <command>` invocations into guided failures.
- R6-R7. Keep one shared relay base-URL resolution rule for `run` and `auth login`, but do not persist `base_url`.
- R8-R13. Add terminal-native `auth login` using relay username/password login plus automatic agent-token creation, with one current stored login only.
- R14-R16. Store local auth state in `~/.tunnel/auth.json`, reserve `~/.tunnel/config.json`, and persist only the metadata needed for local fallback and local-only status.
- R17-R18. Keep `TUNNEL_AUTH_TOKEN` as the explicit higher-priority source and use `auth.json` only as interactive fallback.
- R19-R21. Make `auth logout` local-only and preserve environment-variable precedence after logout.
- R22-R26. Make `auth status` default to JSON, stay local-only, and report both available sources and the currently active source after precedence is applied.

## Scope Boundaries

- No Windows support.
- No browser/OAuth login flow.
- No system credential store or Keychain integration.
- No multi-host or multi-account local auth state.
- No remote token revocation from `auth logout`.
- No relay validation in `auth status`.
- No persisted `base_url`.
- No behavior in this pass that depends on `~/.tunnel/config.json`.

## Context & Research

### Relevant Code and Patterns

- `cmd/tunnel/cmd.go`, `cmd/tunnel/args.go`, `cmd/tunnel/main.go`, and their tests currently define the full `tunnel` CLI contract. The root command already uses Cobra, so this work can stay inside the current framework rather than introducing a new parser.
- `cmd/relay/command.go` shows the repo’s current Cobra subcommand style: small command constructors, `SilenceUsage`, `SilenceErrors`, typed usage wrapping, and explicit child commands. That is the right local pattern for moving `tunnel` to `run` and `auth`.
- `cmd/tunnel/args_test.go` and `cmd/tunnel/main_test.go` already pin important CLI boundaries such as `--help`, `--version`, invalid base URLs, and runtime fast paths. They are the natural place to convert old launcher assumptions into the new `run` contract.
- `internal/e2e/client.go` already has thin, production-shaped HTTP helpers for `POST /api/auth/login` and `POST /api/agent-tokens`. Those request and response shapes are confirmed by `internal/relay/handler/types/auth.go`, `internal/relay/handler/types/agent_token.go`, and `docs/api.md`.
- `golang.org/x/term` is already in `go.mod` and is already used in `internal/tunnel/session/local_terminal.go`, so hidden password prompting does not require a new dependency.
- There is no existing user-level local auth/config file pattern in the repo. This work will establish the first one.

### Institutional Learnings

- No `docs/solutions/` entries exist in this repository.

### External References

- None. The repo already has sufficient local patterns for this CLI and relay-auth integration.

## Key Technical Decisions

| Decision | Choice | Rationale |
|---|---|---|
| CLI framework | Keep Cobra and restructure the existing root command | `cmd/tunnel` already uses Cobra, and `cmd/relay` demonstrates the repo’s preferred subcommand style. |
| Launcher surface | Make `run` the only command-launching entrypoint | This directly satisfies the collision-avoidance requirement instead of preserving a dual-mode CLI indefinitely. |
| Legacy command handling | Treat `tunnel <command>` as a guided unknown-command/usage failure | The migration is a deliberate break, but the error should teach `tunnel run <command>` instead of failing opaquely. |
| Auth persistence boundary | Persist only machine-local agent auth in `~/.tunnel/auth.json`; reserve but do not use `~/.tunnel/config.json` yet | This matches the requirements, keeps the first pass small, and avoids inventing config semantics before they exist. |
| Secret lifetime | Persist the created agent token, but do not persist app access or refresh tokens from `POST /api/auth/login` | Runtime and local status need only the agent token plus local metadata; persisting app sessions would expand the local secret surface without adding required behavior. |
| Source precedence | Resolve auth as `TUNNEL_AUTH_TOKEN` first, then `auth.json` | This preserves explicit env override for scripts, CI, and one-off operator flows. |
| Status data model | Report source-aware JSON with separate local source records plus an `active_source` field | This avoids pretending the active env token has local metadata that may not exist, while still showing when file-backed state is shadowed. |
| File permissions | Create `~/.tunnel/` with `0700` and `auth.json` with `0600` | Plaintext token storage needs explicit local file hardening even before any future credential-store integration. |
| `config.json` creation | Do not create `~/.tunnel/config.json` in this pass | A reserved path is sufficient; creating an empty file now adds no user value and creates needless file-state permutations. |

## Open Questions

### Resolved During Planning

- Should this work introduce a new CLI framework? No. Keep the existing Cobra root and extend it.
- Should `auth login` save relay app-session credentials for later use? No. Persist only the created agent token and local metadata needed for `run` and `status`.
- Should this pass create `~/.tunnel/config.json` preemptively? No. Reserve the path but avoid creating or depending on it until a real non-auth setting exists.
- Should `auth status` attempt network validation? No. Keep it local-only and source-aware.

### Deferred to Implementation

- Finalize the exact generated token-label format once the implementation agent sees what host/machine identifiers are available portably on macOS/Linux without adding unnecessary instability.
- Finalize the exact `auth status` JSON field names once the source-aware shape is implemented and reviewed beside real command output.

## High-Level Technical Design

> *This illustrates the intended approach and is directional guidance for review, not implementation specification. The implementing agent should treat it as context, not code to reproduce.*

### Command Topology

| Invocation | Intended interpretation |
|---|---|
| `tunnel run <command> [args...]` | Launch local command through the existing tunnel runtime |
| `tunnel auth login` | Prompt in terminal, relay login, create agent token, write `auth.json` |
| `tunnel auth logout` | Delete local auth file only |
| `tunnel auth status` | Emit local-only JSON describing available auth sources and active precedence |
| `tunnel <command>` | Guided failure pointing to `tunnel run <command>` |
| `tunnel --version` | Preserve top-level version fast path |

### Runtime Source Resolution

| Concern | Precedence |
|---|---|
| Relay base URL | `--base-url` → `TUNNEL_BASE_URL` → `https://diaro.me` |
| Runtime auth token | `TUNNEL_AUTH_TOKEN` → `~/.tunnel/auth.json` |

### Local Auth State Shape

The persisted file should stay minimal and machine-oriented:

- schema version
- stored username
- generated token label
- plaintext agent token
- local timestamps useful for status output

The file should not contain:

- app access token
- refresh token
- persisted `base_url`
- multiple hosts or accounts

### Status Output Direction

Prefer a source-aware shape over a flattened “current user” shape. At minimum, the output should make it possible to answer these questions without network access:

- Is `TUNNEL_AUTH_TOKEN` set?
- Does `~/.tunnel/auth.json` exist and parse?
- Which source is active after precedence?
- If a file-backed login exists, what local metadata is known about it?

## Implementation Units

- [ ] **Unit 1: Restructure `cmd/tunnel` around explicit `run` and `auth` subcommands**

**Goal:** Convert the existing passthrough root command into a root that owns subcommands, preserves help/version behavior, and teaches the `run` migration clearly.

**Requirements:** R1-R7

**Dependencies:** None

**Files:**
- Modify: `cmd/tunnel/cmd.go`
- Modify: `cmd/tunnel/args.go`
- Modify: `cmd/tunnel/main.go`
- Test: `cmd/tunnel/args_test.go`
- Test: `cmd/tunnel/main_test.go`

**Approach:**
- Move the existing launcher-oriented flags and parsing rules under a `run` subcommand instead of keeping them on the root command.
- Keep one shared base-URL resolution helper that both `run` and `auth login` can call so precedence cannot drift.
- Preserve the top-level help and version contract while teaching the new command topology in help text and error output.
- Handle legacy `tunnel <command>` invocations at the root boundary so users get an actionable migration error instead of Cobra’s raw unknown-command wording.

**Patterns to follow:**
- Cobra subcommand construction in `cmd/relay/command.go`
- Existing usage-error handling in `cmd/tunnel/main.go`
- Existing CLI contract tests in `cmd/tunnel/args_test.go` and `cmd/tunnel/main_test.go`

**Test scenarios:**
- Happy path: `tunnel run codex --profile prod` parses to the same launcher/runtime inputs the old root launcher path used.
- Happy path: `tunnel --help` and `tunnel --version` still work without requiring auth or launcher resolution.
- Edge case: `tunnel claude` exits non-zero with guidance that points to `tunnel run claude`.
- Edge case: `tunnel run --help` shows run-specific invocation guidance instead of launching anything.
- Edge case: `tunnel run codex --help` still passes `--help` through to the launched CLI after the `run` boundary.
- Error path: `tunnel run` with no launcher remains a usage error.
- Error path: invalid `--base-url` still fails before runtime startup on the `run` path.

**Verification:**
- The only path that launches a local command is `tunnel run`.
- Legacy direct-launch invocations become guided failures instead of silent behavior drift.

- [ ] **Unit 2: Add a minimal local auth store and source-resolution helpers**

**Goal:** Introduce one repo-owned place for machine-local auth state, with explicit permissions and no dependency on future config semantics.

**Requirements:** R14-R18, R21

**Dependencies:** Unit 1

**Files:**
- Create: `cmd/tunnel/auth_store.go`
- Create: `cmd/tunnel/auth_store_test.go`
- Modify: `cmd/tunnel/args.go`
- Modify: `cmd/tunnel/main.go`

**Approach:**
- Implement a small auth-store layer in `cmd/tunnel` rather than inventing a reusable internal framework prematurely; this is a CLI-owned concern in this pass.
- Centralize `~/.tunnel/` path resolution, directory creation, file read/write/delete, JSON encoding/decoding, and permission setting in one place.
- Define an `auth.json` schema that stores only the agent token plus local metadata useful for `status` and file-backed fallback.
- Introduce a single source-resolution helper that computes the effective auth token and source metadata for `run` and `status`, instead of letting each command improvise precedence.
- Reserve the `config.json` path as a constant or helper outcome, but do not read, write, or require the file yet.

**Patterns to follow:**
- Simple file I/O style already used in `internal/migration/migration.go` and related tests
- Minimal, typed CLI-owned config structs rather than broad reusable abstractions

**Test scenarios:**
- Happy path: writing a stored login creates `~/.tunnel/` and `auth.json` with the expected permissions and schema.
- Happy path: reading back a valid `auth.json` returns the same token and stored metadata.
- Edge case: `auth.json` missing is treated as “no stored login,” not as a fatal parse error.
- Edge case: corrupted JSON returns a clear local-store error that `auth status` and `run` can surface deterministically.
- Edge case: `TUNNEL_AUTH_TOKEN` present causes source resolution to choose `env` even when `auth.json` exists.
- Edge case: `auth logout` removes the local file while leaving environment-driven precedence unchanged.
- Error path: auth-store initialization fails cleanly when the home directory cannot be resolved or the directory cannot be created.

**Verification:**
- All local auth persistence and precedence logic lives behind one small store/resolution layer.
- No code in this pass depends on `~/.tunnel/config.json`.

- [ ] **Unit 3: Implement terminal-native `auth login`, local-only `auth logout`, and source-aware JSON `auth status`**

**Goal:** Add the user-facing auth command group without persisting more secret material than the runtime actually needs.

**Requirements:** R6-R7, R8-R13, R19-R26

**Dependencies:** Unit 1, Unit 2

**Files:**
- Create: `cmd/tunnel/auth_cmd.go`
- Create: `cmd/tunnel/auth_cmd_test.go`
- Create: `cmd/tunnel/auth_api.go`
- Create: `cmd/tunnel/auth_api_test.go`
- Modify: `cmd/tunnel/cmd.go`
- Modify: `cmd/tunnel/main.go`

**Approach:**
- Add an `auth` command group with `login`, `logout`, and `status` children following the same Cobra construction style as `cmd/relay`.
- Keep the relay HTTP client thin and purpose-built for this command surface: login and create-agent-token only. Mirror the request/response shapes already proven in `internal/e2e/client.go` rather than designing a broader SDK.
- Introduce a small prompt abstraction so tests can supply username/password input without requiring a real TTY, while the production path uses `golang.org/x/term` to hide password input.
- Make `auth login` perform: resolve base URL, collect credentials, `POST /api/auth/login`, `POST /api/agent-tokens` with an auto-generated name, then overwrite the one current stored login in `auth.json`.
- Make `auth logout` delete only the local auth file.
- Make `auth status` render source-aware JSON, including both available sources and the currently active source after precedence is applied.

**Technical design:** *(directional only)*

Prefer a JSON shape along these lines:

| Field | Meaning |
|---|---|
| `active_source` | `env`, `file`, or `none` |
| `available_sources` | source records present locally |
| `env` | presence-only or masked metadata for `TUNNEL_AUTH_TOKEN` |
| `file` | parsed `auth.json` metadata such as username and token label |

This keeps “what is active?” separate from “what metadata do we know locally?” and avoids guessing user identity for env-only tokens.

**Patterns to follow:**
- `cmd/relay/command.go` subcommand layout
- `internal/e2e/client.go` request/response handling
- Existing JSON response structs in `internal/relay/handler/types/auth.go` and `internal/relay/handler/types/agent_token.go`

**Test scenarios:**
- Happy path: `auth login` with valid credentials stores one current login and prints enough success output for the user to know local auth is ready.
- Happy path: `auth status` with only `auth.json` present reports `file` as the active source.
- Happy path: `auth status` with both env and file present reports `env` as active and `file` as shadowed/available.
- Happy path: `auth logout` deletes only the local file and succeeds even if env auth remains set.
- Edge case: `auth login` overwrites an existing stored login instead of appending to it.
- Edge case: auto-generated token naming is deterministic enough to test without making the label format brittle.
- Edge case: `auth status` with no env and no file emits a stable JSON “not logged in/no available source” shape.
- Error path: invalid username/password propagates the relay error cleanly and does not create `auth.json`.
- Error path: login succeeds but agent-token creation fails; no partial local auth file is left behind.
- Error path: hidden password prompting fails or stdin is non-interactive in a way that should produce a clear CLI failure.

**Verification:**
- The command group delivers all first-pass auth behavior without persisting relay app-session credentials or contacting the relay during `status`.

- [ ] **Unit 4: Route `tunnel run` through source-aware auth fallback and keep runtime startup unchanged after token selection**

**Goal:** Make the new `run` command consume the new precedence model while preserving the existing connector/session startup semantics once an auth token has been resolved.

**Requirements:** R6-R7, R17-R18, R21

**Dependencies:** Unit 1, Unit 2

**Files:**
- Modify: `cmd/tunnel/cmd.go`
- Modify: `cmd/tunnel/main.go`
- Test: `cmd/tunnel/args_test.go`
- Test: `cmd/tunnel/main_test.go`

**Approach:**
- Split “resolve runtime inputs” from “start tunnel session” so the run path can choose env vs file token before touching launcher resolution or session startup.
- Keep the post-resolution startup path unchanged: once `runArgs.AuthToken` is populated, the connector/session path should behave the same way it does today.
- Update help text and usage strings so the runtime command shape and auth fallback behavior are explained where users already look.

**Patterns to follow:**
- Existing `runTunnelSession` startup path in `cmd/tunnel/main.go`
- Existing runtime-isolation tests in `cmd/tunnel/main_test.go`

**Test scenarios:**
- Happy path: `tunnel run codex` uses `TUNNEL_AUTH_TOKEN` when set.
- Happy path: `tunnel run codex` falls back to stored file auth when env auth is absent.
- Edge case: `tunnel run codex` with both env and file present passes the env token into the connector.
- Edge case: `tunnel run --help` and `tunnel --help` remain fast paths that do not read the auth file unnecessarily.
- Error path: `tunnel run codex` fails with clear guidance when neither env nor file auth is available.
- Error path: a corrupt `auth.json` produces a deterministic CLI error before launcher/session startup.
- Regression: once a token is resolved, successful startup still reaches launcher resolution, terminal preparation, connector creation, and session startup exactly as before.

**Verification:**
- Auth-source precedence changes only the token-selection phase; runtime startup semantics after token resolution remain stable.

- [ ] **Unit 5: Extend docs and end-to-end coverage for the new CLI contract**

**Goal:** Update the repo’s authoritative docs and add at least one end-to-end proof that local auth can drive `tunnel run` without manual token export.

**Requirements:** R1-R7, R14-R26

**Dependencies:** Units 1-4

**Files:**
- Modify: `README.md`
- Modify: `docs/architecture.md`
- Modify: `docs/local-e2e.md`
- Modify: `AGENTS.md`
- Modify: `CLAUDE.md`
- Create or modify: `internal/e2e/tunnel_auth_test.go`
- Test: `internal/e2e/local_regression_test.go`

**Approach:**
- Update all user-facing command examples from `tunnel <command>` to `tunnel run <command>`.
- Document the first-pass local auth contract clearly: `auth login`, local `auth.json` fallback, env override, local-only `status`, and local-only `logout`.
- Add targeted E2E coverage that exercises the new auth flow at the CLI boundary rather than only through the test helper HTTP client. The strongest happy-path proof is: login through `tunnel auth login`, then `tunnel run` with no `TUNNEL_AUTH_TOKEN`.
- Keep app-facing API docs unchanged unless implementation reveals an externally visible API contract change, because this work only consumes existing APIs.

**Patterns to follow:**
- Current README quick-start structure
- Current local E2E harness in `internal/e2e`
- Docs alignment rules in `AGENTS.md` / `CLAUDE.md`

**Test scenarios:**
- Happy path: an end-to-end flow can log in through the CLI, write `auth.json`, and launch `tunnel run` without exporting `TUNNEL_AUTH_TOKEN`.
- Edge case: an end-to-end flow with both env and file auth proves env precedence over stored auth.
- Regression: docs no longer teach `./bin/tunnel claude` as the normal launcher path.
- Test expectation: README/AGENTS/CLAUDE/architecture/local-e2e wording matches the final CLI contract for `run`, `auth`, and auth-source precedence.

**Verification:**
- The repo’s docs and acceptance coverage describe the same CLI contract the code now implements.

## System-Wide Impact

- **Interaction graph:** the largest change is local CLI structure; relay APIs and `/agent/ws` semantics stay unchanged.
- **Secret handling:** machine-local plaintext token storage is introduced, but the stored secret surface is intentionally bounded to one agent token and local metadata only.
- **Migration surface:** every existing example, doc reference, and user muscle memory that assumes `tunnel <command>` needs explicit migration.
- **Testing impact:** the plan adds new unit seams (store, prompt, auth API client) and at least one CLI-level E2E path.
- **Unchanged invariants:** relay startup, connector registration, attach/session protocol, and app-facing auth endpoints remain the same after token resolution succeeds.

## Risks & Dependencies

| Risk | Mitigation |
|---|---|
| Legacy users get a confusing `unknown command` failure after the CLI break | Intercept legacy direct-launch invocations and emit migration guidance that points to `tunnel run <command>` explicitly |
| Plaintext local token storage leaks broader auth state than necessary | Persist only the agent token and local metadata, use `0700`/`0600` permissions, and do not store app access or refresh tokens |
| `auth status` becomes misleading when env and file both exist | Use a source-aware JSON shape with explicit `active_source` and per-source availability |
| Password prompting is hard to test and easy to couple to TTY specifics | Add a small prompt abstraction so tests can inject credentials while production uses `golang.org/x/term` |
| Empty future config path creates unnecessary file-state permutations | Reserve `config.json` in code/docs only; do not create it yet |
| Docs drift because many examples currently use the old launcher shape | Treat README, architecture docs, local E2E docs, AGENTS, and CLAUDE as part of the same delivery unit |

## Documentation / Operational Notes

- `README.md` must teach `tunnel auth login`, `tunnel run`, and env-overrides-file precedence.
- `docs/architecture.md` should describe the new local CLI auth resolution model because the current wording says `tunnel` starts from `TUNNEL_AUTH_TOKEN`.
- `docs/local-e2e.md` should update the manual flow so “create agent token over HTTP, export env var” is no longer the only local developer path.
- `AGENTS.md` and `CLAUDE.md` must update the top-level `tunnel` startup guidance to reflect `run`, local auth fallback, and the non-persisted `base_url` rule.
- `docs/api.md` should remain unchanged unless implementation exposes a real app-facing contract delta; this plan consumes existing relay APIs only.

## Sources & References

- **Origin document:** `docs/brainstorms/2026-04-17-tunnel-auth-run-requirements.md`
- Related code: `cmd/tunnel/cmd.go`
- Related code: `cmd/tunnel/args.go`
- Related code: `cmd/tunnel/main.go`
- Related tests: `cmd/tunnel/args_test.go`
- Related tests: `cmd/tunnel/main_test.go`
- Related pattern: `cmd/relay/command.go`
- Related API helper pattern: `internal/e2e/client.go`
- Related relay response types: `internal/relay/handler/types/auth.go`
- Related relay response types: `internal/relay/handler/types/agent_token.go`
- Related docs: `README.md`
- Related docs: `docs/architecture.md`
- Related docs: `docs/local-e2e.md`
- Related docs: `AGENTS.md`
- Related docs: `CLAUDE.md`
