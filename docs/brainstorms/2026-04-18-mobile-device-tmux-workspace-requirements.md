---
date: 2026-04-18
topic: mobile-device-tmux-workspace
---

# Mobile Device Tmux Workspace Launch

## Problem Frame

Today the mobile client can only discover and attach to `tunnel` sessions that are already online. That is not enough for the common workflow where the user is away from their computer and wants to start a fresh terminal session from their phone.

The product needs an explicit device-side launch capability, but the original "open a new visible desktop terminal window" direction creates the wrong dependency. It requires terminal-specific automation, raises cross-terminal reliability risk, and makes local display behavior part of the critical path for launch success.

The new direction is: the user explicitly enables remote launch on one computer with `tunnel daemon start`; the daemon manages a dedicated local `tmux` workspace for remotely created sessions; each remote launch creates one new `tmux` session inside that workspace; and the user can later inspect those sessions locally with `tunnel daemon open` or `tunnel daemon sessions` from any terminal they choose. Local display is no longer part of launch success.

This is still a new product capability, not an extension of attach. The mobile client targets an online device, asks it to create a new session, and receives a decisive launch result only when the created `tunnel` session becomes `session_ready`.

## Requirements

**Device Model**
- R1. The relay must expose an online device concept separate from the existing online session concept.
- R2. A device must become online only after the user has explicitly started its device-side daemon with a `tunnel daemon` command on that machine.
- R3. The mobile client must list only the authenticated user's currently online devices.
- R4. The device name shown to the mobile client must default to the machine hostname.
- R5. Device metadata exposed to the mobile client must distinguish between a device that is online and launch-healthy versus online but degraded for launch.

**Launch Request Flow**
- R6. The mobile client must allow the user to choose one online device and submit one command string as a launch request.
- R7. A successful launch request must instruct the selected device to create a brand-new `tunnel` session on that machine.
- R8. The mobile client launch flow must stop at launch result reporting; it must not automatically attach to the newly created session.
- R9. The new session must appear through the normal session discovery flow after `tunnel` registers it.
- R10. Launch success must mean `session_ready`, not merely that the daemon accepted the request or created a local workspace entry.

**Per-Launch Metadata**
- R11. The mobile client must send a working-directory path with each launch request.
- R12. The launch-specific working-directory path must apply only to that one launch request and must not reconfigure the daemon's long-lived startup state.
- R13. When a launch request includes a working-directory path, the selected device must start the new `tunnel run <command>` process in that directory, equivalent to locally changing into that directory before running the command.
- R14. If the supplied working-directory path does not exist or is otherwise unusable as a working directory, the daemon must reject the launch request before starting the session and return a structured cwd-related failure result.
- R15. The mobile client must be able to send an optional session label with each launch request.
- R16. When a launch request includes a session label, the created session must expose that label through the normal session metadata surface.

**Daemon CLI**
- R17. The selected machine must run a long-lived local daemon that stays connected to the relay and can receive launch requests even when no `tunnel` session is currently online.
- R18. The existing `tunnel run <command>` execution path must remain unchanged; daemon management must be introduced through explicit `tunnel daemon ...` subcommands instead of changing the primary launch behavior.
- R19. v1 must provide at least these daemon-management commands: `tunnel daemon start`, `tunnel daemon status`, `tunnel daemon stop`, `tunnel daemon doctor`, `tunnel daemon open`, and `tunnel daemon sessions`.
- R20. `tunnel daemon start` must start a background daemon process that remains alive until it is explicitly stopped or otherwise exits unexpectedly.
- R21. `tunnel daemon stop` must terminate the daemon and remove that device from the online device list.
- R22. `tunnel daemon status` must be read-only and must not perform active health probing as part of its normal behavior.
- R23. `tunnel daemon doctor` must provide a deeper user-invoked diagnostic path beyond the normal read-only `status` command.
- R24. `tunnel daemon open` must attach the user to the daemon-managed local `tmux` workspace.
- R25. `tunnel daemon sessions` must provide a thin, read-only list of sessions currently present in the daemon-managed local `tmux` workspace.

**Tmux Workspace**
- R26. Remote launch in v1 must require local `tmux` on the target machine.
- R27. If `tmux` is unavailable, `tunnel daemon start` must fail and tell the user how to install `tmux` for the current operating system.
- R28. The daemon must manage its own dedicated `tmux` server or socket instead of relying on the user's default `tmux` environment.
- R29. The daemon-managed `tmux` workspace must be reused across daemon restarts rather than recreated each time.
- R30. If the daemon starts and finds existing sessions in the daemon-managed `tmux` workspace, it must keep them and explicitly tell the user that they were preserved.
- R31. Each remote launch request must create one independent `tmux` session inside the daemon-managed workspace.
- R32. `tunnel daemon open` must work from any terminal the user chooses; it must not depend on remembering or detecting the terminal used to start the daemon.
- R33. If `tunnel daemon open` is run when the workspace contains sessions, it must attach using normal `tmux` behavior rather than a custom dashboard or chooser UI.
- R34. If `tunnel daemon open` is run when the daemon-managed workspace contains no sessions, it must not open `tmux`; it must tell the user that there are no daemon-managed sessions to open.
- R35. v1 must not introduce `tunnel`-managed per-session open, close, alias, or picker workflows beyond the thin `sessions` listing and the general `open` entry point.

**Session Lifetime**
- R36. A remote launch request must be allowed to succeed even though no local GUI terminal window is opened automatically.
- R37. The lifecycle of created sessions must not depend on terminal-specific automation or local GUI display.
- R38. When the launched command exits, the corresponding `tmux` session must remain available and return to an interactive shell instead of closing immediately.
- R39. If the daemon process exits unexpectedly or the user runs `tunnel daemon stop`, existing `tmux` sessions created through this workspace must remain intact.
- R40. A user must be able to re-enter the workspace later with `tunnel daemon open` and continue using surviving sessions.

**Concurrency and Launch Result**
- R41. A device may have at most one launch request in progress at a time.
- R42. If a second launch request arrives while another launch request is still in progress for that device, the second request must fail immediately rather than queue.
- R43. The mobile client must receive a structured success or failure result for the launch request.
- R44. A successful result must include the created `session_id`.
- R45. Failure results must use structured reasons rather than a generic failure string.
- R46. The relay and daemon must preserve a request-scoped correlation mechanism so the session that later registers can be matched back to the launch request that created it.
- R47. The creation flow may wait up to roughly 20-30 seconds for `session_ready` before returning a structured timeout failure instead of waiting indefinitely.

**Failure Reasons**
- R48. The v1 failure model must distinguish at least these cases when they are known: `device_offline`, `busy`, `command_not_allowed`, `tmux_not_found`, a cwd-related validation failure, session start failure before `session_ready`, and launch timeout before `session_ready`.

**Platform Scope**
- R49. v1 must support macOS and Linux desktop environments.
- R50. v1 must not require Windows support.
- R51. v1 must not require a GUI terminal application to be available or automatable in order for remote launch to function.

## Success Criteria

- A signed-in mobile user can see their currently online devices even when none of those devices currently has a live `tunnel` session.
- A user can explicitly enable and disable the device-side launch capability with `tunnel daemon start` and `tunnel daemon stop`.
- On a machine without `tmux`, `tunnel daemon start` fails early with an OS-appropriate installation recommendation.
- On a machine with `tmux`, a user can submit an allowed command plus required cwd and optional label to one online device and receive a structured launch result.
- A successful launch creates a new independent `tmux` session in the daemon-managed workspace, starts `tunnel run <command>` there, and returns `session_ready` with a concrete `session_id`.
- When the launched command exits, the `tmux` session remains available and returns to an interactive shell prompt.
- If the daemon is restarted or stopped after sessions have been created, those existing `tmux` sessions remain available and can be revisited with `tunnel daemon open`.
- `tunnel daemon open` re-enters existing daemon-managed sessions, while an empty daemon-managed workspace reports that there are no sessions to open instead of creating an empty tmux session.
- `tunnel daemon sessions` gives the user a thin view of the daemon-managed sessions without requiring a custom session-management layer.
- A missing or unusable cwd is rejected before session start with a structured failure result.
- A second concurrent launch request to the same device fails with a structured busy result instead of creating duplicate sessions.
- If `session_ready` does not arrive within the allowed wait window, the caller receives a structured timeout failure instead of an indefinitely pending request.

## Scope Boundaries

- No Windows support in v1.
- No automatic opening of a local GUI terminal window as part of remote launch.
- No terminal detection, remembered terminal recipe, or terminal-specific launch binding in v1.
- No custom workspace dashboard, chooser, notification system, or session picker layered on top of `tmux`.
- No `screen` backend in v1; the backend is `tmux`.
- No `tunnel`-managed per-session open, close, rename, alias, or shortcut commands in v1.
- No automatic mobile-side attach or redirect into the new session after launch.
- No command profile system, placeholder templating, or per-command argument schema in v1.
- No per-launch local confirmation prompt in v1.
- No device list for offline or historical devices in v1; the mobile client only shows devices that are online now.
- No relay-managed durable queue of pending launch requests.

## Key Decisions

- Separate online devices from online sessions: the mobile client first targets a device, and a new session is created only after the device launches `tunnel`.
- Use an explicitly managed device-side daemon: device-targeted launch is required even when no `tunnel` session is already online, but the user must opt into that capability with `tunnel daemon start` rather than system auto-start.
- Keep the existing launch UX stable: `tunnel run <command>` remains unchanged and daemon behavior lives under `tunnel daemon ...`.
- Make `tmux` the session backend: the daemon should not own long-lived PTYs directly when the product goal is to preserve sessions across daemon crashes and daemon stop.
- Use a dedicated daemon-managed `tmux` server: this avoids mixing remote-launch sessions into the user's normal `tmux` environment.
- Keep local display out of the critical path: remote launch success should not depend on opening or controlling a GUI terminal application.
- Keep the local workspace manual and thin: users enter it with `tunnel daemon open` from any terminal they choose, and session management stays close to default `tmux` behavior.
- Make launch success mean `session_ready`: the mobile flow should report success only after the new session has actually registered and can be identified by `session_id`.
- Make timeout explicit and terminal for the create flow: if `session_ready` does not arrive within the wait window, return a structured failure instead of leaving the client in an indeterminate state.
- Keep mobile launch and session attach separate: launch reports `session_ready` or failure, then the user can use the existing attach flow when ready.

## Dependencies / Assumptions

- The target machine has `tmux` installed and usable in the environment where the daemon runs.
- The target machine has `tunnel` installed and accessible in the execution environment used for launched sessions.
- The device can resolve and enter the caller-supplied cwd before starting the requested command when that cwd is valid.
- Device ownership and trust are already established by the broader account and agent-token model.

## Outstanding Questions

### Resolve Before Planning

- None.

### Deferred to Planning

- [Affects R24-R35] (Technical) What is the narrowest daemon-managed `tmux` shape that preserves reuse across daemon restarts while keeping `tunnel daemon open` and `tunnel daemon sessions` thin?
- [Affects R27] (Needs research) What operating-system detection and installation guidance should `tunnel daemon start` use for the first supported macOS and Linux environments?
- [Affects R31-R40] (Technical) What exact session-start wrapper is needed so a launched command returns to an interactive shell instead of closing the `tmux` session immediately?
- [Affects R43-R48] (Technical) What request correlation mechanism, timeout handling, timeout reason contract, and relay/session registration hooks should complete one pending launch with `session_ready` plus `session_id`?

## Next Steps

-> `/prompts:ce-plan` for structured implementation planning
