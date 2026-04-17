---
date: 2026-04-17
topic: mobile-device-session-launch
---

# Mobile Device Session Launch

## Problem Frame

Today the mobile client can only discover and attach to `tunnel` sessions that are already online. That is not enough for the common workflow where the user is away from their computer and wants to start a fresh terminal session from their phone.

The target experience is: the user explicitly enables remote launch on one computer with `tunnel daemon start`, then later opens the mobile app, chooses that online device, submits a command, and the chosen computer opens a new visible desktop terminal window and starts `tunnel run <command>` there. This creates a brand-new `tunnel` session on that machine without requiring the user to first start the session locally by hand.

This is a new product capability, not an extension of attach. It introduces an online device concept alongside the existing online session concept, plus a new explicit daemon-management CLI surface. The daemon should not depend on the system's default terminal; instead it should infer and remember a terminal-launch recipe from the environment where `tunnel daemon start` was run.

```mermaid
flowchart TB
    A[Desktop user runs tunnel daemon start] --> B[Daemon connects to relay]
    B --> C[Mobile app lists online devices]
    C --> D[User selects one device]
    D --> E[User submits command]
    E --> F[Relay validates app auth and device availability]
    F --> G[Relay forwards launch request to device daemon]
    G --> H[Device daemon validates command allowlist busy state and launcher health]
    H --> I[Device opens terminal via remembered launcher recipe]
    I --> J[Terminal runs tunnel with the requested command]
    J --> K[New tunnel session registers with relay]
    K --> L[User later sees the new session in session list]
```

## Requirements

**Device Model**
- R1. The relay must expose an online device concept separate from the existing online session concept.
- R2. A device must become online only after the user has explicitly started its device-side daemon with a `tunnel daemon` command on that machine.
- R3. The mobile client must list only the authenticated user's currently online devices.
- R4. The device name shown to the mobile client must default to the machine hostname.
- R5. Device metadata exposed to the mobile client must distinguish between a device that is online and launch-healthy versus online but degraded for launch.

**Launch Request Flow**
- R6. The mobile client must allow the user to choose one online device and submit one command string as a launch request.
- R7. A device is launchable only while its device-side daemon is currently connected to the relay and its remembered terminal launcher recipe is healthy.
- R8. A successful launch request must instruct the selected device to create a brand-new `tunnel` session on that machine.
- R9. The mobile client launch flow must stop at launch result reporting; it must not automatically attach to the newly created session.
- R10. The new session must appear through the normal session discovery flow after `tunnel` registers it.

**Daemon CLI**
- R11. The selected machine must run a long-lived local daemon that stays connected to the relay and can receive launch requests even when no `tunnel` session is currently online.
- R12. The existing `tunnel run <command>` execution path must remain unchanged; daemon management must be introduced through explicit `tunnel daemon ...` subcommands instead of changing the primary launch behavior.
- R13. v1 must provide at least these daemon-management commands: `tunnel daemon start`, `tunnel daemon status`, `tunnel daemon stop`, and `tunnel daemon doctor`.
- R14. `tunnel daemon start` must start a background daemon process that remains alive until it is explicitly stopped or otherwise exits unexpectedly.
- R15. `tunnel daemon stop` must terminate the daemon and remove that device from the online device list.
- R16. `tunnel daemon status` must be read-only and must not perform active health probing as part of its normal behavior.
- R17. `tunnel daemon status` must report at least whether the daemon is running, whether it is connected to the relay, whether its launcher recipe is healthy or degraded, which launcher strategy it is bound to, and the most recent launch failure reason when one exists.
- R18. `tunnel daemon doctor` must provide a deeper user-invoked diagnostic path beyond the normal read-only `status` command.
- R19. `tunnel daemon doctor` must be local-first but include relay connectivity as one diagnostic check rather than treating relay reachability as the only thing that matters.
- R20. `tunnel daemon doctor` must perform light active probing, such as validating relay reachability, local GUI-session availability, launcher-recipe viability, `tunnel` executable availability, and daemon config readability, without launching a real terminal window as part of the check.
- R21. The default `tunnel daemon doctor` output must be a human-oriented checklist with per-check `ok`, `warn`, or `fail` status and a short factual explanation.
- R22. `tunnel daemon doctor` must not include prescriptive repair suggestions in its normal output.
- R23. `tunnel daemon doctor` must return exit code `0` only when all checks are `ok`; any `warn` or `fail` result must return a non-zero exit code.

**Launcher Recipe and Terminal Launch**
- R24. `tunnel daemon start` must infer a terminal launcher recipe from the environment where it was run and persist that recipe for later launches.
- R25. The daemon must reuse the remembered launcher recipe for subsequent launches instead of re-detecting the terminal strategy on every request.
- R26. If the remembered launcher recipe becomes invalid, the daemon must remain online but move into a degraded launch state until the user explicitly restarts it.
- R27. v1 must not automatically re-infer or self-heal the launcher recipe after launch failures.
- R28. A successful launch request must open a new visible desktop terminal window through the remembered launcher recipe and run `tunnel run <requested command>` there so the resulting shell session is visible and locally inspectable on the machine.
- R29. v1 must not place the launched session into an existing terminal window as a new tab.
- R30. When the launched `tunnel run <requested command>` process exits, the terminal window must remain open and return to an interactive shell prompt rather than closing automatically.
- R31. This feature is only required when the target machine already has an active desktop session capable of showing a terminal surface.
- R32. If `tunnel daemon start` is run on a machine without a supported desktop terminal-launch environment, it must fail instead of advertising the device as launchable.

**Command Allowlist**
- R33. The device daemon must read a user-editable global configuration file that defines the allowed command whitelist.
- R34. The whitelist decision must be based only on the first token of the requested command string, treated as the main command name.
- R35. The product should ship with a default whitelist that includes common agent commands such as `codex`, `claude`, and `gemini`.
- R36. The user must be able to add and remove allowed main commands in the global configuration file.
- R37. If the requested command's first token is not in the whitelist, the daemon must reject the launch request.
- R38. If the requested command's first token is in the whitelist, the remaining arguments must be passed through without additional product-level restriction in v1.

**Authorization and Safety**
- R39. After a device has been enrolled to a user account, the default v1 behavior is direct execution without per-launch local confirmation.
- R40. The relay must authorize device discovery and launch so a user can only target devices they own.
- R41. The relay and device must treat launch as a distinct capability from attach; existing session attach authorization rules must remain unchanged.

**Concurrency and Result Reporting**
- R42. Each device may have at most one launch request in progress at a time.
- R43. If a second launch request arrives while another launch request is still in progress for that device, the second request must fail immediately rather than queue.
- R44. The mobile client must receive a structured success or failure result for the launch request.
- R45. Failure results must use structured reasons rather than a generic failure string.

**Failure Reasons**
- R46. The v1 failure model must distinguish at least these cases when they are known: `device_offline`, `busy`, `command_not_allowed`, `desktop_unavailable`, `terminal_launch_failed`, and `tunnel_not_found`.

**Platform Scope**
- R47. v1 must support macOS and Linux desktop environments with a usable desktop GUI session.
- R48. v1 must not require Windows support.

## Success Criteria

- A signed-in mobile user can see their currently online devices even when none of those devices currently has a live `tunnel` session.
- A user can explicitly enable and disable the device-side launch capability with `tunnel daemon start` and `tunnel daemon stop`.
- The user can submit an allowed command to one online device and receive a structured launch result.
- On a compatible desktop machine, a new visible terminal window opens and runs `tunnel run <command>`, producing a new session that later appears in the normal session list.
- When the launched `tunnel run <command>` exits, the new terminal window stays open and returns to an interactive shell prompt.
- A disallowed main command is rejected before local terminal launch.
- A second concurrent launch request to the same device fails with a structured busy result instead of creating duplicate sessions.
- If the remembered launcher recipe becomes invalid, the device remains online but is shown as degraded for launch until the user explicitly restarts the daemon.
- `tunnel daemon doctor` gives a stable `ok/warn/fail` checklist and a non-zero exit code whenever the machine is not fully healthy for remote launch.

## Scope Boundaries

- No Windows support in v1.
- No requirement to support launch when the target machine lacks a usable desktop GUI session.
- No automatic mobile-side attach or redirect into the new session after launch.
- No command profile system, placeholder templating, or per-command argument schema in v1.
- No per-launch local confirmation prompt in v1.
- No device list for offline or historical devices in v1; the mobile client only shows devices that are online now.
- No relay-managed durable queue of pending launch requests.
- No automatic recovery or silent rebinding to a different launcher recipe after launch failures in v1.
- No requirement that daemon lifetime be tied to the shell, tab, or window from which `tunnel daemon start` was run.
- No requirement to create new tabs inside existing terminal windows in v1.

## Key Decisions

- Separate online devices from online sessions: the mobile client first targets a device, and a new session is created only after the device launches `tunnel`.
- Use an explicitly managed device-side daemon: device-targeted launch is required even when no `tunnel` session is already online, but the user must opt into that capability with `tunnel daemon start` rather than system auto-start.
- Keep the existing launch UX stable: `tunnel run <command>` remains unchanged and daemon behavior lives under `tunnel daemon ...`.
- Keep command control simple: v1 uses a main-command whitelist from a global config file rather than profiles or template expansion.
- Allow full argument pass-through after whitelist match: the product constrains the executable family, not the full command shape.
- Make launch single-flight per device: one in-progress request at a time is easier to reason about than queueing or fixed cooldown windows.
- Keep mobile launch and session attach separate: launch reports success or failure, then the user can use the existing session discovery flow when ready.
- Bind launch behavior to a remembered launcher recipe, not to the system's default terminal.
- Keep launcher choice stable for one daemon lifetime: v1 does not auto-heal or silently switch terminal-launch strategies after failures.
- Standardize launch presentation on a new terminal window: opening a new window is easier for users to find than attaching to an existing window as a tab.

## Dependencies / Assumptions

- The target machine is already signed in to a desktop session where terminal automation can present a visible terminal surface.
- `tunnel daemon start` can infer a workable launcher recipe from the environment in which it was run when the host terminal setup is supported and a desktop GUI session is available.
- The device has `tunnel` installed and accessible in the execution environment used by the launched terminal surface.
- Device ownership and trust are already established by the broader account and agent-token model.

## Outstanding Questions

### Resolve Before Planning

- None.

### Deferred to Planning

- [Affects R1-R10][Technical] What exact relay protocol and API surface should represent online devices, device health, and launch requests without conflating them with live sessions?
- [Affects R11-R32][Needs research] What is the narrowest implementation shape for the `tunnel daemon` CLI and background process across macOS and the Linux desktop environments the project intends to support first?
- [Affects R19-R32][Needs research] What launcher-recipe inference and persistence model is stable enough for v1 across supported desktop environments?
- [Affects R33-R38][Technical] What config file path and format best fit the existing `tunnel` product surface while staying portable across macOS and Linux?
- [Affects R44-R46][Technical] How should launch success, device degraded state, structured failure reasons, and any doctor-visible daemon status be exposed in the public app-facing API and any daemon-facing relay protocol?

## Next Steps

→ `/prompts:ce-plan` for structured implementation planning
