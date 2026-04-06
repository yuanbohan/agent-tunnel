---
date: 2026-04-06
topic: agentunnel-macos-sleep-prevention
---

# macOS Sleep Prevention During Agentunnel Sessions

## Problem Frame

When `agentunnel` starts a supported launcher, the machine can still fall asleep under normal macOS power management. That breaks the intended operating mode for long-running local agent sessions, because the local terminal, PTY owner, and relay connectivity all depend on the machine staying awake.

The desired product behavior is straightforward: on macOS, `agentunnel` should try to keep the machine awake for as long as the `agentunnel` process is running. This should be on by default, should not require extra user configuration, and should be visible in the startup confirmation so the operator immediately knows whether sleep prevention is active.

## Requirements

**Sleep Prevention Behavior**
- R1. On macOS, `agentunnel` must attempt to enable idle system sleep prevention when starting a supported launcher session.
- R2. When sleep prevention starts successfully, it must remain active for the lifetime of the `agentunnel` process.
- R3. Sleep prevention must be enabled by default in this phase and must not require a user flag or opt-in setting.
- R4. This phase defines sleep prevention for the `agentunnel` process lifetime, not only for the child launcher process lifetime.

**Failure Handling and User Visibility**
- R5. If sleep prevention cannot be started, `agentunnel` must continue normal startup instead of exiting.
- R6. Startup output must clearly indicate whether sleep prevention is active or failed to start.
- R7. Sleep prevention status must appear in the same concise startup confirmation flow as the existing relay/startup status rather than in noisy repeated logs.

**Platform Scope and Documentation**
- R8. This phase only requires supported behavior on macOS.
- R9. Operator-facing documentation must explicitly state that sleep prevention is default-on for macOS sessions in this phase.
- R10. Operator-facing documentation must explicitly state that sleep prevention failure does not block session startup.

## Success Criteria

- A macOS user can start `agentunnel` and immediately see from the startup line whether sleep prevention is active.
- A long-running `agentunnel` session on macOS does not enter normal idle system sleep while the process remains alive.
- If sleep prevention cannot be enabled, the session still starts and the user is told that the machine is not being kept awake.
- Documentation matches runtime behavior about scope, defaults, and failure semantics.

## Scope Boundaries

- No cross-platform sleep prevention behavior is required in this phase.
- No user-facing flag to disable or enable sleep prevention is included in this phase.
- This phase is about preventing normal system sleep while `agentunnel` is running; it does not define behavior for manual sleep, lid-close behavior, or broader power-management policy changes.
- This change does not expand `agentunnel` beyond its current supported launcher set.

## Key Decisions

- macOS-only first: The first version should solve the target operator environment cleanly instead of introducing premature cross-platform behavior.
- Default-on with no toggle: Sleep prevention is treated as part of the product's core long-running session behavior, not an optional advanced mode.
- Process-lifetime semantics: The user wants the machine kept awake for as long as `agentunnel` itself is alive, even if child-process state changes during that lifetime.
- Failure is visible but non-blocking: Sleep prevention improves session reliability, but failure should not prevent local work from starting.
- Startup banner carries the signal: The operator should learn sleep-prevention state from the initial confirmation line, not from secondary logs.

## Dependencies / Assumptions

- `cmd/agentunnel` owns the startup confirmation flow and is the natural place where sleep-prevention state becomes operator-visible.
- The implementation will rely on a macOS-supported system mechanism for preventing idle sleep, but the exact mechanism is a planning detail.

## Outstanding Questions

### Deferred to Planning

- [Affects R6][Technical] What exact startup line wording best communicates relay state and sleep-prevention state without making the banner noisy?
- [Affects R8][Technical] What should non-macOS builds or runs do in code terms: compile out the feature, no-op with no banner status, or surface an explicit unsupported state?

## Next Steps

→ `/prompts:ce-plan` for structured implementation planning
