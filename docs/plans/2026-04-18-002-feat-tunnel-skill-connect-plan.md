---
title: Tunnel skill for connecting a running Claude session to the relay
type: feat
status: active
date: 2026-04-18
origin: docs/brainstorms/2026-04-18-coding-agent-skill-connect-requirements.md
---

# Tunnel skill for connecting a running Claude session to the relay

## Overview

Add a Claude Code skill that lets a user running an untunneled `claude` session generate a one-liner to relaunch the same conversation under `tunnel run claude --resume <id>`, making it visible on the companion mobile app. Add `connect` and `status` actions implemented as new `tunnel skill ...` subcommands in the existing Go CLI. Extend `tunnel run` to export session-identifying env vars into the child process so the skill can reliably detect whether the current session is already tunneled.

v1 is local-only and Claude-specific: the skill is authored in this repo and hand-installed into `~/.claude/skills/tunnel/` for local testing. Distribution and additional agent integrations are explicitly deferred.

## Problem Frame

Today, only sessions launched via `tunnel run claude` are visible on the mobile app. If a user has already started `claude` without tunnel, there is no in-place way to retrofit relay visibility — a skill running inside the agent cannot hijack its parent's PTY without tmux or fragile ptrace-style tooling. The workable general path across coding agents is a session-resume teleport: capture the conversation id, exit, and relaunch under tunnel. A skill is the natural surface for that workflow. (See origin: `docs/brainstorms/2026-04-18-coding-agent-skill-connect-requirements.md`.)

## Requirements Trace

**Skill shape**
- R1. Skill named `tunnel` with two actions, `connect` and `status`, invocable inside Claude Code.
- R2. `description:` metadata phrased for Claude auto-invoke on "connect this session / make visible on mobile / connection status" intents (implemented in Unit 5).

**`connect` action**
- R3/R4. `connect` short-circuits when already tunneled; otherwise prints a single resume one-liner.
- R5/R6. `connect` performs no installation, auth, or daemon management; output is terse (one line + two-line instruction).

**`status` action**
- R7/R8. `status` reports a session layer (env vars) and a device layer (daemon control socket), degrading gracefully when the daemon is down; no relay calls.

**Detection**
- R9. Detection combines `TUNNEL_SESSION_ID` + `TUNNEL_SESSION_PID` env vars with ancestor-PID validation to reject stale vars.

**Local build + test**
- R10/R11/R12. v1 is a local, hand-installed Claude skill; code structured so future multi-agent/distribution work is additive.

## Scope Boundaries

- No in-place PTY takeover; no tmux, ptrace, reptyr, or terminal-window spawning.
- No skill installation, auth management, or daemon start/stop from the skill.
- No Claude marketplace manifest, no install script, no public skills repo in v1.
- No new relay protocol or API changes.
- No skills for agents other than Claude Code in v1.
- No attempt to define one universal resume command across agents in v1; only the Claude command shape is implemented here.

## Context & Research

### Relevant Code and Patterns

- **PTY child process env wiring:** `internal/tunnel/session/process.go:31` — `StartCommandWithInitialSinks` sets `cmd.Env = append(os.Environ(), "TERM=xterm-256color")`. This is the only place tunnel customizes child env today.
- **`tunnel run` entry point:** `cmd/tunnel/main.go:158` generates `sessionID`; `cmd/tunnel/main.go:197` invokes `startSession` (aliased to `StartCommandWithInitialSinks` at `cmd/tunnel/main.go:54`). Both session id and PID (`os.Getpid()`) are trivially available at this call site. Base URL is already resolved via `TUNNEL_BASE_URL` flag/env handling earlier in the run flow.
- **Cobra subcommand pattern:** `cmd/tunnel/cmd.go:58-61` adds `run`, `auth`, `daemon`, `version` subcommands via `root.AddCommand(newXxxCmd(...))`. `newAuthCmd` (`cmd/tunnel/cmd.go:109`) is a good template for a multi-subcommand tree with its own `AddCommand` calls — the `skill` tree will mirror this shape.
- **Daemon control-socket client:** `internal/tunnel/daemon/control.go:127` defines a client helper that sends `Request{Action: actionStatus}` over the unix socket; `control.go:181` handles the 2-second dial timeout; `StatusInfo` (`control.go:21-37`) is the JSON shape returned. `tunnel skill status` can call the existing client directly — no daemon-side changes needed.
- **Path resolution for socket:** look at how `tunnel daemon status` already finds and dials the socket (same package).

### Institutional Learnings

- No existing `docs/solutions/` entry covers this directly. The closest adjacent context is the daemon + device-launch design (`2026-04-18-001-feat-mobile-device-tmux-workspace-plan.md`), which established that device-side state is daemon-local and that the daemon control socket is the right channel for skill-type local queries.

### External References

- Claude Code skills reference: [Extend Claude with skills](https://code.claude.com/docs/en/skills). A skill is a folder with `SKILL.md` (YAML frontmatter + instructions); Claude dispatches to it based on the `description:` field, and the skill body can call shell commands.
- Claude Code plugin/marketplace path: [Create and distribute a plugin marketplace](https://code.claude.com/docs/en/plugin-marketplaces). Captured for reference only; not used in v1.

## Key Technical Decisions

- **Logic in the `tunnel` Go binary, not shell:** `tunnel skill connect` and `tunnel skill status` own all behavior. SKILL.md is a thin wrapper that invokes them. Reasons: cross-platform ancestor-PID walk is clean in Go (`gopsutil` or a small platform shim) vs painful in POSIX shell; the daemon-client Go code is already there and returns typed `StatusInfo`; no `jq` or other runtime deps; easy to unit-test. (See origin: `[Affects R10][Technical]`.)
- **Export three env vars from `tunnel run`:** `TUNNEL_SESSION_ID`, `TUNNEL_SESSION_PID` (the tunnel process's own PID — `os.Getpid()`), and `TUNNEL_BASE_URL`. Only the first two gate detection; base URL is carried for `status` display and future use. `TUNNEL_CONNECTED=1` is intentionally omitted — the two identifying vars are sufficient, and a boolean flag would be redundant and drift-prone.
- **Ancestor-PID walk with process-name check:** reading `TUNNEL_SESSION_PID`, the skill walks its own parent chain (macOS: `ps -o ppid=,comm= -p <pid>` via `os/exec`, Linux: `/proc/<pid>/status` `PPid:` + `Name:` fields) until it finds the PID or exits the chain. Validation requires the PID to be (1) alive, (2) an ancestor, (3) named `tunnel`. All three must hold. Stale vars inherited from prior shells or manually exported ones fail on (2) or (1).
- **Daemon call from `status` is best-effort with a 2-second timeout:** reuses the existing dial timeout in `internal/tunnel/daemon/control.go:181`. If the daemon is unreachable, the session-layer output still prints and the device-layer line reads "daemon: not running."
- **Skill source lives under `skills/claude/tunnel/` in this repo:** versioned alongside the code that implements its subcommands, so the SKILL.md, the Go subcommands, and the env-var contract change together. Install is manual for v1 (symlink or copy to `~/.claude/skills/tunnel/`). Distribution path chosen later.
- **Resume command rendering is agent-specific:** v1 ships only the Claude renderer, which outputs `tunnel run claude --resume <id>`. The skill should emit a normal shell command, not `exec`, so exiting the resumed Claude session returns the user to the same shell instead of replacing it. We rely on `claude --resume` restoring model/flag choices from saved session state; if it turns out it doesn't, the Claude renderer gains extra args via a deferred implementation decision (see Open Questions). Future Gemini/Codex work must plug in their own renderer rather than reuse the Claude string.

## Open Questions

### Resolved During Planning

- Where logic lives → in the `tunnel` binary as `skill` subcommands.
- Env var shape → `TUNNEL_SESSION_ID`, `TUNNEL_SESSION_PID`, `TUNNEL_BASE_URL`.
- How `status` handles missing daemon → best-effort, 2-second timeout, degraded device-layer line.
- Flag name for Claude's conversation id → `--resume-id` (not `--session-id`), to keep it distinct from tunnel's own `session_id`.
- Ancestor-walk I/O error behavior → fail-closed with diagnostic, exit 0 (prevents destroying a live tunneled session on a transient probe error).

### Resolved Before Implementation (Unit 0 spike)

- Claude Code's mechanism for a skill to obtain the current conversation id. Unit 0 is the spike; its outcome determines SKILL.md wiring in Unit 5 and whether the UX flow needs a user-paste fallback.

### Deferred to Implementation

- **Cross-platform parent-PID walk library choice:** `github.com/shirou/gopsutil/v3` vs a minimal handwritten shim that shells out to `ps` on macOS and reads `/proc` on Linux. Decide during implementation based on existing module dependencies; prefer the handwritten shim if gopsutil is not already a dependency, to keep binary size small.
- **Process name match string:** on macOS `comm` may be `tunnel` (when installed as `tunnel`) or the full path. Implementation should match on basename, and include integration tests covering both shapes.
- **`claude --resume` flag fidelity:** verify at implementation time whether `--resume` restores model/flags or whether extra args must be replayed. The resume-one-liner template is a single function in Unit 3, so changing its shape is a one-place fix.
- **Shell-safe handling of the Claude resume id:** do not hard-code an allowlist until Unit 0 confirms the real id format. Implementation should either quote/escape the value safely for the emitted shell command or, if Unit 0 proves the id format is narrower, add validation that matches the verified format rather than an assumed one.
- **Future agent targets:** Gemini CLI and Codex CLI appear to have relevant skill/command and resume surfaces, but their integration shapes differ enough that they should be treated as follow-on work, not bundled into this Claude-first plan.
- **Whether to also include `TUNNEL_RUN_ARGV` in the exported env:** could help future in-place replay, but out of scope for v1.

## High-Level Technical Design

> *This illustrates the intended approach and is directional guidance for review, not implementation specification. The implementing agent should treat it as context, not code to reproduce.*

```
┌─────────────────────────── claude (running, untunneled) ───────────────────┐
│                                                                            │
│  user asks: "connect this to tunnel"                                       │
│    └─► Claude invokes SKILL.md                                             │
│          └─► shells out:  tunnel skill connect --resume-id <claude-id>    │
│                                    │                                       │
│                                    ▼                                       │
│       ┌────────────────── tunnel skill connect ──────────────────┐         │
│       │ 1. read TUNNEL_SESSION_ID, TUNNEL_SESSION_PID from env   │         │
│       │ 2. ancestor-PID walk: find PID in parent chain,          │         │
│       │    check alive + process-name == "tunnel"                │         │
│       │ 3a. if all checks pass → print:                          │         │
│       │        Already connected to tunnel relay                 │         │
│       │        (session <id>, base <url>).                       │         │
│       │ 3b. otherwise → print:                                   │         │
│       │        tunnel run claude --resume <claude-id>            │         │
│       │        then: "/exit Claude, then paste above"            │         │
│       └──────────────────────────────────────────────────────────┘         │
└────────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────── tunnel run claude (owned PTY) ──────────────────┐
│                                                                            │
│  StartCommandWithInitialSinks:                                             │
│    cmd.Env = os.Environ()                                                  │
│               + TERM=xterm-256color                                        │
│               + TUNNEL_SESSION_ID=<id>                                     │
│               + TUNNEL_SESSION_PID=<tunnel-pid>                            │
│               + TUNNEL_BASE_URL=<url>                                      │
│                                                                            │
│  ─► child claude inherits these; skill inside sees them.                   │
└────────────────────────────────────────────────────────────────────────────┘

tunnel skill status layering:
    session layer: env vars  → printed always (or "not connected")
    device layer:  daemon unix socket (2s timeout)
                    → printed when daemon running
                    → "daemon: not running" otherwise
    (no relay I/O, ever)
```

## Implementation Units

- [ ] **Unit 0: Verify Claude Code session-id capture mechanism (spike)**

**Goal:** Confirm how a Claude Code skill obtains the *current* Claude conversation id from inside a running session, before committing to the SKILL.md wiring in Unit 5. This is load-bearing for R4: if no stable channel exists, the resume one-liner cannot be constructed without user involvement.

**Requirements:** R4 (blocking).

**Dependencies:** None.

**Outputs:**
- Written decision captured in this plan's "Resolved During Planning" section, selecting one of:
  1. Claude exposes a well-known env var (e.g. `CLAUDE_SESSION_ID`) — SKILL.md passes it through; flow unchanged.
  2. Claude writes a session-state file at a known path — SKILL.md reads and passes it through.
  3. No programmatic capture — SKILL.md body instructs Claude to emit the id it already knows into the command, OR asks the user to paste it. UX flow documented in Unit 5 updates accordingly.
- If fallback (3) is taken, update the High-Level Technical Design diagram and Unit 5 verification steps to reflect the user-action change.

**Approach:** Check Claude Code's skill docs, inspect a running `claude` session's environment and local state files (`~/.claude/...`), and run a trivial skill that dumps env + relevant file contents to confirm. Time-box to one investigation session.

---

- [ ] **Unit 1: Export session env vars from `tunnel run` into child env**

**Goal:** Make `TUNNEL_SESSION_ID`, `TUNNEL_SESSION_PID`, and `TUNNEL_BASE_URL` available to the process launched by `tunnel run` so the skill can detect the tunneled state.

**Requirements:** R9 dependency.

**Dependencies:** None.

**Files:**
- Modify: `internal/tunnel/session/process.go` (extend `StartCommandWithInitialSinks` signature to accept caller-supplied extra env, preserving today's `TERM` wiring)
- Modify: `cmd/tunnel/main.go` (build the three env entries at the call site that already knows `sessionID`, `os.Getpid()`, and the resolved base URL; pass them through)
- Modify: `cmd/tunnel/main_test.go` (three existing fixtures at `main_test.go:1127`, `main_test.go:1178`, `main_test.go:1230` call `session.StartCommandWithInitialSinks`; update each to the new signature — verified via `grep -n StartCommandWithInitialSinks cmd/tunnel`)
- Test: `internal/tunnel/session/process_test.go` (add coverage that extra env reaches the child)
- Test: `cmd/tunnel/main_test.go` (assert the three env vars are populated in the extra-env slice passed to the fake)

**Approach:**
- Prefer a small signature extension (an `extraEnv []string` parameter) over an options struct — minimal ripple, easy to reason about.
- The PID to export is the tunnel process's own (`os.Getpid()`), not the PTY child's. The ancestor walk will find this PID in the child's parent chain.
- Base URL should be the already-resolved value (what `tunnel run` is about to use), not the raw env var — this keeps `status` output consistent with whatever the process actually connected to.

**Patterns to follow:**
- Env var constant declarations: `cmd/tunnel/args.go:12-14` (`tunnelBaseURLEnv`, `tunnelAuthTokenEnv`, `tunnelLaunchRequestIDEnv`). Define new constants there for consistency.

**Test scenarios:**
- Happy path: `StartCommandWithInitialSinks` with non-empty `extraEnv` passes those entries through `cmd.Env` to the child (verify via a child that writes `env` output into a sink).
- Happy path: `tunnel run` wires all three expected env vars into the extra-env slice with the correct values (session id matches the generated id, PID matches `os.Getpid()`, base URL matches the resolved URL).
- Edge case: nil/empty `extraEnv` preserves current behavior — only `TERM=xterm-256color` is appended.
- Edge case: when the user's shell already exports `TUNNEL_SESSION_ID` or `TUNNEL_SESSION_PID` (stale or user-set), the tunnel-supplied values must win. Verify by constructing `cmd.Env` as `os.Environ()` first, then appending `TERM` and the three session vars *last* (Go's `os/exec` uses last-wins for duplicate keys on Unix). Add a dedicated test that seeds the parent environ with a conflicting `TUNNEL_SESSION_ID` and asserts the child sees the tunnel-supplied value.

**Verification:**
- Launching `tunnel run env` prints the three env vars with expected values.
- Existing `tunnel run` tests still pass with the new signature.

---

- [ ] **Unit 2: Ancestor-PID walk helper**

**Goal:** A small, cross-platform helper that, given a target PID, tells the caller whether that PID (a) is alive, (b) is in the current process's parent chain, and (c) has a process name matching `tunnel`.

**Requirements:** R9.

**Dependencies:** None.

**Files:**
- Create: `internal/tunnel/skill/ancestor.go` (public `VerifyAncestor(targetPID int, wantName string) (bool, error)` plus unexported helpers)
- Create: `internal/tunnel/skill/ancestor_darwin.go` (macOS parent/name lookup via `ps -o ppid=,comm= -p <pid>`)
- Create: `internal/tunnel/skill/ancestor_linux.go` (Linux parent/name lookup via `/proc/<pid>/status`)
- Test: `internal/tunnel/skill/ancestor_test.go` (table-driven tests exercising the walk logic with a fake `parentOf` function; platform-specific tests behind build tags to verify real lookups)

**Approach:**
- Factor the walk and platform lookup so the walk is testable with injected `parentOf(pid) (ppid int, name string, alive bool, err error)`.
- Match process name by basename: e.g. `/usr/local/bin/tunnel` and `tunnel` both count.
- Cap the walk at a reasonable depth (say 32) to avoid pathological loops if the parent chain is somehow cyclic.
- Return `(false, nil)` for expected "not found" outcomes; reserve `error` for unexpected I/O failures.

**Patterns to follow:**
- Platform-split files via `_darwin.go` / `_linux.go` (build tags) — mirrors the existing pattern in `internal/tunnel/daemon/process_unix.go` / `process_other.go`.

**Test scenarios:**
- Happy path: injected chain where `targetPID` appears two hops up with name `tunnel` → returns true.
- Happy path: injected chain where `targetPID` appears at the immediate parent → returns true.
- Edge case: `targetPID` not present in the chain → returns false.
- Edge case: `targetPID` present but process reports not alive → returns false.
- Edge case: `targetPID` present and alive but name is `bash` (stale PID reuse) → returns false.
- Edge case: name match on basename works for both `tunnel` and `/usr/local/bin/tunnel`.
- Edge case: walk depth cap triggers on a pathological injected chain → returns false with no panic.
- Error path: `parentOf` returns a real I/O error mid-walk → error propagated.
- Integration (darwin, behind build tag): real `ps` invocation against the current process's own parent tree returns plausible results.
- Integration (linux, behind build tag): real `/proc` read against the current process's own parent tree returns plausible results.

**Verification:**
- `go test ./internal/tunnel/skill/...` passes on both macOS and Linux.

---

- [ ] **Unit 3: `tunnel skill connect` subcommand**

**Goal:** Implement the Claude-specific `connect` action for v1: detect tunneled state via env + ancestor walk; print either "already connected" or the Claude resume one-liner with `/exit` + paste instructions.

**Requirements:** R1, R3, R4, R5, R6, R9, R10.

**Dependencies:** Unit 1 (env vars must be exported before detection can succeed), Unit 2 (ancestor walk).

**Files:**
- Create: `cmd/tunnel/skill_cmd.go` (new `newSkillCmd()` returning a cobra command with `connect` and `status` children; wire in `cmd/tunnel/cmd.go`)
- Modify: `cmd/tunnel/cmd.go` (add `root.AddCommand(newSkillCmd())` alongside the existing run/auth/daemon/version adds)
- Test: `cmd/tunnel/skill_cmd_test.go` (table-driven tests that fake the env reader and ancestor-walk verifier, assert exact stdout for each branch)

**Approach:**
- Accept `--resume-id <id>` as a required flag on `connect`. The SKILL.md passes through Claude's current conversation id. Flag is named `--resume-id` (not `--session-id`) to avoid collision with tunnel's own `session_id` concept — the value here identifies the *Claude* conversation, not the tunnel session.
- **Do not freeze an allowlist before Unit 0 confirms the real Claude id format:** the primary requirement is that the emitted one-liner stays shell-safe when copy-pasted. If Unit 0 shows the id can contain shell-significant bytes, the renderer must quote/escape it correctly for the supported shell target; if Unit 0 shows the format is already narrow, add validation that matches the verified format rather than an assumed regex.
- Inject env reader and ancestor verifier via command-local vars (the pattern `osEnv = os.Getenv` at `cmd/tunnel/auth_store.go:23` is the convention here) so tests can swap them.
- **Stream convention (applies uniformly to both `connect` and `status`):** the single copy-pasteable command goes to stdout, so `$(tunnel skill connect --resume-id ...)` captures exactly the one-liner. All human-oriented messages (instructions, "already connected" notices, diagnostics, errors) go to stderr. Exit 0 for expected outcomes including "already connected" and "not connected." Non-zero only on genuine usage errors (missing flag, invalid `--resume-id`).
- Keep resume rendering behind one helper even in v1, but keep the scope narrow: name and document it as the Claude resume renderer, or equivalently as a tiny private helper for `claude --resume`, not as a generalized agent registry. The only purpose of the seam is to avoid rewriting `connect` later.
- Output shape when already connected (stderr only; stdout empty; exit 0):

  ```
  Already connected to tunnel relay (session <id>, base <url>).
  ```

- Output shape when not connected (stdout line 1, stderr line 2; exit 0):

  ```
  tunnel run claude --resume <resume-id>
  # Exit this Claude session (/exit), then paste the line above into your shell.
  ```
- The emitted command is produced by a single Claude-specific helper so the deferred "does `claude --resume` need extra args" decision is a one-line change.

**Patterns to follow:**
- Multi-subcommand cobra shape: `newAuthCmd` at `cmd/tunnel/cmd.go:109` — `authCmd.AddCommand(newAuthLoginCmd(...))` and sibling commands.
- Env-var-backed injection for testability: `cmd/tunnel/auth_store.go:23` (`osEnv = os.Getenv`).

**Test scenarios:**
- Happy path: `TUNNEL_SESSION_ID` and `TUNNEL_SESSION_PID` present, ancestor verifier returns true → prints "already connected" with session id and base URL.
- Happy path: env vars absent → prints resume one-liner with the `--resume-id` value; stderr carries the instruction line.
- Happy path: env vars present, ancestor verifier returns false (stale) → prints resume one-liner (treats env as not connected).
- Edge case: `TUNNEL_SESSION_ID` present but `TUNNEL_SESSION_PID` absent → prints resume one-liner (treats as not connected).
- Edge case: `TUNNEL_SESSION_PID` is non-numeric garbage → prints resume one-liner (treats as not connected, does not crash).
- Edge case: `--resume-id` flag omitted → cobra returns a usage error; exit code non-zero; stderr shows the flag requirement.
- Edge case: `--resume-id` value contains shell-special characters or whitespace → the emitted one-liner remains shell-safe and copy-pasteable because the renderer quotes/escapes the value correctly, unless Unit 0 proves those bytes are impossible and a narrower validated format is adopted.
- Error path: ancestor verifier returns a real I/O error → command fails *closed* (does not print the resume one-liner that would tell a possibly-tunneled user to `/exit`). Instead prints a diagnostic on stderr: `could not verify tunnel state (<reason>); if this session is not already tunneled, retry or run \`tunnel skill status\` for details`, and exits 0. Fail-open would risk destroying a live tunneled session on a transient `ps` hiccup.

**Verification:**
- `tunnel skill connect --resume-id abc` in a clean shell prints the resume one-liner.
- Same command run inside `tunnel run bash`, then `tunnel skill connect --resume-id abc`, prints "already connected."

---

- [ ] **Unit 4: `tunnel skill status` subcommand**

**Goal:** Implement the `status` action: print session layer (env-derived) and device layer (daemon probe), degrading gracefully when the daemon is not running.

**Requirements:** R1, R7, R8.

**Dependencies:** Unit 1, Unit 2, Unit 3 (reuses the cobra tree and injected env reader/ancestor verifier).

**Files:**
- Modify: `cmd/tunnel/skill_cmd.go` (add `newSkillStatusCmd()`; wire into `newSkillCmd`)
- Test: `cmd/tunnel/skill_cmd_test.go` (extend with status-specific tests, including a fake daemon-status client)

**Approach:**
- Reuse the existing daemon client at `internal/tunnel/daemon/control.go:127` (the status-returning helper). Inject it via a command-local `statusFn = daemon.Status` so tests can fake the response and the error.
- Dial timeout stays at the existing 2 seconds; no new timeout.
- Output shape (human-friendly, two labeled blocks — we're not designing a machine-parseable format yet):

  ```
  session:
    connected:   yes
    session_id:  1718035812345678901
    base_url:    https://diaro.me

  device:
    daemon:      running
    device_id:   dev_abc123
    display:     yuanbo-mbp
    platform_id: macos
    relay:       connected
    launch:      healthy
  ```

- When daemon unreachable: `daemon:      not running` and omit the other device fields.
- When session not tunneled: `connected:   no` and omit `session_id`/`base_url`.

**Patterns to follow:**
- Same injection pattern as Unit 3.
- `StatusInfo` field mapping: `internal/tunnel/daemon/control.go:21-37`.

**Test scenarios:**
- Happy path: session env present (and ancestor check passes) + daemon returns a full `StatusInfo` → both blocks print all fields.
- Happy path: session env absent + daemon reachable → session block says `connected: no`; device block still prints.
- Happy path: session env present + daemon unreachable (`ErrNotRunning`) → session block prints; device block shows `daemon: not running`.
- Edge case: daemon client returns a non-`ErrNotRunning` error (timeout, socket permission denied) → device block shows `daemon: unavailable (<reason>)`; session block unaffected.
- Edge case: `StatusInfo.RelayConnected` false → `relay: disconnected`.
- Edge case: `StatusInfo.LaunchHealth` empty string → `launch:` line omitted rather than printed blank.
- Integration: `tunnel skill status` end-to-end against a locally-running daemon on the author's machine prints expected device fields.
- Exit-code invariant: every scenario above exits 0; non-zero is reserved for genuine usage errors (unknown subcommand, unexpected panic).

**Verification:**
- `tunnel skill status` outside a tunneled session and with no daemon prints the expected degraded form with exit code 0.
- Same command inside `tunnel run bash` with the daemon running prints both blocks populated.

---

- [ ] **Unit 5: Claude Code SKILL.md + local install instructions**

**Goal:** Author the Claude-only `SKILL.md` and supporting files that Claude Code loads; document how to install them into `~/.claude/skills/tunnel/` for local testing.

**Requirements:** R1, R2, R10, R11.

**Dependencies:** Unit 0 (session-id capture decision), Units 3 and 4 (the subcommands must exist for the SKILL to call).

**Files:**
- Create: `skills/claude/tunnel/SKILL.md` (YAML frontmatter — `description:` written so Claude auto-invokes on phrases like "connect this session to the tunnel relay", "show tunnel status"; body: short instructions + the two shell invocations)
- Create: `skills/claude/README.md` (one-page local-install guide: `ln -s $(pwd)/skills/claude/tunnel ~/.claude/skills/tunnel`, verification step, and what to expect)

**Approach:**
- SKILL.md body directs Claude to invoke `tunnel skill connect --resume-id "<claude current conversation id>"` or `tunnel skill status`, then show the output to the user verbatim. The exact session-id capture technique is the output of Unit 0 — wire whatever channel Unit 0 resolved (env var passthrough, state-file read, or user-action fallback).
- Do not add Gemini/Codex branches, examples, or fallback prose to this v1 skill file. If future agent support is added, it should land in separate agent-specific skill assets and a separate plan, not by turning the Claude SKILL.md into a multi-agent dispatcher.
- Keep the `description:` field single-sentence and specific. A drifting or vague description is the most common reason Claude fails to auto-invoke. Draft to iterate on during implementation:

  > Connect the current coding-agent session to the tunnel relay so it is visible and continuable on the companion mobile app, or report tunnel connection status. Invoke when the user says things like "connect this session to tunnel," "make this visible on my mobile app," or "show tunnel status."
- README install step uses a symlink so edits to `skills/claude/tunnel/SKILL.md` in the repo are picked up live on the author's machine.

**Patterns to follow:**
- Claude Code skill structure per [Extend Claude with skills](https://code.claude.com/docs/en/skills) (frontmatter + markdown body).

**Test scenarios:**
<!-- No behavioral logic in the skill file itself beyond the shell-out; testing is done through the subcommands (Units 3–4) and a manual end-to-end walkthrough. -->
- Test expectation: none -- SKILL.md is a thin wrapper whose behavior is covered by the Unit 3 and Unit 4 test suites plus the manual end-to-end verification below.

**Verification:**
- From a fresh local `claude` session (no tunnel), asking "make this session visible on my mobile app" causes Claude to invoke the skill and display the resume one-liner.
- After `/exit` and pasting the one-liner, a new `tunnel run claude --resume <id>` starts with the conversation preserved, and the session is visible on the mobile app.
- Inside that tunneled session, "show tunnel status" produces the populated two-block output.
- Inside the same session, "connect to tunnel" produces the "already connected" short-circuit.

---

- [ ] **Unit 6: Docs update for the env-var contract**

**Goal:** Document the new exported env vars and the skill subcommands so future work and external users (or skill authors) can depend on the contract.

**Requirements:** Supports R9 and R12 (future-additive structure).

**Dependencies:** Units 1, 3, 4 (contract must match code).

**Files:**
- Modify: `docs/architecture.md` (new short section: "Env vars exported to `tunnel run` children", listing `TUNNEL_SESSION_ID`, `TUNNEL_SESSION_PID`, `TUNNEL_BASE_URL` with semantics and the ancestor-PID guard expectation)
- Modify: `README.md` (one-line mention of the `tunnel skill` subcommand tree and a pointer to `skills/claude/README.md`)
- Modify: `CLAUDE.md` and `AGENTS.md` (add to the `Start Here` list: `cmd/tunnel/skill_cmd.go` owns the skill subcommands; note the `TUNNEL_SESSION_*` env contract)

**Approach:**
- Keep the env-var section in `docs/architecture.md` small and behaviorally focused — what each var means, who sets it, who reads it, that they are per-process (not global), and that `TUNNEL_SESSION_PID` pairs with an ancestor-PID check to defeat stale values.
- `CLAUDE.md` / `AGENTS.md` note per the existing convention in those files (one line each, fitting the `Start Here` bullet style).

**Patterns to follow:**
- Existing `Start Here` bullet style in `CLAUDE.md` and `AGENTS.md`.

**Test scenarios:**
- Test expectation: none -- documentation-only unit.

**Verification:**
- Env-var names and semantics in `docs/architecture.md` exactly match the implementation in Unit 1.
- `CLAUDE.md` Start Here list includes the new file ownership entry.

## System-Wide Impact

- **Interaction graph:** `tunnel run`'s child-env setup changes shape; every caller of `session.StartCommandWithInitialSinks` must match the new signature (three call sites in `cmd/tunnel/main_test.go` plus one in `cmd/tunnel/main.go`). No relay-side surfaces are affected.
- **Error propagation:** The skill subcommands are local-only; failures (daemon unreachable, env malformed) do not propagate to the relay or to other sessions. `status` and `connect` both degrade in-place and exit 0 for expected "not connected" / "daemon down" cases; non-zero only on genuine usage errors.
- **State lifecycle risks:** The exported env vars are per-process and die with the tunnel run. Stale-state risk is exactly what the ancestor-PID check defeats; there is no shared state to clean up elsewhere.
- **API surface parity:** No public HTTP or websocket contract changes. The new "public contract" is the child-env shape, documented in `docs/architecture.md`. Any future non-Claude agent skill must read the same three vars.
- **Agent scope:** Only Claude Code is implemented in this plan. Supporting Gemini CLI or Codex CLI is intentionally deferred because their skill discovery and resume UX differ enough to deserve their own validation and command-rendering rules.
- **Integration coverage:** Mocked unit tests will not prove that Claude Code actually auto-invokes the skill based on the `description:` — that is verified manually on the author's machine per Unit 5.
- **Unchanged invariants:** `tunnel run` startup semantics, relay/agent protocol, attach behavior, and device-launch flow are untouched. Only the child process environment gains three vars.

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| Claude Code does not expose the current session id to skills in any stable way | Deferred to implementation (Unit 5). Fallback: skill body asks the user to supply the id, or reads Claude's local session-state file. Does not block Units 1–4. |
| `claude --resume <id>` drops model/flag choices; user's resumed session silently differs | Deferred. Fix is a one-line change in the resume-template function (Unit 3). |
| Cross-platform parent-PID walk has edge cases (shells that double-fork, `nohup`, process re-parenting to PID 1) | Covered by the depth cap + name check; unit tests enumerate these. On unexpected I/O error, `connect` fails *closed* (prints a diagnostic rather than the resume one-liner) to avoid telling a tunneled user to `/exit`. |
| User renamed the `tunnel` binary (e.g. `tunnel-dev`) → ancestor name check fails, `connect` reports not-connected inside a real tunneled session | Documented assumption in Unit 2: process-name match expects the default basename. If users ship renamed binaries, the skill will produce a false "not connected" reading and a fresh resume would still work (no destructive outcome). Revisit if this becomes a real workflow. |
| User's agent binary is not literally `claude` (e.g. wrapper alias, `claude-code`, custom launcher) → resume one-liner runs the wrong binary | Unit 3 template hard-codes `claude` for v1. Document the assumption in Unit 5's README; future work could make the agent binary a flag on `tunnel skill connect`. |
| The design gets over-generalized too early and mixes Claude, Gemini, and Codex assumptions into one path | Keep this plan Claude-only. Preserve only the minimal seam for a future agent-specific renderer so later support is additive rather than speculative abstraction. |
| `TUNNEL_SESSION_PID` is reused by the OS after tunnel exits, pointing at an unrelated live process | The process-name check rejects this (the reused PID's name won't be `tunnel`). Enumerated in Unit 2 test scenarios. |
| Skill source in `skills/claude/` drifts from the `tunnel` binary contract | Unit 6 docs the env-var contract; versioning both in the same repo/commit keeps them lockstep. |

## Documentation / Operational Notes

- Once Unit 6 lands, the env-var contract should be treated as part of the tunnel/skill compatibility line. Per `CLAUDE.md`, the pre-v1 compatibility line is `0.minor` (e.g. `v0.1.x` and `v0.2.x` are distinct lines), so breaking changes to `TUNNEL_SESSION_*` require a `0.minor` bump while we remain pre-v1.
- No monitoring or rollout machinery required — this is a CLI-local change.

## Sources & References

- **Origin document:** [docs/brainstorms/2026-04-18-coding-agent-skill-connect-requirements.md](../brainstorms/2026-04-18-coding-agent-skill-connect-requirements.md)
- Related code: `internal/tunnel/session/process.go:31`, `cmd/tunnel/main.go:158`, `cmd/tunnel/cmd.go:58`, `internal/tunnel/daemon/control.go:21-37`, `cmd/tunnel/args.go:12-14`.
- External docs: [Extend Claude with skills](https://code.claude.com/docs/en/skills), [Create and distribute a plugin marketplace](https://code.claude.com/docs/en/plugin-marketplaces) (referenced for v2 distribution; not built in v1).
