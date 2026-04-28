---
date: 2026-04-27
topic: mobile-remote-terminal
---

# Mobile Remote Terminal

## Problem Frame

Mobile users today can only attach to running agent sessions (`claude`, `codex`, etc.). They cannot start an ad-hoc shell on their machine from the phone — no `cd`, no quick `git status`, no on-the-fly `git worktree add`. The brainstorm asks for an in-app interactive remote shell using the user's own shell config (zsh / bash / fish / etc.), so autosuggestion, history, and key bindings come "for free", paired with a Termux-style soft keyboard for the modifier and special keys mobile keyboards don't expose.

The product line is "honest in-app remote terminal", explicitly *not* a full SSH-replacement: smart UX (per-command exit-code surfaces, last-prompt picker, etc.) is out of scope, and there is no intention to compete on shell-fidelity with mature mobile SSH clients. The win is "I'm already in the tunnel app and want to fix something quickly without app-switching".

## Security Model

Mobile terminal turns the existing launch endpoint into a remote interactive shell scoped to the daemon user — effectively RCE for anyone holding a valid app session token. Plan-level mitigations:

- **Stolen app-session token = full shell takeover**: prevention via R20 (revocation kills shell process, not just attaches; cascade survives daemon offline via queued stop intent); detection via R21 (audit logs include denied attempts, retained ≥30 days); blast-radius bound via R19 (4h idle ceiling).
- **Operator misconfiguration on multi-user dev box**: R3 — operators control mobile-terminal availability per-machine via `AllowedCommands`. Capability flag is best-effort UX gate; defense-in-depth is daemon-side launch-time recheck (R5).
- **Allowlist-as-RCE-gate erosion**: shells listed in `AllowedCommands` make the first-token allowlist a no-op for code paths that run through them. The plan accepts this — once `terminal_supported` is true, the operator has consented to "anything the user can run via shell". Must be documented in `docs/daemon.md`.
- **`cwd` chosen to source attacker-controlled rc files**: R11a — `cwd` is validated (absolute, canonicalized, daemon-user-readable). Login-shell rc/profile files are sourced from `$HOME` only, not `cwd`, so a hostile `cwd` cannot redirect rc execution.
- **Daemon-user code execution tampering with audit logs**: accepted on a single-host deploy. Audit logs share trust boundary with the daemon user. Operators who need a stronger boundary should ship audit events off-host (out of phase scope; recorded in Open in Planning).

The relay remains content-opaque: PTY bytes are forwarded but never inspected, parsed, or logged. Audit logs (R21) record metadata only, including denied attempts.

Out of scope: filtering escape-sequence injection from the mobile client (clients can already inject arbitrary bytes), per-command authorization, sandboxing the shell, idle re-authentication, and a per-device launch rate limit (acceptable since R20+R19 bound blast radius).

## Requirements

**Protocol contract**
- R1. `internal/protocol` adds a new `SessionKind = "agent" | "shell"` field on session info, orthogonal to the existing `launch_source`. Default `agent` for backward compatibility. Sessions registered without an explicit kind are treated as `agent`.
- R2. The existing `launch_source` enum is **not** extended. Mobile terminals are launched with `launch_source: "mobile"` (already supported) and `kind: "shell"` (new). This avoids changing the relay launch correlation matcher and the multiple sites that hardcode comparisons against `SessionLaunchSourceMobile`.

**Daemon contract**
- R3. The daemon maintains a hardcoded **recognized-shell list**: `zsh`, `bash`, `fish`, `dash`, `sh`. The daemon exposes a `terminal_supported: bool` capability on its `/device/ws` registration (propagated by the relay through `GET /api/devices`), true when `AllowedCommands` contains at least one entry from the recognized-shell list; false otherwise. Operators disable mobile terminals on a machine by removing all shells from `AllowedCommands`. The capability is recomputed and re-published on daemon restart and on daemon config reload; relay reflects the latest connected-daemon value, and `GET /api/devices` does not surface stale capability for offline daemons. The mobile UI gate is best-effort; the daemon also re-checks at launch time (R5) and rejects mismatched launches with `terminal_not_allowed`.
- R4. Daemon `AllowedCommands` accepts `zsh`, `bash`, `fish`, `dash`, `sh` as additional valid first-tokens. The default daemon config is updated to include `zsh`, `bash`, `fish`. Existing daemons with custom (non-empty) `AllowedCommands` files do not silently inherit the new defaults; they must edit their config to opt in. The default-config upgrade behavior is documented in `docs/daemon.md`.
- R5. The daemon discovers the user's preferred shell at launch time. Discovery mechanism is deferred (see Open in Planning). The discovered shell binary must (a) be on the daemon's recognized-shell list (R3), (b) be present in `AllowedCommands`, and (c) resolve to a real executable on the daemon user's machine. Failing any of these rejects the launch with a `terminal_not_allowed` failure reason. The daemon-supplied `command` argv on the wire layer is the resolved shell binary name (e.g. `zsh`); the AllowedCommands check at `connector.go:185-198` runs against this resolved name. Login-shell argv-rewriting (R6) happens after the AllowedCommands gate, not before.
- R6. The discovered shell is launched as an **interactive login shell**. Implementation passes the POSIX login-shell convention (argv[0] with leading `-`, e.g. `-zsh`) plus the interactive flag, so user rc/profile files are sourced regardless of shell flavor. There is no `/bin/sh` fallback; if no allowed shell is discoverable, the launch fails.
- R6a. The launched shell inherits an environment equivalent to what the user would see when starting their shell from a normal interactive login on the same machine (PATH including Homebrew/asdf/mise/nvm-style additions, `HOME`, `USER`, `LANG`/`LC_*`, and any persistent env added by login dotfiles). This must hold regardless of whether the daemon was started by `tunnel daemon start` from a normal shell, by launchd, or by systemd-user. Mechanism (snapshotting env at `tunnel daemon start`, sourcing a login shell once at daemon startup, etc.) is deferred to planning; the requirement is that "+ Terminal" landing in a shell where `git`, `node`, etc. are not on `PATH` is a launch failure of the feature, not an acceptable variant.
- R7. Mobile terminal launches reuse `POST /api/devices/:id/launch`. The launch request carries `kind: "shell"` (new field) alongside the existing `command`, `cwd`, `label`, and `launch_source: "mobile"` plumbing.
- R8. The launched shell process is wrapped in `tunnel run`, matching today's session model and reusing the existing PTY mirror, agent registration, attach websocket, snapshot generation, and resize handling. No new transport, no new mirror.
- R9. The wrapping `tunnel run` invocation must skip the interactive auto-update check when launching a `kind: "shell"` session. Tapping "+ Terminal" must not trigger a binary update download. The mechanism (CLI flag, env var, or internal-only metadata field) is TBD in planning. **Whatever the mechanism, it must key on `kind: "shell"`, not on `launch_source: "mobile"` alone**, since existing kind=agent mobile launches (already passing `--launch-source mobile`) must continue to receive update checks.

**Launch defaults and cwd handling**
- R10. The default cwd for a mobile terminal launch is the daemon user's `$HOME`. The mobile client may pass an explicit `cwd`, but the "+ Terminal" entry in this phase always omits it.
- R11. The relay launch handler accepts an empty `cwd` field on launch requests where `kind: "shell"`. The relay parses `kind` before the existing cwd-non-empty check (`internal/relay/handler/api/devices.go:39-44`) and skips that check for shell launches. The daemon (`connector.go:200-210`'s `resolveLaunchCWD` path) treats empty `cwd` as "use `$HOME` of the daemon user" for shell launches and returns the existing `path_not_found` for agent launches.
- R11a. Any non-empty `cwd` (default or client-supplied) is validated daemon-side before the shell starts: must be absolute, must canonicalize (post-symlink) to a directory the daemon user can `stat`, and the canonicalized path must remain inside the daemon user's reachable filesystem. Validation failure rejects the launch with `path_not_found`. Login-shell rc/profile files are sourced from the daemon user's `$HOME`, never from `cwd`.

**Mobile client: entry**
- R12. The Devices tab adds a "+ Terminal" affordance on each device row. The button is enabled only when the device's `terminal_supported` capability is true and the device is online.
- R13. Tapping "+ Terminal" issues `POST /api/devices/:id/launch` with `kind: "shell"`, the daemon's preferred shell, and zero additional form input. On `session_ready`, the app navigates to the Terminals tab focused on the new session.

**Mobile client: terminal UI**
- R14. The mobile app gains a top-level **Terminals** tab. The tab lists currently running shell sessions, grouped by device, with each row showing device name, started-at, and a stop button. The tab includes a "Stop all" affordance on the right.
- R15. Terminals are presented as a separate top-level entity from agent sessions. Mobile UI filters by `kind: "shell"` and never co-mingles shell and agent sessions in the same list.
- R16. The terminal screen reuses the existing xterm-compatible attach renderer. Above the system keyboard the app shows a sticky bar containing the keys the mobile app already exposes today plus two new modifier toggles: `Ctrl` and `Alt`. Modifiers are sticky-for-one-keystroke: tapping a modifier latches it; the next non-modifier key consumes the latched modifiers and clears them. Visual state for the latched modifier must be distinct from the default state.
- R17. Modified Enter (Shift / Ctrl / Alt + Enter) sends a plain `\r`. The Kitty keyboard protocol (CSI u) is not implemented in this phase. Other modified keys also send their legacy sequences. (TUI agents that need CSI u should be launched as agent sessions through the existing agent-launch path, not from inside the mobile terminal.)

**Lifecycle**
- R18. Mobile terminal sessions are persistent across mobile app backgrounding, attach websocket drops, and network blips, the same way `tunnel run` sessions are persistent today via the daemon's tmux workspace.
- R19. The daemon stops a mobile terminal session when **both** of the following hold for the configured idle window: (1) no client attach has been open against the session, AND (2) the session has received zero input bytes from any attach client. Default idle window is **12 hours** — long enough to cover a typical workday's `tail -f` or `npm run dev` left running, short enough to prevent forgotten shells from accumulating across days. Operators may set the idle window to any duration including `0` (never expire) via daemon config. Idle is tracked daemon-side; the relay is not the source of truth. Modifier-only events that produce no input bytes (R16 latch toggles before a key is pressed) do not count as input.
- R20. When the app session that launched a `kind: "shell"` session is revoked (logout, password change, admin disable), the shell session is stopped. The revocation key is the relay's per-session record of the launching app-session id (relay must persist this on session registration, since today only `LaunchSource` is stored on `SessionInfo`). Routine token rotation that preserves the same logical app-session does not trigger stop. Stop semantics: the relay sends `stop_session` to the owning agent; the daemon kills the inner PTY child (the shell process) and closes the tmux window; the daemon does **not** rely on the existing agent-session "logout closes attaches but leaves the agent running" rule for shell kinds. Active attaches close with reason `session_revoked`. Best-effort guarantees apply: if the daemon is offline at revocation time, the relay queues the stop intent and re-issues it on daemon reconnect; if the daemon never reconnects, the shell expires by R19's idle window.
- R21. The relay produces structured audit log entries for `mobile_terminal` lifecycle events through `internal/logx`. Entries include: **launch accepted** (user, device, session id, cwd-as-requested, time), **launch denied** (user, device, reason, time — covers `terminal_not_allowed`, `device_offline`, `path_not_found`, `busy`, capability mismatch), **attach open/close** (user, session id, reason), **stop** (session id, reason — covers `session_stopped`, `session_revoked`, `session_idle_timeout`, `session_owner_disconnected`). The daemon produces matching launch entries with the **resolved** shell binary, pid, and canonicalized cwd, plus exit entries (exit code or signal). Audit logs contain metadata only — no PTY bytes. Retention: relay audit entries must be retained for at least 30 days, separate from the rolling relay request log if necessary; this is independent of any retention shorter than 30 days that the general relay log may use.
- R22. Closing a mobile terminal is allowed at any time via `POST /api/sessions/:id/stop`. The mobile UI exposes per-row stop and a tab-level "Stop all" action mapped to bulk stop calls.

## Success Criteria

- Tapping "+ Terminal" on an online, capable device row produces a usable interactive shell on the target machine within ~2s with no intermediate form to fill.
- Inside the terminal, the user's own shell features (zsh-autosuggestion, fzf history, `Ctrl-R`, custom prompts, aliases, Tab completion) work without any client-side intervention.
- All keys needed for daily shell + TUI use are reachable: existing soft-keyboard keys plus `Ctrl` and `Alt` modifiers.
- The Terminals tab shows all running mobile terminals across all devices, with single-row stop and tab-level stop-all.
- Closing a terminal occurs only via explicit user action, daemon-side idle expiry, app-session revocation, or the device going offline permanently.
- Tapping "+ Terminal" never triggers a binary update download.
- Audit logs let an operator answer "who launched a shell on which device, when, and what made it stop" without reading any PTY content.

## Scope Boundaries

- Not building a separate ephemeral PTY channel parallel to `tunnel run`. Mobile terminal is a normal session with `kind: "shell"`.
- Not adding shell-integration in any form: no `ZDOTDIR` injection, no OSC 633 parsing, no command-finished events, no exit-code surfaces, no prompt-boundary detection.
- Not adding session-detail "open terminal here" entry. Devices tab is the only entry in this phase.
- Not adding cwd selection, cwd history, recent-dir picker, or shell picker on creation.
- Not adding a configurable keybar, user-defined macros, multi-row Termux-style keyboard, or two-finger gestures.
- Not implementing Kitty keyboard protocol / CSI u encoding. Modified Enter and other modifier combinations send legacy sequences.
- Not implementing scroll-to-previous-command, command picker, or any UX that requires shell-integration.
- Not adding a per-command authorization layer or shell sandboxing. The whole shell runs as the daemon user with whatever the daemon user can do.
- Not filtering escape-sequence injection from the mobile client. The relay stays content-opaque.
- Not adding a per-device hard concurrency cap. Cleanup is timeout-driven (R19), not count-driven.

## Key Decisions

- **Reuse session model rather than introduce a separate terminal primitive.** The `tunnel run` wrapper gives mirror, agent registration, attach, resize, snapshot, and persistence-via-tmux for free. A separate primitive would re-implement all of these for one orthogonal axis (kind), which a new field provides at much lower cost.
- **`SessionKind` orthogonal field rather than `launch_source = "mobile_terminal"`.** Source identifies *where* the launch came from; kind identifies *what* the session is. They are orthogonal and both meaningful (e.g., a future feature might launch a shell from the desktop app — `launch_source: "local"` + `kind: "shell"`). Using a new source value would require touching at least six sites that hardcode `SessionLaunchSourceMobile`; using a new orthogonal field requires no changes to existing `source`-aware code paths.
- **Wrap in `tunnel run`, not direct daemon-spawned PTY.** Reuses everything; the only added complexity is suppressing the auto-update check on shell launches (R9) and ensuring the wrapper invokes the inner shell as login + interactive (R6).
- **Don't assume zsh.** R5/R6 commit to "user's preferred shell, must be in allowlist, must be a real shell". No `/bin/sh` fallback because /bin/sh cannot deliver the experience the success criteria promise.
- **Default daemon config ships shell-capable.** Fresh installs and upgrades to a default config include `zsh`/`bash`/`fish` in `AllowedCommands`, so a user who installs the daemon and then launches the mobile app can use "+ Terminal" immediately, no extra opt-in step. Justified by the user's explicit stance that the target machine is the user's own laptop and security can be relaxed relative to multi-user / shared-host scenarios. If a future product line targets shared/team boxes, introduce an explicit `terminal_enabled` daemon flag at that point rather than retrofitting opt-in here.
- **"Feels like desktop" is a hard product promise, not best-effort.** R6a (env propagation) is treated as load-bearing: if `git`/`node`/`brew`-installed binaries don't work on first launch, the feature has failed. Whatever discovery mechanism the planning phase picks, it must hit this bar — variants where "the daemon's stripped env is what you get" are unacceptable.
- **Drop Kitty CSI u for modified Enter.** Honest baseline behavior in plain shell is more important than supporting one TUI (Claude Code) that has its own dedicated agent-attach path. CSI u can be added later behind an app-mode-detection check if needed.
- **`terminal_supported` is a discovered device capability, not a runtime error.** The "+ Terminal" affordance is hidden / disabled per-device based on capability so users never tap a button that fails by config.
- **Stop-on-revocation rather than reuse the agent-session "logout doesn't disconnect" rule.** Shells are RCE-equivalent; allowing a revoked auth to leave a live shell behind is unacceptable. Agent sessions keep the lighter rule because they're purpose-built and time-bounded.
- **Idle timeout cleanup, not per-device cap.** Caps interfere with legitimate "5 terminals open for different tasks" workflows; idle expiry only catches forgotten / accidentally created terminals. Default 12 hours, picked to cover a workday's long-running command (`tail -f`, `npm run dev`) without accumulating across days. Operators may set `0` to disable cleanup, accepting standing RCE-equivalent surface in exchange for "exactly like SSH" semantics. The Terminals tab gives the user immediate visibility (R14) so manual close stays primary.
- **Audit logs in this phase.** Without them, the security model is incomplete on day one.
- **Design pattern borrowed from paseo's mobile UI.** Specifically the toggle-modifier-for-one-key pattern in `packages/app/src/components/terminal-pane.tsx`. Not borrowing paseo's separate-primitive backend (different daemon model) and not borrowing CSI u encoding (R17).

## Dependencies / Assumptions

- Daemon's existing tmux workspace continues to host launched sessions; tmux survival across daemon restart is what makes "persistent terminal" practical. (Verified in `docs/daemon.md`.)
- `protocol.SessionLaunchSource{Local,Mobile}` constants exist as enum-like values in `internal/protocol/message.go`. Adding `SessionKind` is a new orthogonal field, not a launch_source mutation. (Verified in `internal/protocol/message.go`.)
- Daemon `AllowedCommands` is a flat first-token list and gates `codex`/`claude`/`gemini` today. Adding shells is a config append. (Verified in `internal/tunnel/daemon/config.go`.)
- The relay launch correlation matcher in `internal/relay/handler/agent/ws.go:86` is **not** modified; mobile terminal launches use `launch_source: "mobile"` to satisfy the existing matcher. (Verified by grep.)
- `tunnel run` exposes (or can be extended to expose) a flag suppressing the interactive update check; today the check is unconditional on interactive run. (Implementation detail; called out in R9 and Open in Planning.)
- Mobile app already has an xterm-compatible renderer attached to `/api/sessions/:id/attach/ws`; concrete mobile implementation lives outside this repo. The mobile-side requirements are part of the contract but not implemented here.

## Outstanding Questions

### Resolve Before Planning
(none — all decisions made through brainstorming dialogue)

### Deferred to Planning
- [Affects R5, R6a][Needs research] How the daemon discovers the user's preferred shell **and** captures a login-equivalent env when started under launchd / systemd with a stripped env. Candidates for shell discovery: `$SHELL`, `getpwuid_r` on `/etc/passwd`, `dscl` on macOS. Candidates for env capture: snapshot env at `tunnel daemon start` (when run from a user shell), source a login shell once at daemon startup, or invoke each shell launch via a wrapper that goes through a login shell. Pick during planning.
- [Affects R6][Technical] Exact argv construction for "interactive login shell" across zsh / bash / fish / dash / sh. The leading-dash convention has quirks (fish historically ignored it; dash differs on `-i`). Capture matrix in `docs/daemon.md` during planning.
- [Affects R7][Technical] Where `kind` lives on the wire. Today `internal/relay/handler/types/device.go` `DeviceLaunchRequest` and `internal/protocol/device.go` `DeviceLaunchRequestFrame` carry `{Command, CWD, Label}`; `internal/tunnel/daemon/connector.go:162` `launchHandler.Handle` takes `(requestID, command, cwd, label)`. All three need a new `kind` field threaded through. Confirm additive change is JSON-compatible with older daemons (default `agent`).
- [Affects R3][Technical] Where `terminal_supported` lives on the wire. `internal/protocol/device.go` `DeviceInfo` has no capabilities field today; add as a flat additive field on `DeviceInfo` (default false for older daemons), not a separate sub-object.
- [Affects R9][Technical] Mechanism for "skip interactive update check" — flag, env var, or internal-only metadata field on the launch request. Required: keys on `kind`, not on `launch_source`.
- [Affects R13][Design] Mobile UX for the launch round-trip: pending state on the "+ Terminal" button (~500ms-2s), debounce against double-tap (existing relay/daemon enforce single in-flight launch per device — second tap returns `busy`), failure surface for `terminal_not_allowed` / `device_offline` / `path_not_found`, retry path.
- [Affects R14][Design] Terminals tab states: empty ("no terminals running" + CTA), loading on first open, error on relay unreachable, per-row "stopping…" state. Disambiguator when two terminals share the same device + cwd (probably launch-order index or started-at). Tab-level "Stop all" confirmation copy (high-blast-radius, easy to mis-tap).
- [Affects R14][Design] Whether app cold-start with running terminals opens to Terminals tab or always to the default tab.
- [Affects R16][Design] Visual treatment of latched modifiers (color / underline / filled), double-tap behavior (cancel / lock / Termux-style), two-modifier composition. Default to paseo's pattern unless mobile design rules it out.
- [Affects R16][Design][Needs research] Hardware Bluetooth keyboard handling — keybar hide, OS Ctrl/Alt vs in-app modifiers, system shortcut routing.
- [Affects R14, R16][Design][Needs research] Accessibility — VoiceOver/TalkBack announcement of latched modifiers, dynamic-type / cell-sizing, minimum touch target, landscape / tablet layout.
- [Affects R18][Technical] `session_id` continuity for shell sessions across daemon restart. Tmux preserves the PTY; whether the new `tunnel run` re-registers under the same `session_id` is what makes the mobile UI's terminal-row continue to work vs. swap. Verify during planning and document the chosen behavior.
- [Affects R20][Technical] Exact relay-side schema for "launching app-session id" per session, since `SessionInfo` does not store this today. Consider whether existing app-session revocation hook is edge-triggered or detectable on the next request, and whether a polling sweep is needed for stop-on-revocation.
- [Affects R22][Technical] Whether tab-level "Stop all" issues N parallel `POST /api/sessions/:id/stop` calls or a new bulk endpoint. Default: N parallel; flip to bulk only if rate-limited.

## Next Steps
→ `/ce:plan` for structured implementation planning
