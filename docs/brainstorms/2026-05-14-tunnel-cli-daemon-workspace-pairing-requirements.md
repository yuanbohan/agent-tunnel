---
date: 2026-05-14
topic: tunnel-cli-daemon-workspace-pairing
---

# Tunnel CLI Daemon, Workspace, And Pairing Simplification

## Summary

Tunnel should present one clear mental model before external release: the daemon is the required local background runtime, pairing manages trusted mobile clients, workspace commands manage the daemon-owned tmux view, and session commands manage live Tunnel sessions across machines.

This document supersedes the command grouping assumptions in `docs/brainstorms/2026-04-22-unified-session-management-requirements.md` where daemon commands still owned workspace open and close.

## Problem Frame

The current daemon command surface mixes several different jobs under `tunnel daemon`: daemon lifecycle, tmux workspace inspection, local broker inspection, mobile pairing, trusted device management, and remote-launch session control. That is workable for development, but it asks users to understand internal implementation boundaries before they can operate the product.

The next product shape should make the public CLI match the user's intent:

| User intent | Public command area | Product meaning |
| --- | --- | --- |
| Keep this computer available to Tunnel | `tunnel daemon` | Manage the local required background daemon. |
| Trust or remove a mobile client | `tunnel pair` | Manage pairing and trusted client devices. |
| View the daemon-owned terminal workspace | `tunnel workspace` | Enter or leave the local tmux workspace used by mobile-created sessions. |
| See or stop live work | `tunnel session` | Manage account-visible live Tunnel sessions. |
| Start local work | `tunnel run` | Start a foreground local session that is also registered with the daemon broker. |

Because Tunnel has not been externally released, this redesign can be a clean break. The old public command spellings do not need compatibility aliases in this revision.

## Actors

- **Local CLI user**: works from a desktop terminal, starts sessions with `tunnel run`, pairs mobile clients, and occasionally opens the local workspace.
- **Mobile user**: uses a trusted mobile client to discover computers and start sessions remotely.
- **Local daemon**: keeps the computer online for remote launch, owns trusted pairing state, owns the daemon tmux workspace, and exposes the local broker used by `tunnel run`.
- **Installer/update flow**: prepares the local environment and warns about missing dependencies such as tmux.

## Command Surface

| Area | Commands | Solves | Notes |
| --- | --- | --- | --- |
| Local daemon | `tunnel daemon start`<br>`tunnel daemon status`<br>`tunnel daemon stop`<br>`tunnel daemon doctor` | Makes this computer available, checks health, stops the background runtime, and diagnoses setup issues. | `start` must be idempotent. |
| Pairing and trust | `tunnel pair`<br>`tunnel pair devices`<br>`tunnel pair revoke <fingerprint>` | Pairs a mobile client, lists trusted clients, and removes trust. | `devices` and `revoke` live under `pair` because they operate on pairing trust. |
| Workspace view | `tunnel workspace open`<br>`tunnel workspace close` | Opens or closes the daemon-owned tmux workspace view for mobile-created local work. | No `workspace sessions` command. |
| Live sessions | `tunnel session list`<br>`tunnel session stop <session-id>` | Lists and stops live Tunnel sessions visible to the current account. | This is account-level session management, not tmux inventory. |
| Local start | `tunnel run <command>` | Starts a local foreground Tunnel session. | Always requires daemon and broker registration; no public `--daemon` flag. |

## Key Flows

### First Machine Setup

1. The user installs or updates Tunnel.
2. Tunnel checks whether tmux is available.
3. If tmux is missing, Tunnel prints a clear warning with platform-appropriate manual installation guidance.
4. Tunnel does not install tmux automatically.
5. The user starts availability with `tunnel daemon start`.
6. The computer becomes discoverable to paired mobile clients while the daemon is online.

### Local Session Start

1. The user runs `tunnel run <command>`.
2. The CLI ensures the local daemon is running or attempts the configured best-effort start path.
3. If the daemon or broker is unavailable, the user command does not start.
4. The error tells the user to run `tunnel daemon start` and includes the most actionable failure reason available.
5. Once the daemon broker is available, the session is registered locally and can appear alongside mobile-created sessions.

### Interactive Pairing

1. The user runs `tunnel pair`.
2. The CLI creates a pairing invitation and renders a scannable QR code.
3. The same terminal remains interactive while the invitation is pending.
4. The mobile client scans the QR code and submits a pairing response.
5. The CLI displays the requesting device identity and prompts for the 6-digit SAS code.
6. The user types the code in the same command and presses Enter.
7. Correct SAS input completes pairing; timeout, Ctrl-C, or SAS mismatch cancels the attempt.

### Workspace Access

1. A mobile client starts work on this computer.
2. The daemon launches the session in its dedicated tmux workspace.
3. The local user can run `tunnel workspace open` to enter that workspace.
4. The local user can run `tunnel workspace close` to detach the local workspace view.
5. Session listing and stopping still happen through `tunnel session`.

### Account-Level Session Management

1. The user runs `tunnel session list`.
2. The CLI shows live sessions for the authenticated account across machines.
3. Each row makes source and machine context visible: local-created versus mobile-created, this machine versus another machine.
4. The user stops a session with `tunnel session stop <session-id>`.
5. Stop targets the live Tunnel session, not the daemon process and not the workspace view.

## Requirements

### Command Ownership

- R1. `tunnel daemon` must only expose local daemon lifecycle and diagnostics commands: `start`, `status`, `stop`, and `doctor`.
- R2. `tunnel daemon start` must be idempotent. If the daemon is already running with the compatible local context, the command succeeds and says it is already running.
- R3. The public CLI must remove `tunnel daemon pair`, `tunnel daemon devices`, `tunnel daemon revoke`, `tunnel daemon open`, `tunnel daemon close`, `tunnel daemon sessions`, and `tunnel daemon broker sessions`.
- R4. The public CLI must add top-level `tunnel pair` for interactive mobile pairing.
- R5. Trusted client listing must be exposed as `tunnel pair devices`.
- R6. Trusted client revocation must be exposed as `tunnel pair revoke <fingerprint>`.
- R7. Workspace view commands must be exposed as `tunnel workspace open` and `tunnel workspace close`.
- R8. `tunnel workspace` must not expose a `sessions` subcommand in this revision.
- R9. `tunnel session` must expose only `list` and `stop` in this revision.
- R10. `tunnel run` must not expose the public `--daemon` option in this revision.

### Daemon-Required Runtime

- R11. `tunnel run <command>` must require a compatible local daemon before starting the user command.
- R12. Every `tunnel run` session must register with the local daemon broker so local-created and mobile-created sessions can be discovered through one local roster.
- R13. If the daemon cannot be started or reached, `tunnel run` must fail before launching the user command.
- R14. Daemon startup failure messages must include the next manual action: `tunnel daemon start`.
- R15. The daemon-required behavior must not weaken the existing relay startup gate for `tunnel run`.
- R16. The product may rely on the daemon being explicitly started for mobile discovery after reboot or after a period without local `tunnel run` usage.
- R17. This revision must not add system service installation, login startup registration, launchd setup, systemd setup, or a background auto-start daemon manager.

### Pairing UX

- R18. `tunnel pair` must support the happy path in one interactive command: show QR, wait for the mobile response, prompt for the SAS, and confirm trust.
- R19. The default pairing invitation wait time should be 5 minutes to match the invitation lifetime.
- R20. The pending pairing screen must make the remaining time, cancellation key, and next expected user action obvious.
- R21. The CLI must continue accepting typed input while the QR code is displayed so the user can enter the 6-digit SAS without switching commands.
- R22. After a mobile response arrives, the CLI must display enough device identity to help the user confirm the intended client before typing the SAS.
- R23. A SAS mismatch must not create trust. The CLI should cancel the attempt and tell the user to run `tunnel pair` again.
- R24. `tunnel pair devices` must show the exact fingerprint value accepted by `tunnel pair revoke <fingerprint>`.
- R25. `tunnel pair revoke <fingerprint>` must use the same fingerprint spelling shown in `tunnel pair devices`; users should not need to transform, shorten, or decode the value.

### Workspace UX

- R26. `tunnel workspace open` must enter the daemon-owned tmux workspace used for mobile-created sessions.
- R27. `tunnel workspace close` must close or detach the local workspace view without stopping sessions.
- R28. Workspace command copy must describe a workspace view, not account-level session management.
- R29. When no workspace is available or no mobile-created work exists, `tunnel workspace open` must explain the state clearly and point to the relevant next action.
- R30. Workspace commands must not become the primary way to list or stop sessions.

### Session Management

- R31. `tunnel session list` must list live Tunnel sessions visible to the authenticated account.
- R32. The list must include both local-created sessions and mobile-created sessions when both are online and visible.
- R33. The list must distinguish sessions on this machine from sessions on other machines.
- R34. The list must distinguish launch source: local `tunnel run` versus mobile-created remote launch.
- R35. The list must show enough machine context for the user to understand where each session is running.
- R36. `tunnel session stop <session-id>` must stop the selected live Tunnel session regardless of whether it was started locally or from mobile.
- R37. `tunnel session stop` must not stop the daemon process.
- R38. `tunnel session stop` must not treat workspace open or close as part of session shutdown.
- R39. If a session cannot be stopped, the error must identify whether the issue is authorization, offline owner, missing session, relay connectivity, or another known state.

### Display And Information Design

- R40. User-facing list commands must be designed for scanning first and copying second.
- R41. Dense list output should use stable tables with aligned columns by default.
- R42. Tables must not rely on color alone to communicate status or source.
- R43. Default table output must avoid emoji because emoji width can break terminal alignment.
- R44. Important identifiers that users must copy, such as session IDs and fingerprints, must be visible in full unless the terminal is too narrow.
- R45. When truncation is required, human labels and paths may be truncated before copy-critical identifiers.
- R46. Long paths should use middle truncation so both the leading context and final directory remain visible.
- R47. Empty optional values should display as `-`, not as blank cells.
- R48. Unknown values should display as `unknown` when the distinction matters, not as a misleading default.
- R49. Help text and command output must use product concepts rather than internal implementation names such as broker roster or tmux session inventory.
- R50. Error messages must include a short cause, the command that failed, and a next action when a user action can fix the state.

## Presentation Examples

These examples describe the intended information shape. Exact widths and wording can be refined during implementation.

### `tunnel session list`

The default output should make machine and launch source clear without requiring users to know relay or daemon internals.

```text
┌────────┬────────┬──────────────┬──────────────┬──────────────┬──────────────────────┬──────────────────────────────┬──────┐
│ Scope  │ Source │ Session      │ Label        │ Command      │ Machine              │ CWD                          │ Age  │
├────────┼────────┼──────────────┼──────────────┼──────────────┼──────────────────────┼──────────────────────────────┼──────┤
│ local  │ local  │ 1839201      │ api-fix      │ codex        │ This machine         │ ~/repo                       │ 12m  │
│ local  │ mobile │ 1839455      │ mobile-task  │ claude       │ This machine         │ ~/work/agent-tunnel          │ 4m   │
│ remote │ local  │ 1839012      │ review       │ codex        │ Office Linux         │ /home/yuanbo/work/.../api    │ 1h   │
│ remote │ mobile │ 1838777      │ deploy       │ claude       │ Yuanbo MacBook Pro   │ /Users/yuanbo/.../frontend   │ 2h   │
└────────┴────────┴──────────────┴──────────────┴──────────────┴──────────────────────┴──────────────────────────────┴──────┘
```

Column intent:

| Column | Meaning |
| --- | --- |
| `Scope` | `local`, `remote`, or `unknown`, based on whether the session is running on this computer. |
| `Source` | `local` for local CLI-created sessions, `mobile` for mobile-created sessions, or `unknown`. |
| `Session` | Copyable session identifier accepted by `tunnel session stop`. |
| `Label` | User-provided label when present. |
| `Command` | Root command or shell launched for the session. |
| `Machine` | Human-readable computer name, using `This machine` for the current computer. |
| `CWD` | Working directory with middle truncation for long paths. |
| `Age` | Human-readable time since session start. |

### `tunnel pair devices`

The revocation identifier should be visually obvious and copyable.

```text
┌──────────────────────────────────┬──────────────────────┬──────────┬──────────────┬──────────────┐
│ Fingerprint                      │ Device               │ Status   │ Paired       │ Last Seen    │
├──────────────────────────────────┼──────────────────────┼──────────┼──────────────┼──────────────┤
│ 8f4b2e7c19d34a60b0a7e2c6f9d12011 │ Yuanbo iPhone        │ trusted  │ 2026-05-14   │ 2m ago       │
│ 31d0ac1f77194cb28de063ad92ff875e │ iPad Pro             │ trusted  │ 2026-05-10   │ 3d ago       │
└──────────────────────────────────┴──────────────────────┴──────────┴──────────────┴──────────────┘
```

### `tunnel pair`

The interactive pending screen should keep the next action visible below the QR code.

```text
Pair this computer with a mobile device

Scan the QR code with the Tunnel mobile app.

Expires in: 04:58
Cancel:     Ctrl-C

Waiting for mobile confirmation...

Mobile device requesting trust:
  Device:      Yuanbo iPhone
  Fingerprint: 8f4b2e7c19d34a60b0a7e2c6f9d12011

Enter 6-digit code shown on your phone: 123456

Paired Yuanbo iPhone.
```

### Daemon Startup Failure From `tunnel run`

```text
tunnel run requires the local daemon, but the daemon is not reachable.

Next step:
  tunnel daemon start

Details:
  local control socket did not respond within 5s
```

## Success Criteria

- A new user can understand the CLI command groups from `tunnel --help` without learning internal broker or tmux details.
- `tunnel daemon` reads as local background daemon management only.
- `tunnel pair` reads as the home for mobile pairing and trust management.
- `tunnel workspace` reads as a view into the local daemon workspace, not as session management.
- `tunnel session list` clearly shows local versus remote machines and `run` versus `mobile` launch source.
- `tunnel session stop <session-id>` works for both local-created and mobile-created live sessions.
- `tunnel run` always produces a broker-visible local session or fails before launching the user command.
- Missing daemon and missing tmux states produce actionable, user-friendly messages.
- The default CLI output feels intentionally designed: aligned, readable, copy-friendly, and free of internal terminology.

## Scope Boundaries

- No compatibility aliases for removed public commands in this revision.
- No public `tunnel run --daemon` option in this revision.
- No automatic tmux installation in this revision.
- No system service installation, launchd setup, systemd setup, or login auto-start setup in this revision.
- No `tunnel workspace sessions` command in this revision.
- No `tunnel session start` command in this revision.
- No local-only session stop path in this revision unless later planning finds it necessary for correctness.
- No mobile app UI redesign in this document beyond CLI-visible pairing, trust, and session metadata expectations.
- No direct QUIC, TLS, STUN, or relay-fallback protocol redesign in this document; this document is only the CLI and local UX readiness layer.

## Key Decisions

- Clean break is acceptable because Tunnel is still in development and has not been externally released.
- The daemon is required because mobile-created sessions and local-created sessions need one local broker-visible model.
- `tunnel run` should fail before launching the user command if it cannot reach the required daemon.
- `tunnel daemon start` should be safe to run repeatedly.
- Pairing belongs at top level because it is a first-class user action, not a daemon implementation detail.
- `devices` and `revoke` belong under `pair` because they operate on pairing trust.
- Workspace open and close belong under `workspace` because they are tmux workspace view actions, not daemon lifecycle actions.
- Session list and stop remain account-level and relay-visible, not tmux workspace inventory commands.
- tmux should be checked and explained, but not automatically installed.
- The CLI should prefer carefully designed tables and copyable identifiers over minimal raw dumps.

## Dependencies / Assumptions

- The local daemon broker can represent all `tunnel run` sessions launched on this machine.
- Session metadata can expose launch source as `run`, `mobile`, or `unknown` without inferring from fragile implementation details.
- The CLI can determine whether a listed session belongs to the current machine using stable local daemon or device identity.
- The daemon can report trusted device fingerprints in the same spelling required by revoke.
- The installer/update path has a place to run a tmux availability check and print non-blocking guidance.
- The relay remains the account-wide live session authority for `tunnel session list` and `tunnel session stop`.

## Outstanding Questions

### Resolve Before Planning

- None.

### Deferred to Planning

- [Affects R11-R14][Technical] What exact startup sequence should `tunnel run` use to start or verify the daemon without weakening relay registration gating?
- [Affects R12][Technical] What broker registration acknowledgement is sufficient before the user command starts?
- [Affects R31-R35][Technical] What exact metadata field should represent launch source so `run`, `mobile`, and `unknown` are not inferred from legacy command paths?
- [Affects R33][Technical] Which local identity should classify a session as `local` versus `remote`?
- [Affects R18-R23][Technical] Should `tunnel pair` support a non-interactive JSON mode for automation, or should automation wait until the interactive UX is stable?
- [Affects R40-R50][Design] What table rendering library or local helper should own width measurement, truncation, and terminal fallback behavior?

## Next Steps

-> `/prompts:ce-plan` for structured implementation planning
