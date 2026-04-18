---
date: 2026-04-18
topic: coding-agent-skill-connect
---

# Coding-Agent Skill for Connecting a Running Session to the Relay

## Problem Frame

A user running a coding agent (Claude Code first, Codex and others later) in a local terminal wants the current conversation to become visible and continuable on the companion mobile app. Today, that only works if the agent was launched under `tunnel run <agent>` from the start. If the user already started `claude` without `tunnel run`, there is no in-place way to retrofit relay visibility — a skill running inside `claude` cannot hijack its parent's PTY without tmux (excluded by scope) or fragile ptrace/reptyr tooling.

The goal is a skill, distributable across any coding agent that supports skills, that gives the user a clean path from "running untunneled" to "running tunneled, same conversation" using session resume as the general mechanism.

## User Flow

```
inside claude (untunneled)          outside claude (user's shell)       mobile app
         │
   invoke skill
         │
         ├── detect: already tunneled? ── yes ──► skill prints
         │                                        "already connected" and stops
         │
         │── no ──► skill prints one-liner:
         │            tunnel run claude --resume <session-id>
         │          plus short instruction: "/exit, then paste above"
         │
   user runs /exit ──────────────► user pastes command ──► claude resumes
                                    under `tunnel run`                 │
                                                                       ▼
                                                              session appears
                                                              and is attachable
```

## Requirements

**Skill shape**
- R1. The skill is named `tunnel` and is invocable inside a running coding-agent session (first target: Claude Code) via its native skill-invocation mechanism. It exposes exactly two actions: `connect` and `status`.
- R2. The skill's `description:` metadata is phrased so Claude auto-invokes it when the user expresses intent like "connect this session to the tunnel relay," "make this session visible on my mobile app," or asks for connection status.

**`connect` action**
- R3. When invoked in a session already running under `tunnel run`, `connect` detects this and prints a short "already connected" message without emitting a resume command.
- R4. When invoked in a session that is not tunneled, `connect` prints exactly one copy-pasteable command that, when executed by the user after exiting the current agent, will relaunch the same agent under `tunnel run` resuming the same conversation by session id. In v1 this command is specifically `tunnel run claude --resume <id>`. Future agents must render their own agent-specific resume command rather than reusing the Claude form.
- R5. `connect` performs no installation, no auth configuration, and no daemon management. It does not attempt to reparent, re-exec, or otherwise manipulate the running agent process.
- R6. `connect` output is terse: the one-liner plus at most a two-line instruction explaining the user action (`/exit`, then paste).

**`status` action**
- R7. `status` reports two layers of state, degrading gracefully when a layer is unavailable:
    - *Session layer* (from env vars): whether the current session is tunneled, and if so its `session_id` and `base_url`.
    - *Device layer* (from the daemon's local control socket, action `status`): whether the daemon is running, and if so `device_id`, `display_name`, `platform_id`, `relay_connected`, `launch_health`.
- R8. If the daemon is not running or its socket is unreachable, `status` still prints the session layer and indicates the daemon layer is unavailable. `status` never blocks on network I/O to the relay.

**Detection**
- R9. "Already connected" (for R3) is determined by two env vars exported by `tunnel run` into the child process — `TUNNEL_SESSION_ID=<id>` and `TUNNEL_SESSION_PID=<pid>` (the PID of the `tunnel` process that owns the session) — combined with an ancestor-PID validation:
    - Read both env vars. If either is missing, treat as not connected.
    - Walk the skill process's parent chain (e.g. via `/proc/<pid>/status` on Linux, `ps -o ppid=,comm= -p <pid>` on macOS). If no ancestor matches `TUNNEL_SESSION_PID`, or that process is not alive, or its process name is not `tunnel`, treat as not connected (env var is stale).
    - Only when env vars are present AND the ancestor check passes is the session considered tunneled.
    - Detection performs no daemon or relay calls. This keeps `connect` correct when the daemon is down and immune to manually-exported stale env vars leaking across shells.

**Local build + test (v1 scope)**
- R10. The skill for Claude Code is authored as a `SKILL.md` + any supporting scripts under a local skill directory (e.g. `~/.claude/skills/tunnel/`) on the developer's own machine. No packaging, no marketplace manifest, no install script in v1.
- R11. The skill is considered "done" when it works end-to-end on the author's machine against a running local `tunnel` + `claude` setup, including: `connect` emits the correct resume one-liner, `status` prints the expected two-layer output, and the already-tunneled short-circuit behaves correctly.
- R12. Distribution (marketplace manifest, multi-agent recipes, install script) is deferred. The v1 skill should be authored in a way that doesn't paint the design into a corner — shared logic separable from Claude-specific bits — but no multi-agent infrastructure is built in v1.

## Success Criteria

- On the author's own machine, starting from an already-running `claude` with meaningful conversation history, invoking the skill produces the correct resume one-liner; after `/exit` and pasting, the same conversation resumes under `tunnel run` and is visible on the mobile app.
- Invoking the skill in an already-tunneled session produces no resume command and causes no confusion.
- `status` prints the expected session-layer + device-layer output, and degrades gracefully when the daemon is not running.
- The skill code is structured so future multi-agent and distribution work (recipe files, marketplace manifest) is an additive change, not a rewrite.

## Scope Boundaries

- Not in scope: in-place PTY takeover of the currently running agent. No tmux, no ptrace, no reptyr, no process reparenting.
- Not in scope: spawning a new terminal window, shell-integration trampolines, daemon-driven handoff hooks.
- Not in scope: the skill installing `tunnel`, managing `~/.tunnel/auth.json`, running `tunnel auth login`, or starting `tunnel daemon`.
- Not in scope: preserving the exact PID, in-flight tool calls, or any ephemeral process state of the original agent run. Only conversation history (what `--resume` restores) is preserved.
- Not in scope: skills for agents other than Claude Code in v1.
- Not in scope: a generic multi-agent resume-command layer beyond keeping the Claude-specific implementation structurally separable for later follow-on work.
- Not in scope for v1: any form of distribution — no marketplace manifest, no install script, no public repo, no multi-agent recipe system. v1 is a local, hand-installed skill on the author's machine.
- Not in scope: any change to the relay protocol or relay API.

## Key Decisions

- **Session-resume teleport over in-place attach**: a skill running inside the agent cannot own its parent's PTY without excluded tooling. Session resume is the only general, no-tmux, no-ptrace mechanism that preserves conversation history across coding agents.
- **Copy-paste one-liner, not automation**: explicit user action (`/exit`, paste) is more reliable across shells and agents than shell integration or spawned terminals, and requires no install-time shell hooks.
- **Agent-specific resume command**: the product behavior is "resume this same conversation under tunnel," but the concrete command line belongs to the current agent. v1 only supports Claude Code, so the emitted command is Claude-specific; future Gemini/Codex work must provide their own command shape.
- **Env-var detection, not daemon probe**: reading an env var is cheap, deterministic, and avoids a dependency on the daemon running. It does require `tunnel run` to export the var (see Dependencies).
- **Ancestor-PID check against stale env vars**: env-var presence alone is a hint, not proof — users may export `TUNNEL_SESSION_ID` manually or inherit a stale one across shells. Pairing `TUNNEL_SESSION_PID` with an ancestor-chain walk makes detection robust to leaked or stale env without needing the daemon or relay.
- **Defer distribution**: v1 is "make the skill work on my machine." No marketplace manifest, no install script, no public repo. Keeps scope focused on the actual hard parts (env-var contract, session-id capture, resume-command shape, daemon probe) and lets distribution be chosen later with full information. Claude Code has a native plugin-marketplace path (`.claude-plugin/marketplace.json` + `/plugin marketplace add owner/repo`) that is the likely v2 answer but is not built in v1.
- **Recipe-capable, not recipe-driven in v1**: shared logic and Claude-specific bits are kept separable so a future recipe-driven multi-agent structure is additive, but no recipe machinery is built yet.

## Dependencies / Assumptions

- **Tunnel-side change required** (verified): `tunnel run` today sets only `TERM=xterm-256color` on the child env (see `internal/tunnel/session/process.go:33`). Shipping R9 requires `tunnel run` to export at least `TUNNEL_SESSION_ID`, `TUNNEL_SESSION_PID`, and `TUNNEL_BASE_URL` into the child environment. `TUNNEL_SESSION_PID` must be the PID the ancestor-walk expects to find — either the `tunnel` process itself or a stable wrapper PID — so the exported value and the actual parent PID agree. This is a prerequisite for the skill to ship.
- **Daemon status socket exists** (verified): `internal/tunnel/daemon/control.go:13-36` already defines an `actionStatus` returning the `StatusInfo` fields `status` needs (device_id, display_name, platform_id, relay_connected, launch_health). No new daemon API is required; the skill just calls the existing socket.
- **Claude Code exposes the current session id to skills** (unverified): the resume command needs the current `claude` session id. Assumed available via a Claude-Code-provided env var or config file readable from a skill's shell context. Must be verified during planning; if unavailable, the skill may need to instruct the user to supply the id or read it from Claude's session-state file directly.
- **`claude --resume <id>` preserves the user-visible conversation** (assumed based on Claude Code's documented resume behavior).
- **Users have `tunnel` on PATH and are authenticated**. The skill does not validate this; failure surfaces through `tunnel run`'s own startup errors.

## Outstanding Questions

### Resolve Before Planning
_(none — all product decisions are settled)_

### Deferred to Planning
- [Affects R9][Technical] Final env var names and the exact PID semantics: which PID should `TUNNEL_SESSION_PID` carry (the `tunnel` binary's PID vs the PTY session's owner), and how does the skill identify the process by name on macOS vs Linux (comm field, argv[0], symlink target)? Document in `docs/architecture.md`.
- [Affects R4][Needs research] How does a Claude Code skill reliably obtain the *current* `claude` session id from inside that session? Env var, known file path, or skill-context variable — confirm in Claude Code's skill docs.
- [Affects R4][Technical] Does `claude --resume <id>` need to replay model/flag choices the user passed at launch, or does resume restore them from saved session state? If the former, the skill must capture and replay them; if the latter, the one-liner stays trivial.
- [Affects R10][Technical] Implementation language for the skill's helper scripts: pure shell (simpler, no deps) vs a small helper invoked via the skill (easier JSON parsing of the daemon status socket). Pick during planning.
- [Deferred][Non-blocking] Distribution path (marketplace manifest vs install script vs both) and multi-agent recipe structure — revisit after the v1 skill is working locally.

## Next Steps

→ `/ce:plan` for structured implementation planning (tunnel-side env-var export + ancestor-aware detection + local Claude skill)
