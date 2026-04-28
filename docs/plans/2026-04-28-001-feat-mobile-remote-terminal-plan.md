---
title: 'feat: Add mobile remote terminal'
type: feat
status: active
date: 2026-04-28
origin: docs/brainstorms/2026-04-27-mobile-remote-terminal-requirements.md
---

# feat: Add mobile remote terminal

## Overview

Add an in-app remote interactive shell launchable from the mobile app's Devices tab. Each shell session is a normal `tunnel run <user_shell>` invocation, wrapped in the daemon's existing tmux workspace, distinguished from agent sessions by a new `SessionKind = "agent" | "shell"` field orthogonal to the existing `launch_source`. The mobile app gets a new top-level **Terminals** tab listing running shell sessions across devices with single-row stop and a tab-level Stop all action.

The plan deliberately reuses the entire `tunnel run` pipeline (PTY mirror, agent registration, attach websocket, snapshot, resize, tmux survival) rather than introducing a parallel terminal channel. New scope: a `kind` field threaded end-to-end, a `terminal_supported` device capability, daemon-side env-snapshot at startup, login-shell argv matrix, idle-timeout sweeper, stop-on-app-session-revocation cascade, structured audit log entries, and `cwd` validation.

This plan does not implement the mobile app UI (different repo). Mobile-side requirements are captured as contract obligations and deferred design notes.

## Problem Frame

Mobile users today can only attach to running agent sessions. They cannot start an ad-hoc shell on their machine from the phone — no `cd`, `git status`, or quick `git worktree add`. The product line is "honest in-app remote terminal": user's own shell config (zsh/bash/fish autosuggestion, history, prompt, alias, Tab completion) works on first tap; the mobile app exposes Ctrl/Alt soft keys plus its existing key set. Security can be relaxed relative to multi-user/SSH because the target is the user's own machine, but the launch endpoint still becomes RCE-equivalent for anyone holding a valid app-session token, which sets the bar for revocation, audit, and idle-cleanup behavior.

The brainstorm dialogue resolved the high-leverage decisions: reuse session model, wrap in `tunnel run`, `SessionKind` orthogonal field, default 12h idle, stop-on-revocation, audit logs. See origin: `docs/brainstorms/2026-04-27-mobile-remote-terminal-requirements.md`.

## Requirements Trace

All 22 requirements from the origin doc carry forward verbatim. Grouped here for plan structure:

**Protocol contract:** R1 (`SessionKind = agent | shell`), R2 (no new `launch_source` value).

**Daemon contract:** R3 (`terminal_supported` capability), R4 (shell-allowlist defaults), R5 (shell discovery + AllowedCommands gate on resolved name), R6 (interactive login shell argv matrix), R6a (env-equivalent-to-login propagation), R7 (`kind` on launch request), R8 (wrap in `tunnel run`), R9 (skip auto-update check on shell launches, key on `kind`).

**Launch defaults / cwd:** R10 (`$HOME` default), R11 (relay accepts empty `cwd` for `kind: shell`), R11a (cwd canonicalization, no symlink escape, rc files from `$HOME`).

**Mobile client:** R12 ("+ Terminal" affordance gated on capability), R13 (zero-form launch), R14 (Terminals tab), R15 (kind-only filter, no co-mingling), R16 (Ctrl/Alt sticky modifier toggles), R17 (modified Enter sends `\r`, no CSI u).

**Lifecycle:** R18 (persistence via tmux), R19 (12h idle, `0` to disable), R20 (stop on app-session revocation, daemon kills inner PTY child), R21 (audit logs metadata-only, denied-launch entries, ≥30d retention), R22 (per-row stop + tab-level Stop all).

## Scope Boundaries

- No separate ephemeral PTY channel; `tunnel run` wrapper is the only path.
- No shell-integration (no OSC 633, no command-finished hooks, no exit-code surfaces).
- No session-detail "open terminal here" entry; Devices tab is the only entry.
- No cwd picker / cwd history / shell picker on creation.
- No configurable keybar / macros / multi-row keyboard / two-finger gestures.
- No Kitty CSI u encoding for modified Enter or other modified keys.
- No per-command authorization, sandboxing, escape-sequence filtering, or per-device hard concurrency cap.
- No mobile UI implementation in this repo (contract only).

## Context & Research

### Relevant Code and Patterns

- **Protocol:** `internal/protocol/message.go` (`SessionInfo`, `LaunchContext`, `AgentFrame`); `internal/protocol/device.go` (`DeviceInfo`, `DeviceFrame`, `DeviceLaunchRequestFrame`). All struct fields are JSON-additive; older daemons / clients ignore unknown keys.
- **Daemon launch chain:** `internal/tunnel/daemon/connector.go` (`launchHandler.Handle`, `buildShellWrapper`, `resolveLaunchCWD`, AllowedCommands enforcement at lines 185-198, exec-login-shell tail at line 282); `internal/tunnel/daemon/tmux.go` (`CreateLaunchSession`, `TerminateWorkspaceSession`); `internal/tunnel/daemon/config.go` (`Config`, `defaultAllowedCommands`, `Allows`).
- **Daemon runtime / state:** `internal/tunnel/daemon/runtime.go` (snapshot pattern for status fields); `internal/tunnel/daemon/runtime_test.go`.
- **Tunnel run process:** `cmd/tunnel/main.go:160-274` (where `LaunchContext` is built from `--launch-source` / `--launch-request-id` flags, where `protocol.SessionInfo` is constructed for registration, and where the relay connector starts).
- **Relay launch handler:** `internal/relay/handler/api/devices.go` (HTTP `POST /api/devices/:id/launch`, current empty-cwd 400 at lines 41-44); `internal/relay/handler/types/device.go` (`DeviceLaunchRequest`); `internal/relay/device/registry.go` (`Registry.Launch`, `ResolveLaunchIfOwner`, `CompleteLaunchIfOwner`, in-flight enforcement); `internal/relay/handler/agent/ws.go` (registration + launch-correlation hook at lines 86-97); `internal/relay/handler/device/ws.go` (`SetLaunchSourceForUser` post-registration backfill at line 100).
- **Session registry & ownership:** `internal/relay/session/registry.go` (`SessionOwner{UserID, AgentTokenID}`, `RegisterOwned`, `SetLaunchSourceForUser`). `RegisterOwned` is the canonical pattern to extend for capturing additional metadata at registration time. `SetLaunchSourceForUser` is the canonical pattern for "user-scoped post-registration backfill" — `SetSessionKindForUser` follows the same shape.
- **Auth / revocation:** `internal/relay/auth/app_service.go` and tests (`RevokeReason = "password_changed"` in `app_service_test.go`). Logout / password-change already produces an app-session revocation event; the cascade hook is the new code surface.
- **Logging:** `internal/logx/logx.go` provides `logx.Info(event, fields...)` with structured slog JSON output. Existing relay events (`agent_ws_connected`, `agent_registered`, `agent_disconnected`) live in `internal/relay/handler/agent/ws.go`. Audit log entries use the same channel; the +30d retention requirement is achieved by adding a separate sink rather than changing the existing rolling log.
- **Existing wrapper precedent:** `connector.go:261-283` (`buildShellWrapper`) already threads launch metadata through env vars and a `tunnel run` invocation. The new wrapper for `kind: shell` keeps the env-restoration prefix/suffix and replaces the `tunnel run ...` middle with the shell launch.
- **Idle tracking precedent:** none. New code on the daemon's `runtimeState` plus a sweeper goroutine.
- **Prior plan precedent:** `docs/plans/2026-04-22-001-feat-unified-session-management-plan.md` for stop semantics; `docs/plans/2026-04-18-001-feat-mobile-device-tmux-workspace-plan.md` for the launch-correlation pattern; `docs/plans/2026-04-18-003-feat-session-platform-identity-plan.md` for `DeviceInfo` evolution.

### Institutional Learnings

- `docs/solutions/` does not exist in this repo. No prior write-ups for shell env propagation, idle sweeps, or revocation cascades. Treat those as greenfield with extra rigor.

### External References

- External research not required; paseo's mobile UI pattern (`packages/app/src/components/terminal-pane.tsx`) was already studied during brainstorming and the soft-keyboard / sticky-modifier pattern is the design reference. Login-shell argv matrix is documented public knowledge from each shell's man page (zsh, bash, fish, dash).

## Key Technical Decisions

- **`SessionKind` is a new orthogonal field, not a `launch_source` extension.** Adding `kind: "agent" | "shell"` to `SessionInfo`, `LaunchContext`, `DeviceFrame.Kind`, `DeviceLaunchRequest.Kind`, and `launchHandler.Handle` signature. `launch_source` matcher at `internal/relay/handler/agent/ws.go:86` and the rewrite-to-local at `cmd/tunnel/main.go:171-173` are untouched. (See origin Key Decisions.)
- **Wrap shell in `tunnel run`, not direct daemon-spawn.** Daemon constructs an inner argv that invokes `tunnel run --launch-source mobile --launch-request-id <id> --kind shell --shell-bin <path> --shell-argv0 -<name> --shell-args -i [or -l -i for fish]`, and `tunnel run` exec's the shell with the requested argv0 / args. This keeps mirror, registration, attach, resize, snapshot identical to today; tunnel run is just told "exec a shell instead of spawn a child agent".
- **Env-snapshot at `tunnel daemon start`.** When `tunnel daemon start` is invoked from a shell, daemon captures `os.Environ()` at process startup and stores it on `runtimeState.LoginEnv`. When `tunnel daemon start` is invoked under launchd / systemd with a stripped env (detected by absence of `SHELL` or by a configurable signal), daemon resolves the user's shell, runs `<shell> -l -c "printenv"` once at startup, and parses the result into `LoginEnv`. Failures fall back to system env. Each `kind: shell` launch inherits `LoginEnv` (merged into the wrapper command's environment); agent launches keep their existing env. (Resolves R6a.)
- **Shell discovery order:** `LoginEnv["SHELL"]` first; if empty/missing or not on the recognized-shell list, `os/user.Current()` + parse `/etc/passwd` `pw_shell` field; on macOS where dscl is the primary store, parse `dscl . -read /Users/$USER UserShell` as a third fallback. Validate against R3 recognized-shell list AND R5 `AllowedCommands` membership. Failure → `terminal_not_allowed`. (Resolves R5 deferred.)
- **Login-shell argv matrix:**

  | Shell | argv[0] | extra args | rationale |
  |---|---|---|---|
  | zsh | `-zsh` | `-i` | leading-dash signals login; `-i` forces interactive even when stdin is a PTY |
  | bash | `-bash` | `-i` | same |
  | fish | `-fish` | `-l -i` | historical leading-dash handling is unreliable; `-l --login` is canonical |
  | dash | `-dash` | `-i` | leading-dash works; `-i` ensures interactive mode |
  | sh | `-sh` | `-i` | catch-all; rarely the user's real shell |

  Daemon emits `--shell-argv0 -<name>` and `--shell-args -i` (or `-l -i` for fish) on the inner `tunnel run` invocation. (Resolves R6 deferred.)
- **`kind` on the wire:** flat `Kind string` field added to `protocol.SessionInfo`, `protocol.LaunchContext`, `protocol.DeviceFrame` (alongside Command/CWD/Label), and `types.DeviceLaunchRequest`. `omitempty` JSON tag so older clients/daemons that omit it default to "agent" via post-decode normalization. (Resolves R7 deferred.)
- **`terminal_supported` on the wire:** flat `TerminalSupported bool` field added to `protocol.DeviceInfo`. Daemon computes once at startup (after env snapshot + shell discovery) and re-publishes via `DeviceUpdateFrame` whenever config reloads. Defaults to false in old daemons. (Resolves R3 deferred.)
- **Skip-auto-update mechanism:** new internal-only flag `--kind shell` on `tunnel run` (parsed by `cmd/tunnel/main.go`'s args parser); `tunnel run` suppresses the auto-update check when `kind == "shell"`. Keys on `kind`, not on `launch_source`, so existing `kind: agent` mobile launches keep update checks. (Resolves R9 deferred.)
- **Empty cwd path:** `LaunchDevice` handler in `internal/relay/handler/api/devices.go` parses `kind` first; for `kind: "shell"` it permits empty `cwd` and forwards as-is. Daemon's `resolveLaunchCWD` gains a `kind`-aware variant: empty + shell → resolve to `os.UserHomeDir()` of the daemon user; empty + agent → existing `path_not_found`. (Resolves R11.)
- **Cwd validation (R11a):** non-empty `cwd` is canonicalized (`filepath.Abs` + `filepath.EvalSymlinks`) and validated `IsDir() && Stat() succeeds`. No further "inside reachable filesystem" check beyond `Stat` — daemon already runs as the user, so `Stat`-success is the de-facto reachability check. Login-shell rc/profile files are sourced from `$HOME`; the wrapper passes `HOME=<daemon user $HOME>` regardless of `cwd`.
- **`tunnel run` exec'ing the user's shell:** today `tunnel run <command>` builds a child PTY by exec'ing `command.Path command.Args...`. For `kind: shell` we still go through the same path; daemon's wrapper invokes `tunnel run --kind shell --shell-bin <path> --shell-argv0 -<name> --shell-args "-i"` and `tunnel run` is taught to construct the exec.Cmd with `Cmd.Path = shellBin, Cmd.Args = [argv0, "-i"]`. The session_info reported on register has `Kind = "shell"`, `Launcher = "<shell>"` (e.g. `zsh`).
- **Idle sweeper:** daemon-side ticker (every 1m) iterates `runtimeState.Sessions`, comparing now to per-session `LastInputAt` and `LastAttachAt`. When session is `kind: shell` and both timestamps are older than `IdleWindow`, daemon issues a stop on the wrapped `tunnel run` (sends a control signal that maps onto the existing `Running.Close()` path; needs new daemon→runtime hook). Idle window default 12h, configurable to `0` (disable). (Resolves R19.)
- **Stop-on-revocation cascade (R20):** new field `LaunchingAppSessionID string` captured on the relay session record at registration time when `LaunchContext.Source == mobile` AND `kind == shell`. New hook in `internal/relay/auth/app_service.go`: on app-session revocation event, iterate `session.Registry` for live shell sessions with matching `LaunchingAppSessionID` and route `stop_session` AgentFrame to each owning agent. The existing relay-removes-session-on-disconnect rule plus this revocation hook + R19 idle backstop together cover the daemon-online-vs-offline matrix.
- **Audit log:** new structured events through `logx.Info`:
  - `terminal_launch_accepted`, `terminal_launch_denied`, `terminal_attach_open`, `terminal_attach_close`, `terminal_stopped` on the relay
  - `terminal_launch_started`, `terminal_exited` on the daemon
  
  Both sides go through `internal/logx/logx.go`. Retention ≥30d is achieved by configuring relay deployment to write `relay.log` with a 30d-minimum rotation; this is a deploy-config constraint documented in `docs/operation.md` rather than a separate sink.
- **Default daemon config ships shell-capable.** `defaultAllowedCommands` in `internal/tunnel/daemon/config.go` becomes `["codex", "claude", "gemini", "zsh", "bash", "fish"]`. Existing daemons with custom non-empty `AllowedCommands` files do not auto-merge (current `LoadConfig` only fills defaults when the file is missing or all-empty).
- **Borrow paseo's modifier-toggle UX (mobile only).** Sticky-for-one-keystroke pattern from `paseo/packages/app/src/components/terminal-pane.tsx`. Mobile implementation lives outside this repo.

## Open Questions

### Resolved During Planning

- **Where `kind` lives on the wire** → resolved: flat field on `SessionInfo` / `LaunchContext` / `DeviceFrame` / `DeviceLaunchRequest` with JSON `omitempty` and post-decode default to `agent`.
- **Where `terminal_supported` lives** → resolved: flat bool on `DeviceInfo`, default false.
- **Skip-update-check mechanism** → resolved: `--kind shell` flag on `tunnel run`; suppression branched on kind.
- **Login-shell argv matrix** → resolved (table above).
- **Shell discovery order under launchd/systemd** → resolved (env snapshot at start + `/etc/passwd` + dscl fallbacks).
- **Env propagation mechanism** → resolved: snapshot at `tunnel daemon start`, falling back to `<shell> -l -c printenv` once at daemon startup when started under launchd/systemd.
- **`session_id` continuity across daemon restart** → resolved: tmux survives, the inner `tunnel run` process is unaffected, so `session_id` is preserved automatically. No new code needed.
- **Stop-all bulk endpoint vs N-parallel** → resolved: N parallel `POST /api/sessions/:id/stop` calls from mobile; no new endpoint.
- **App-session-id schema for stop-on-revocation** → resolved: new `LaunchingAppSessionID` field on relay session record, populated at registration when context says mobile + kind shell.

### Deferred to Implementation

- Exact daemon-runtime hook for "stop the inner `tunnel run` from the daemon side" (R19 idle sweep + R20 revocation cascade-when-daemon-relays-stop). Today the daemon's launchHandler can `terminate` a workspace, which kills tmux but not the inner run gracefully. The stop path that R19/R20 need is "send `stop_session` to the relay → relay routes to owning agent → `tunnel run` exits cleanly". The relay→agent path already exists; the daemon-side trigger does not. May need a new daemon → relay control message, or daemon→tmux send-keys to deliver stop. Decide during implementation by reading the existing `Running.Close()` and `connector.go` stop frame plumbing.
- Whether to detect "daemon started under launchd vs from a shell" by env shape (no `SHELL`, no `TERM`, etc.) or by an explicit daemon-config flag. Likely env-shape with a one-line override.
- Mobile UX: pending state, failure surface for `terminal_not_allowed` / `device_offline` / `path_not_found`, retry path (origin R13 design defer).
- Mobile UX: Terminals tab states (empty / loading / error / per-row stopping) and disambiguator copy (origin R14 design defer).
- Mobile UX: latched-modifier visual treatment, double-tap behavior, two-modifier composition (origin R16 design defer).
- Mobile UX: app cold-start with running terminals — open Terminals tab or default tab (origin R14 design defer).
- Mobile UX: hardware Bluetooth keyboard handling (origin R16 design defer).
- Mobile UX: accessibility — VoiceOver/TalkBack, dynamic-type, touch targets, landscape (origin R14/R16 design defer).

## High-Level Technical Design

> *This illustrates the intended approach and is directional guidance for review, not implementation specification. The implementing agent should treat it as context, not code to reproduce.*

### Launch flow (kind: shell)

```text
Mobile app                Relay                   Daemon                   tmux + tunnel run
   |                        |                       |                          |
   | POST /api/devices/:id  |                       |                          |
   |  /launch {kind:shell,  |                       |                          |
   |   cwd:""}              |                       |                          |
   |----------------------->|                       |                          |
   |                        | DeviceLaunchRequest   |                          |
   |                        |   Frame{kind:shell,   |                          |
   |                        |   command:"",         |                          |
   |                        |   cwd:""}             |                          |
   |                        |---------------------->|                          |
   |                        |                       | discoverShell -> "zsh"   |
   |                        |                       | check AllowedCommands    |
   |                        |                       | resolveCWD("") -> $HOME  |
   |                        |                       | canonicalize             |
   |                        |                       | buildShellWrapper:       |
   |                        |                       |   tunnel run              |
   |                        |                       |     --kind shell         |
   |                        |                       |     --launch-source mobile
   |                        |                       |     --launch-request-id  |
   |                        |                       |     --shell-bin /bin/zsh |
   |                        |                       |     --shell-argv0 -zsh   |
   |                        |                       |     --shell-args -i      |
   |                        |                       |   (env from LoginEnv)    |
   |                        |                       |--CreateLaunchSession---->|
   |                        |                       |                          | tmux new-session
   |                        |                       |                          | exec tunnel run
   |                        | launch_result accepted|                          | exec /bin/zsh -i
   |                        |<----------------------|                          |   (argv[0]=-zsh)
   |                        |                       |                          |
   |                        |  /agent/ws register   |                          |
   |                        |  {Kind:shell,         |                          |
   |                        |   LaunchContext:{     |                          |
   |                        |     Source:mobile,    |                          |
   |                        |     RequestID,        |                          |
   |                        |     Kind:shell}}      |                          |
   |                        |<-----------------------|<------------------------|
   |                        | CompleteLaunchIfOwner |                          |
   |                        | SetSessionKindForUser |                          |
   |                        | record                |                          |
   |                        |  LaunchingAppSessionID|                          |
   | session_ready          |                       |                          |
   |<-----------------------|                       |                          |
   | GET /api/sessions/:id  |                       |                          |
   |  /attach/ws            |                       |                          |
   |<--------- attach + PTY bytes ------------------|                          |
```

### Stop semantics matrix

| Trigger | Path | Audit event |
|---|---|---|
| User taps Stop on a row | mobile → `POST /api/sessions/:id/stop` → relay routes `stop_session` to agent → `tunnel run` exits → tmux pane goes back to its trailing `exec login_shell -l` (or the wrapper's tail) and pane stays | `terminal_stopped reason=user` |
| User taps Stop all | mobile issues N parallel stops | N × above |
| Idle window expired | daemon sweeper triggers → daemon sends stop request to relay → relay routes `stop_session` → `tunnel run` exits | `terminal_stopped reason=idle_timeout` |
| App-session revoked (logout / password change / admin disable) | auth service fires revoke event → cascade hook iterates shell sessions with matching `LaunchingAppSessionID` → relay routes `stop_session` to each | `terminal_stopped reason=session_revoked` |
| Daemon offline at revoke time | session already removed from relay discovery on daemon disconnect; on daemon reconnect, relay observes `LaunchingAppSessionID` is revoked and immediately routes `stop_session` after re-registration | `terminal_stopped reason=session_revoked` (post-reconnect) |
| Device permanently offline | session evaporates from discovery as today | `terminal_stopped reason=session_owner_disconnected` |

## Implementation Units

- [ ] **Unit 1: Add `SessionKind` and capability fields to protocol structs**

**Goal:** Thread `kind` and `terminal_supported` through `internal/protocol`. No behavior change yet; pure protocol additive change.

**Requirements:** R1, R3, R7

**Dependencies:** None.

**Files:**
- Modify: `internal/protocol/message.go` (add `Kind string` to `SessionInfo` and `LaunchContext`; add `SessionKindAgent`/`SessionKindShell` consts)
- Modify: `internal/protocol/device.go` (add `Kind string` to `DeviceFrame`; add `TerminalSupported bool` to `DeviceInfo`; update `DeviceLaunchRequestFrame` constructor signature)
- Modify: `internal/protocol/message_test.go`
- Test: `internal/protocol/message_test.go`, `internal/protocol/device_test.go` (create if missing)

**Approach:**
- Add constants `SessionKindAgent = "agent"`, `SessionKindShell = "shell"`.
- All new fields use JSON `omitempty`; post-decode normalization treats empty `Kind` as `SessionKindAgent` for backward compat.
- `DeviceLaunchRequestFrame` signature gains `kind string` argument; existing callers pass `SessionKindAgent`.

**Patterns to follow:**
- `SessionLaunchSource{Local,Mobile}` const pattern in `internal/protocol/message.go:5-8`.
- `omitempty` usage on `LaunchSource` in the same struct.

**Test scenarios:**
- Happy path: encode/decode `SessionInfo{Kind: "shell"}` round-trips; `Kind` survives JSON marshal/unmarshal.
- Edge case: encoding `SessionInfo{}` produces no `kind` key; decoding `{}` yields empty Kind, normalized to `agent` by helper.
- Edge case: `DeviceInfo{TerminalSupported: false}` produces `terminal_supported: false` (kept; not omitted) so old clients can distinguish "default false" from "missing field"; alternatively use `*bool` if absence semantics matter — decide at impl time.
- Happy path: `DeviceLaunchRequestFrame("req", "", "", "", "shell")` builds a frame with `Kind: "shell"` and `Command: ""`.

**Verification:** `go test ./internal/protocol` passes; existing tests unchanged in behavior.

- [ ] **Unit 2: Daemon shell discovery + AllowedCommands extension + recognized-shell list + login-env snapshot**

**Goal:** Daemon can discover the user's preferred shell, validate it against the recognized-shell list and `AllowedCommands`, and produce a `LoginEnv` snapshot that subsequent shell launches inherit. Update default `AllowedCommands` to include shells.

**Requirements:** R3 (capability derivation), R4 (allowlist defaults), R5 (discovery + gate on resolved name), R6a (env-equivalent-to-login).

**Dependencies:** Unit 1.

**Files:**
- Modify: `internal/tunnel/daemon/config.go` (extend `defaultAllowedCommands`, add `RecognizedShells` const list, add `IsRecognizedShell` helper, add `IdleWindow` config field)
- Modify: `internal/tunnel/daemon/runtime.go` (add `LoginEnv`, `TerminalSupported` fields to runtime state and snapshot)
- Create: `internal/tunnel/daemon/shell_discovery.go` (`DiscoverShell` function, env-snapshot logic)
- Modify: `internal/tunnel/daemon/connector.go` (call `DiscoverShell` + env snapshot at startup; surface `TerminalSupported` on registration `DeviceInfo`)
- Test: `internal/tunnel/daemon/config_test.go`, `internal/tunnel/daemon/shell_discovery_test.go`

**Approach:**
- `RecognizedShells = []string{"zsh", "bash", "fish", "dash", "sh"}`. `IsRecognizedShell(name) bool` via case-insensitive basename compare.
- `DiscoverShell(env map[string]string, lookupUser func() string)` returns `(binPath, name, error)`. Order: `env["SHELL"]` → `os/user.Current()` + `/etc/passwd` → macOS `dscl`. Validate `IsRecognizedShell(name)` and `exec.LookPath(binPath)`.
- `SnapshotLoginEnv(stripped bool, shellBin string)` returns `map[string]string`. If env was already rich (has `SHELL`, `PATH` looks healthy), return `os.Environ()` parsed. Otherwise spawn `<shellBin> -l -c "printenv"` and parse.
- `runtimeState.TerminalSupported` set at startup based on (a) at least one recognized shell present in `AllowedCommands` AND (b) discovery succeeded for at least one such shell. Re-evaluated on config reload.
- `defaultAllowedCommands` becomes `["codex", "claude", "gemini", "zsh", "bash", "fish"]`. Note in `docs/daemon.md` that custom configs do not auto-merge.

**Patterns to follow:**
- `runtimeState.snapshot()` shape in `internal/tunnel/daemon/runtime.go` (read-write under mu, returns Status struct).
- `LoadConfig` failure handling in `internal/tunnel/daemon/config.go:17-35` (graceful fallback to defaults).

**Test scenarios:**
- Happy path: `DiscoverShell({"SHELL": "/bin/zsh"}, ...)` returns (`/bin/zsh`, `zsh`, nil).
- Edge case: `DiscoverShell({}, ...)` falls through to passwd lookup; on a fixture passwd shell is `/usr/bin/fish`, returns (`/usr/bin/fish`, `fish`, nil).
- Error path: `DiscoverShell` with all sources empty returns error; runtime's `TerminalSupported` becomes false.
- Edge case: `SHELL` set to `/bin/tcsh` (not in recognized list) → discovery rejects, returns error.
- Edge case: `IsRecognizedShell("ZSH")` true (case insensitive); `IsRecognizedShell("powershell")` false.
- Happy path: `Config.Allows("zsh")` true after default-config load.
- Integration: `DefaultConfig().AllowedCommands` includes the three shells in lower case; `Normalize` keeps them sorted.
- Happy path: `SnapshotLoginEnv(stripped=false)` returns the current process env unchanged.
- Edge case: `SnapshotLoginEnv(stripped=true)` invokes the shell with `-l -c printenv`; given a fixture shell that prints `PATH=/foo:/bar\nFOO=bar`, the result map includes both.
- Error path: `SnapshotLoginEnv` shell exit code != 0 — falls back to `os.Environ()` and emits a `daemon_login_env_snapshot_failed` warn log.

**Verification:** `go test ./internal/tunnel/daemon` passes; `tunnel daemon doctor` reports `terminal_supported: true` on a dev box with default config.

- [ ] **Unit 3: Daemon launch path: thread `kind`, login-shell wrapper, env propagation, cwd validation, idle bookkeeping**

**Goal:** `launchHandler.Handle` accepts `kind`; for `kind: shell` it discovers shell, builds a wrapper that launches `tunnel run --kind shell --shell-bin … --shell-argv0 -<name> --shell-args -i [-l -i for fish]` with `LoginEnv` merged in, validates `cwd`, records ID for the new session in `runtimeState.Sessions` for idle tracking. AllowedCommands gate runs on the *resolved* shell name (not the wire `command`, which is empty for shell launches).

**Requirements:** R5 (gate on resolved shell), R6 (login-shell argv matrix), R6a (env propagation), R7 (kind on wire), R8 (wrap in `tunnel run`), R10 (default `$HOME` cwd), R11 (empty cwd → `$HOME`), R11a (cwd canonicalization), R19 (idle bookkeeping setup — sweeper itself in Unit 5).

**Dependencies:** Unit 1, Unit 2.

**Files:**
- Modify: `internal/tunnel/daemon/connector.go` (`launchHandler.Handle` and `handle` signatures gain `kind string`; new `buildShellLaunchWrapper`; `resolveLaunchCWD` gains `kind`-aware variant; serveOnce reads `frame.Kind`)
- Modify: `internal/tunnel/daemon/connector.go` (record `runtimeSession` per launch)
- Test: `internal/tunnel/daemon/connector_test.go`

**Approach:**
- `Handle(ctx, requestID, kind, command, cwd, label)`. For `kind == "shell"`:
  - Skip `shellquote.Split(command)` and `command_not_allowed` based on `command`. Instead call discovered-shell from runtime state (Unit 2). Run `Allows(<resolved-shell-name>)` against current `Config.AllowedCommands`.
  - `resolveLaunchCWD("", kind="shell")` returns `os.UserHomeDir()` of the daemon user. Non-empty cwd is canonicalized via `filepath.Abs` + `filepath.EvalSymlinks` and `Stat()` checked.
  - Build wrapper that exports `LoginEnv` keys before `tunnel run`. Pass `--kind shell --shell-bin <path> --shell-argv0 -<name> --shell-args <flags>` to `tunnel run`.
  - Record `runtimeSession{SessionID: "<TBD-from-tunnel-run>", LastInputAt: now, LastAttachAt: now, IdleWindow: cfg.IdleWindow}` keyed by request ID temporarily; reconcile to actual `session_id` once register-completion arrives. (May simplify by indexing by `WorkspaceSession`.)
- For `kind: agent` (or empty): existing path unchanged.

**Execution note:** Test-first for the shell-wrapper construction — it has many small pieces (env passthrough, argv flags, cwd handling) that benefit from a regression-proof test before implementation.

**Patterns to follow:**
- `buildShellWrapper` in `internal/tunnel/daemon/connector.go:261-283` for env-restoration prefix/suffix idiom.
- Failure-reason setting via `state.setLastFailure` at `connector.go:181-227`.

**Test scenarios:**
- Happy path: `Handle(ctx, "req-1", "shell", "", "", "")` with discovered shell `/bin/zsh` returns `accepted` and the wrapper string includes `--kind shell --shell-bin /bin/zsh --shell-argv0 -zsh --shell-args -i`.
- Happy path: `Handle(ctx, "req-2", "shell", "", "/Users/me/code", "")` canonicalizes cwd, wrapper includes the canonical path.
- Happy path: `Handle(ctx, "req-3", "shell", "", "", "")` with no cwd resolves to `$HOME`.
- Edge case: `Handle(ctx, "req-4", "shell", "", "/etc/../etc/private", "")` canonicalizes; if path doesn't exist fail with `path_not_found`.
- Edge case: `Handle(ctx, "req-5", "shell", "", "/nonexistent", "")` → `path_not_found`.
- Error path: `Handle` with no recognized shell available (TerminalSupported=false) → `terminal_not_allowed`.
- Error path: `Handle(ctx, "req-6", "agent", "claude", "", "")` (empty cwd, kind agent) → `path_not_found` (existing behavior preserved).
- Edge case: `kind` empty in frame defaults to `agent` (back-compat).
- Edge case: shell discovered as `fish` → wrapper passes `--shell-args "-l -i"`.
- Integration: env snapshot includes `PATH=/opt/homebrew/bin:…`; the wrapper's exported env contains it; an integration test stubs `exec.LookPath` to confirm `git` resolves through that PATH.

**Verification:** `go test ./internal/tunnel/daemon` passes. Manual: `tunnel daemon start` from a normal shell → `tunnel daemon doctor` reports terminal_supported true → relay sees `terminal_supported: true` in `GET /api/devices`.

- [ ] **Unit 4: `tunnel run` accepts `--kind shell` and exec's the user's shell with the requested argv0/args**

**Goal:** Extend `tunnel run` argument parsing and process-launch to accept `--kind shell --shell-bin <path> --shell-argv0 <argv0> --shell-args "<flags>"`. When `kind == shell`, suppress the auto-update check, register `SessionInfo.Kind = shell`, and exec the shell with the requested argv layout instead of treating shell args as a normal command.

**Requirements:** R6, R8, R9 (skip update check key on kind), R1 (Kind in registration).

**Dependencies:** Unit 1.

**Files:**
- Modify: `cmd/tunnel/main.go` (extend `runArgs` parsing for new flags; branch process-launch for shell kind; pass `Kind` into `SessionInfo` and `LaunchContext`)
- Modify: `cmd/tunnel/args.go` (mark new flags as internal; do not surface in user-facing help)
- Modify: `cmd/tunnel/main_test.go` and `cmd/tunnel/args_test.go`
- Modify: `internal/tunnel/launcher/` if argv reshaping requires it (likely: launcher is a thin PATH resolver; changes go in main.go's exec.Cmd construction)

**Approach:**
- Parse `--kind`, `--shell-bin`, `--shell-argv0`, `--shell-args`. Validate that `--shell-bin` resolves on PATH (via existing launcher) and that `kind` is `agent` or `shell`.
- For `kind == "shell"`: skip the auto-update check entirely (do not call the updater). Construct `exec.Cmd{Path: shellBin, Args: append([]string{argv0}, splitShellArgs(args)...)}`. Set `SessionInfo.Kind = "shell"`, `Launcher = filepath.Base(shellBin)`, `CommandPreview = "<shellname> <args>"`.
- Pass `Kind: "shell"` in `LaunchContext` so the relay's correlation step (Unit 6) can backfill `SessionInfo.Kind`.

**Patterns to follow:**
- Existing `launchContextFromRunArgs` in `cmd/tunnel/main.go:264-274`.
- Existing internal-flag style for `--launch-source` / `--launch-request-id`.

**Test scenarios:**
- Happy path: `runArgs.parse(["run", "--kind", "shell", "--shell-bin", "/bin/zsh", "--shell-argv0", "-zsh", "--shell-args", "-i"])` produces `parsed.Kind == "shell"`, etc.
- Happy path: `LaunchContext` for shell kind has `Kind: "shell"` populated when `--launch-source mobile` and `--launch-request-id` are also present.
- Edge case: `--kind shell` without `--shell-bin` → parse error.
- Edge case: `--kind agent` without `--shell-*` flags → existing behavior unchanged.
- Edge case: `--kind shell` plus a positional command argument → parse error (mutually exclusive).
- Integration: stub the auto-updater; confirm it is **not** called for `--kind shell`; confirm it **is** called for `--kind agent` (regression: `kind: agent` mobile launches still get updates).

**Verification:** `go test ./cmd/tunnel` passes; manual smoke: `tunnel run --kind shell --shell-bin /bin/zsh --shell-argv0 -zsh --shell-args -i` from a real shell starts an interactive zsh and registers with relay if base URL is reachable.

- [ ] **Unit 5: Daemon idle sweeper + stop hook**

**Goal:** Daemon-side ticker that walks live shell sessions, identifies sessions where both "no attach" and "no input" exceeded `IdleWindow`, and stops them. Provide a stop hook that the relay-revocation cascade (Unit 7) and idle sweeper both call.

**Requirements:** R19 (12h default idle, configurable to 0), R22 (stop allowed any time).

**Dependencies:** Unit 3.

**Files:**
- Modify: `internal/tunnel/daemon/runtime.go` (add `Sessions map[string]*runtimeSession`, idle bookkeeping mutators)
- Create: `internal/tunnel/daemon/idle_sweeper.go` (ticker, walk sessions, send stop)
- Modify: `internal/tunnel/daemon/connector.go` (record session metadata on launch result; clear on session exit)
- Modify: `internal/tunnel/connector/connector.go` (or similar) — wire input/attach event timestamps back to daemon if the daemon needs them (or: the daemon polls relay for attach state — likely impractical, so daemon must observe its own tmux pane activity)
- Test: `internal/tunnel/daemon/idle_sweeper_test.go`

**Approach:**
- The daemon does not naturally see attach/input events — those flow client → relay → owning agent (`tunnel run`). For idle to be daemon-tracked, the simplest path is: `tunnel run` periodically reports "last input/attach" to the daemon via a daemon-local control message (or daemon polls tmux pane idle time via `tmux display-message -p '#{session_attached}'` and `#{pane_active}` plus `#{history_size}` heuristics).
- **Recommended path:** extend the daemon control socket so the agent (`tunnel run` started by daemon launch) can post `IdleHeartbeat{SessionID, LastInputAt, LastAttachOpenCount}` to the daemon every 30s. This avoids tmux introspection and stays inside Go.
- Sweeper iterates `Sessions`, computes `idle = max(now - LastInputAt, now - LastAttachAt) when AttachOpenCount == 0`, and triggers stop when `idle > IdleWindow`. With `IdleWindow == 0`, sweeper is disabled.
- Stop is performed by the daemon sending a SIGTERM to the wrapped `tunnel run` process via tmux (or via daemon control socket). Cleanest: `tmux send-keys -t <session> "C-c"` then SIGTERM if still alive.

**Execution note:** Characterization-first for tmux interaction — write a probe test that confirms the chosen tmux invocation actually sends Ctrl-C to a fixture pane before wiring it into the sweeper.

**Patterns to follow:**
- `internal/tunnel/daemon/control.go` for daemon control-socket message handling.
- `internal/tunnel/daemon/runtime.go` mutex pattern.

**Test scenarios:**
- Happy path: register session at T0 with `LastInputAt = T0`, `LastAttachAt = T0`, `AttachOpenCount = 0`, `IdleWindow = 1h`. Advance time to T0+30m via injectable clock — sweeper does nothing. Advance to T0+2h — sweeper triggers stop.
- Edge case: `IdleWindow = 0` (disabled) — sweeper never triggers regardless of clock.
- Edge case: `AttachOpenCount > 0` (client currently attached) — sweeper does not trigger even if input is older than window.
- Edge case: agent-kind session in `Sessions` — sweeper ignores it (only shell kinds are affected).
- Edge case: `IdleHeartbeat` with monotonically increasing `LastInputAt` resets the idle timer.
- Error path: stop call fails (tmux unreachable) — sweeper logs `terminal_idle_stop_failed` and re-tries on next tick.
- Integration: full path — register session, wait past idle window, observe `terminal_stopped reason=idle_timeout` log entry and live session removed from relay discovery.

**Verification:** `go test ./internal/tunnel/daemon` passes; manual: configure `IdleWindow: 30s` in daemon config, launch a shell, leave detached, confirm session disappears from `GET /api/sessions` after ~30s.

- [ ] **Unit 6: Relay launch handler accepts `kind`, propagates through device frame, captures `LaunchingAppSessionID`, exposes capability**

**Goal:** `POST /api/devices/:id/launch` accepts `kind: "shell"`, allows empty `cwd` for shell kind, forwards `kind` to daemon via `DeviceLaunchRequestFrame`. Relay's agent-WS registration hook captures `LaunchingAppSessionID` for kind=shell sessions and writes `Kind` into `SessionInfo`. `GET /api/devices` surfaces `terminal_supported`.

**Requirements:** R7, R11, R3 (capability surfaced), R20 (LaunchingAppSessionID capture).

**Dependencies:** Unit 1.

**Files:**
- Modify: `internal/relay/handler/types/device.go` (add `Kind string` to `DeviceLaunchRequest`)
- Modify: `internal/relay/handler/api/devices.go` (parse kind, branch empty-cwd validation)
- Modify: `internal/relay/device/registry.go` (`Launch` accepts kind, plumbs into `DeviceLaunchRequestFrame`)
- Modify: `internal/relay/handler/agent/ws.go` (capture `register.LaunchContext.Kind`, set `sessionInfo.Kind`, capture `app session id` from authenticated middleware context for shell kind)
- Modify: `internal/relay/session/registry.go` (extend session record with `Kind` and `LaunchingAppSessionID`; new `SetSessionKindForUser` paralleling `SetLaunchSourceForUser`)
- Modify: `internal/relay/handler/middleware/` (expose authenticated app-session id alongside user id)
- Test: `internal/relay/handler/rest_api_test.go`, `internal/relay/handler/ws_api_test.go`, `internal/relay/session/registry_test.go`

**Approach:**
- `LaunchDevice` parses `kind` (default `agent`); for `shell` permits empty `cwd`. Forwards `(kind, command, cwd, label)` to `registry.Launch`.
- `registry.Launch` builds `DeviceLaunchRequestFrame(requestID, kind, command, cwd, label)`. Daemon-side `serveOnce` (Unit 3) reads `frame.Kind`.
- After `CompleteLaunchIfOwner` resolves a kind=shell launch with a session id, registry calls a new `SetSessionKindForUser(sessionID, userID, "shell")` plus stores `LaunchingAppSessionID` in the session record.
- For agent kind, behavior is unchanged.

**Patterns to follow:**
- `SetLaunchSourceForUser` in `internal/relay/session/registry.go:269` for the user-scoped backfill pattern.
- `LaunchDevice` in `internal/relay/handler/api/devices.go:28` for HTTP handler shape.

**Test scenarios:**
- Happy path: `POST /api/devices/dev-1/launch {"kind": "shell"}` (no cwd, no command) accepted; daemon receives `DeviceLaunchRequestFrame{Kind: "shell", CWD: "", Command: ""}`.
- Happy path: same request followed by mock daemon `launch_result accepted` then mock agent register with `LaunchContext.Kind = shell`. Resulting `SessionInfo.Kind == "shell"` and the session record has `LaunchingAppSessionID` populated.
- Edge case: `POST /api/devices/dev-1/launch {"kind": "agent"}` with empty cwd → 400 `invalid_request` (existing behavior preserved).
- Edge case: `POST /api/devices/dev-1/launch {}` (no kind) → defaults to agent, existing behavior.
- Edge case: kind=shell launch from a user whose app session is revoked between accept and register → session register completes but immediately receives `stop_session` (covered by Unit 7).
- Error path: device offline mid-launch → `device_offline`, audit log `terminal_launch_denied reason=device_offline` (audit log itself in Unit 8).
- Integration: end-to-end through the shared ws_api_test harness — full launch flow with kind=shell asserts `SessionInfo.Kind == "shell"` in `GET /api/sessions`.

**Verification:** `go test ./internal/relay/...` passes; `make test-relay` passes.

- [ ] **Unit 7: Stop-on-app-session-revocation cascade**

**Goal:** When the relay's auth service emits a revocation event (logout / password change / admin disable), the relay iterates live shell sessions whose `LaunchingAppSessionID` matches and routes `stop_session` to each owning agent. Surface the new `session_revoked` reason on attach close.

**Requirements:** R20.

**Dependencies:** Unit 6.

**Files:**
- Modify: `internal/relay/auth/app_service.go` (publish revocation event: app-session id + user id + reason)
- Modify: `internal/relay/session/registry.go` (new `StopSessionsForRevokedAppSession(appSessionID, reason)` walking live shell sessions)
- Modify: `internal/relay/handler/new.go` (wire the auth-service revocation event to the session registry hook)
- Test: `internal/relay/auth/app_service_test.go`, `internal/relay/session/registry_test.go`

**Approach:**
- Auth service exposes a subscriber channel or callback `OnAppSessionRevoked(func(AppSessionID, UserID, Reason))`. Hook subscribes at handler-construction time.
- `StopSessionsForRevokedAppSession` filters by `Kind == "shell"` AND `LaunchingAppSessionID == revokedID`, sends `StopSessionFrame` to each owning agent peer, emits audit log `terminal_stopped reason=session_revoked`.
- Agent kind sessions are not touched — they keep the existing "logout closes attaches but agent keeps running" rule (CLAUDE.md product boundary).
- Daemon-offline case: relay's session disappeared on disconnect; on reconnect, the agent register hook (Unit 6) checks revocation set and routes stop immediately. Implement as: relay maintains `RecentlyRevokedAppSessions` set with TTL ≥ 24h; agent register that matches it triggers immediate stop.

**Patterns to follow:**
- Existing agent-token revocation cascade `DisconnectAgentTokenDevices` in `internal/relay/device/registry.go:222`.

**Test scenarios:**
- Happy path: register two shell sessions with `LaunchingAppSessionID = "app-1"`, emit revoke for `app-1`, both sessions receive `stop_session` and are removed from registry; audit log entries fired.
- Edge case: revoke for `app-2` (no matching sessions) — no-op, no error, no audit log noise.
- Edge case: agent-kind session with `LaunchingAppSessionID = "app-1"` — explicitly not stopped.
- Edge case: revoke fires while daemon is offline (session not in registry) — registry records app-1 as recently revoked; later the same `tunnel run` re-registers with `LaunchContext.Kind = shell` and the same launching id — register hook detects and immediately routes stop.
- Edge case: TTL expiry — revoke at T0, daemon reconnects at T+25h, register hook does not stop (TTL was 24h).
- Error path: `StopSessionFrame` send fails (peer disconnected) — registry still removes the session; audit log entry has `delivery_status = failed`.
- Integration: full e2e — login, launch shell, password-change, observe shell stops within seconds; new attach with old token rejected.

**Verification:** `go test ./internal/relay/...` passes.

- [ ] **Unit 8: Audit log entries (relay + daemon)**

**Goal:** Structured `logx` events for the full mobile_terminal lifecycle on both sides. Metadata only, no PTY content.

**Requirements:** R21.

**Dependencies:** Unit 6, Unit 7.

**Files:**
- Modify: `internal/relay/handler/api/devices.go` (emit `terminal_launch_accepted` / `terminal_launch_denied` based on result)
- Modify: `internal/relay/handler/agent/ws.go` (emit `terminal_attach_open` / `terminal_attach_close` for kind=shell sessions; add to existing `agent_registered` log when kind=shell with the launching app session id)
- Modify: `internal/relay/session/registry.go` (emit `terminal_stopped` with reason in stop paths)
- Modify: `internal/tunnel/daemon/connector.go` (emit `terminal_launch_started` / `terminal_exited`)
- Modify: `docs/operation.md` (document the events list and 30-day retention requirement)
- Test: `internal/relay/handler/logging_test.go` (extend existing logging test patterns)

**Approach:**
- Reuse `logx.Info(event, fields...)`. Field set per event type is fixed; document in `docs/operation.md`.
- `terminal_launch_denied` covers all reject reasons: `terminal_not_allowed`, `device_offline`, `path_not_found`, `busy`, `command_not_allowed` (when shell discovery fails on the daemon).
- Daemon entries include `pid`, `resolved_shell`, `resolved_cwd`. Relay entries omit those (relay never sees them).
- 30-day retention is enforced by `docs/operation.md` instructing operators to size relay log rotation accordingly. No new log sink in this phase.

**Patterns to follow:**
- `logx.Info("agent_registered", ...)` at `internal/relay/handler/agent/ws.go:114`.
- Field constructors `logx.String`, `logx.Int64` from `internal/logx/logx.go`.

**Test scenarios:**
- Happy path: kind=shell launch flow produces these events in order: `terminal_launch_accepted` (relay), `terminal_launch_started` (daemon), `terminal_attach_open` (relay on first attach), `terminal_attach_close`, `terminal_stopped reason=user`, `terminal_exited` (daemon).
- Edge case: capability mismatch → `terminal_launch_denied reason=terminal_not_allowed`.
- Edge case: idle timeout stop → `terminal_stopped reason=idle_timeout`.
- Edge case: revocation stop → `terminal_stopped reason=session_revoked`.
- Edge case: kind=agent launches do **not** emit `terminal_*` events (regression check — existing `agent_registered` etc. unchanged).
- Integration: a full flow test asserts the observed JSON log line shapes match the documented schema.

**Verification:** `go test ./internal/relay/...` passes; `internal/relay/handler/logging_test.go` covers the event list.

- [ ] **Unit 9: Update docs and verification matrix**

**Goal:** Bring `docs/api.md`, `docs/protocol.md`, `docs/architecture.md`, `docs/daemon.md`, `docs/operation.md`, `README.md`, `CLAUDE.md`, `AGENTS.md` into alignment per the project's Docs Expectations contract.

**Requirements:** Project-wide Docs Expectations from `CLAUDE.md`.

**Dependencies:** Units 1-8.

**Files:**
- Modify: `docs/api.md` (kind on `POST /api/devices/:id/launch`; `terminal_supported` on `GET /api/devices`; new `terminal_not_allowed` failure reason; new `session_revoked` / `session_idle_timeout` close reasons; `kind` on session info)
- Modify: `docs/protocol.md` (kind on `LaunchContext` / `DeviceFrame` / register frame)
- Modify: `docs/architecture.md` (mobile-terminal subflow under launch handling; idle sweeper ownership; revocation cascade)
- Modify: `docs/daemon.md` (recognized-shell list, login-shell argv matrix, env-snapshot mechanism, idle-window config, default-config upgrade behavior, allowlist-as-RCE-gate erosion warning)
- Modify: `docs/operation.md` (audit-log event schema, 30-day retention requirement, deploy-side log rotation guidance)
- Modify: `README.md` (mention `+ Terminal` and Terminals tab in the mobile section; recognized-shell list)
- Modify: `CLAUDE.md`, `AGENTS.md` (mobile terminal added to Current Product Boundaries; `SessionKind` mention)

**Approach:**
- Each doc gets a focused section/subsection for shell-kind behavior.
- `docs/api.md` is the canonical source for HTTP/WS contracts; mobile clients code against it.
- Avoid duplicating text across docs — link to canonical sections (`docs/daemon.md` for argv matrix etc.).

**Patterns to follow:**
- Existing structure of `docs/api.md` for endpoint sections.
- Existing structure of `docs/daemon.md` for `Failure Reasons` and `Health Model` sections.

**Test scenarios:** *Test expectation: none — pure documentation update.*

**Verification:** Read-through review. All cross-doc claims consistent with shipped behavior in Units 1-8.

## System-Wide Impact

- **Interaction graph:** mobile launch → `/api/devices/:id/launch` → relay registry → device WS → daemon launch handler → tmux + tunnel run → register on agent WS → relay session registry. Stop fan-out: user / idle-sweeper / revocation-cascade → relay session registry → owning agent WS → tunnel run → tmux pane close.
- **Error propagation:** every launch denial path (`terminal_not_allowed`, `path_not_found`, `device_offline`, `busy`, `command_not_allowed`, `tmux_not_found`) propagates from daemon through relay through HTTP body. Mobile client must surface each. Audit log records denials with reason.
- **State lifecycle risks:** (a) `LaunchingAppSessionID` mismatch if app-session id is rotated mid-flight (revocation cascade may miss); mitigated by capturing at register time, not at HTTP-launch time. (b) Idle sweeper races with reconnect: heartbeat from `tunnel run` resets the timer; if `tunnel run` crashes silently, sweeper expires the session. (c) `RecentlyRevokedAppSessions` TTL must exceed worst-case daemon offline window — 24h chosen to align with R19 default.
- **API surface parity:** `kind` field becomes part of all session-listing endpoints. Tunnel CLI `tunnel session list` (separate prior plan) should display kind too — out of scope for this plan but flagged.
- **Integration coverage:** end-to-end shell launch + audit + revocation tests live in `internal/relay/handler/ws_api_test.go` and a new `internal/relay/handler/terminal_e2e_test.go` if needed. Daemon-side e2e in `internal/tunnel/daemon/connector_test.go` covering wrapper construction, idle sweep, and stop-via-tmux.
- **Unchanged invariants:** existing `launch_source` matcher at `agent/ws.go:86`; existing `kind: agent` mobile launch flow; existing attach websocket protocol; existing snapshot/resize semantics; existing agent-session "logout doesn't disconnect" rule for kind=agent.

## Risks & Dependencies

| Risk | Mitigation |
|------|------------|
| `LoginEnv` snapshot under launchd produces wrong `PATH` (Homebrew vs system) | Detect stripped env (no `SHELL`/`USER`) and run `<shell> -l -c printenv` once. Fall back to system env with a warning event so operators see it. Document in `docs/daemon.md` how to verify. |
| Idle sweeper kills a session whose user was just about to attach | Heartbeat from `tunnel run` runs every 30s and resets the timer when an attach is open. Default 12h is generous. Operators can set `0` to disable. |
| Revocation cascade misses a session whose daemon is offline at the time | `RecentlyRevokedAppSessions` registry with 24h TTL; agent register hook checks set on every reconnect. Idle window is the final backstop. |
| Operators with custom `AllowedCommands` files get no shell support after upgrade | R4 explicitly says custom configs do not auto-merge. Documented in `docs/daemon.md`. Operators add `zsh`/`bash`/`fish` themselves. |
| Login-shell argv quirks (fish leading-dash; dash `-i` differences) | Argv matrix in Key Decisions; per-shell test in Unit 4. |
| Stop fails to reach `tunnel run` (tmux send-keys race) | Daemon falls back to SIGTERM after a short grace; sweeper retries on next tick; audit log records `terminal_idle_stop_failed`. |
| Audit log retention not actually 30d on a deployed relay | Document in `docs/operation.md`; add a release-checklist item to verify rotation config; out of code scope. |
| `kind` field collides with a future protocol field | `kind` is a generic name but already established by paseo and other shell-relay products as the canonical discriminator. Acceptable. |

## Documentation / Operational Notes

- Mobile UI work happens in the mobile-app repo; this plan publishes the contract that the mobile team consumes.
- Operators must verify `docs/operation.md`'s 30-day audit retention guidance matches their deployment's log rotation config.
- After upgrade, daemons running with custom (non-default) `AllowedCommands` need a manual edit to add shell tokens. Surface this in release notes.
- The `--kind shell --shell-bin --shell-argv0 --shell-args` flags on `tunnel run` are internal-only and should not appear in user-facing help.

## Sources & References

- **Origin document:** [docs/brainstorms/2026-04-27-mobile-remote-terminal-requirements.md](../brainstorms/2026-04-27-mobile-remote-terminal-requirements.md)
- Related code: `internal/protocol/message.go`, `internal/protocol/device.go`, `internal/tunnel/daemon/connector.go`, `internal/tunnel/daemon/config.go`, `internal/relay/handler/api/devices.go`, `internal/relay/handler/agent/ws.go`, `internal/relay/handler/device/ws.go`, `internal/relay/device/registry.go`, `internal/relay/session/registry.go`, `internal/relay/auth/app_service.go`, `internal/logx/logx.go`, `cmd/tunnel/main.go`
- Prior plans: `docs/plans/2026-04-22-001-feat-unified-session-management-plan.md` (stop semantics), `docs/plans/2026-04-18-001-feat-mobile-device-tmux-workspace-plan.md` (launch correlation), `docs/plans/2026-04-18-003-feat-session-platform-identity-plan.md` (DeviceInfo evolution)
- Mobile UI reference: `paseo/packages/app/src/components/terminal-pane.tsx` (modifier-toggle pattern; out-of-repo)
- Project conventions: `CLAUDE.md`, `AGENTS.md`
