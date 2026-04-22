---
date: 2026-04-22
topic: unified-session-management
---

# Unified Session Management

## Problem Frame

Tunnel currently has two visible session paths: users start local interactive sessions with `tunnel run`, while mobile clients can ask a machine daemon to start sessions inside the daemon-managed tmux workspace. Both paths ultimately create a live `tunnel run` session, but the user-facing management surface is split between relay session APIs, daemon workspace commands, and daemon-specific terminate behavior.

The desired product model is simpler: `tunnel run` remains the only local session start command, `tunnel session` manages live Tunnel sessions, and `tunnel daemon` manages only the background device-launch daemon and its tmux workspace view.

## Requirements

**Command Surface**
- R1. `tunnel run <command>` remains the only local CLI command for starting a foreground Tunnel session.
- R2. Do not add `tunnel session start` in this revision.
- R3. Add `tunnel session list` as the primary CLI command for listing live Tunnel sessions for the current account.
- R4. Add `tunnel session stop <session-id>` as the primary CLI command for stopping a live Tunnel session.
- R5. Keep `tunnel daemon start`, `tunnel daemon stop`, `tunnel daemon status`, `tunnel daemon doctor`, `tunnel daemon open`, and `tunnel daemon close` focused on daemon and workspace lifecycle, not general session management.
- R6. De-emphasize `tunnel daemon sessions` because it lists daemon tmux workspace sessions rather than account-level Tunnel sessions; it should not be presented as the main session list command.

**Session Listing**
- R7. `tunnel session list` lists all currently online Tunnel sessions visible to the authenticated account, including sessions on other machines.
- R8. The list must distinguish current-machine sessions from sessions running on other machines.
- R9. The list must distinguish local-created sessions from mobile-created sessions.
- R10. The list must use the existing session `label` concept for user-provided session naming; do not introduce a separate `name` concept.
- R11. The default list output must use a bordered table so dense session metadata remains readable and rows can be matched to columns.
- R12. The default table must not use emoji because terminal width handling can break table alignment.
- R13. The default table must omit `platform_id`; remote machine display should use the best available machine name without appending platform details.

**Table Shape**
- R14. The default table columns are `Scope`, `Source`, `Session`, `Label`, `Command`, `Machine`, `CWD`, and `Age`.
- R15. `Scope` values are `local`, `remote`, or `unknown`.
- R16. `Source` values are `local` or `mobile`, with missing or unknown metadata displayed as `local`.
- R17. `Session` must remain copyable and should not be truncated under normal terminal widths.
- R18. Empty `Label` values display as `-`.
- R19. Long `Label`, `Command`, and `Machine` values use fixed maximum widths and tail truncation.
- R20. Long `CWD` values use fixed maximum width and middle truncation so both the leading path context and final directory remain visible.
- R21. The table should prioritize stable alignment over showing every byte of long metadata.

Example default shape:

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

**Session Stop**
- R22. `tunnel session stop <session-id>` stops the target live Tunnel session, regardless of whether it was started directly with `tunnel run` or launched by a daemon.
- R23. Mobile session stop and local CLI session stop should use the same product semantics: stop the target `tunnel run` session.
- R24. Stopping a daemon-launched session should stop the Tunnel session without killing the daemon process or treating daemon workspace view lifecycle as part of session stop.
- R25. In this revision, route both mobile and local CLI stop through the relay to preserve one stop path, one authorization model, and one account-wide session list.
- R26. Do not add a local-only session registry or per-session local control socket in this revision.
- R27. A successful stop message should include the stopped session id and enough machine context to make remote stops clear.

**Future Connection Model**
- R28. This revision must not implement device-level long-connection multiplexing, but session list and stop semantics should not depend on the current one-socket-per-concern shape.
- R29. The stop concept should be expressible as a routable session control message so a later single-connection-per-device model can carry it without changing user-facing behavior.

## Success Criteria

- A user can run `tunnel session list` and see all online sessions for their account in a stable bordered table.
- The table clearly shows whether each session is local or remote.
- The table clearly shows whether each session was created locally or from mobile.
- A user can stop any listed online session with `tunnel session stop <session-id>`.
- Mobile stop and local CLI stop behave consistently because both target the live `tunnel run` session through the relay.
- Daemon commands remain understandable as daemon/workspace lifecycle commands rather than general session management commands.
- The chosen stop model remains compatible with a future relay/device/client connection multiplexing refactor.

## Scope Boundaries

- No `tunnel session start` in this revision.
- No local-only stop fast path in this revision.
- No local session registry, per-session local control socket, or local session pid inventory in this revision.
- No new command filters such as `--local` in the first version.
- No emoji in the default bordered session table.
- No `platform_id` display in the default table.
- No daemon workspace killing as part of normal `tunnel session stop`.
- No one-connection-per-device multiplexing refactor in this revision.

## Key Decisions

- Keep `tunnel run` as the only local start path: local session creation is already simple and does not need a second spelling.
- Route CLI stop through the relay first: this keeps local CLI and mobile stop behavior aligned and avoids adding a separate local registry.
- Treat `session stop` as stopping the live Tunnel session: daemon and workspace lifecycle remain owned by `tunnel daemon` commands.
- Use `label`, not `name`: the product already has label metadata, so a new naming concept would add confusion.
- Use a bordered, fixed-width table: dense session metadata is easier to scan when columns are stable and long values are shortened predictably.
- Keep stop as a routable session control action: this avoids tying the product behavior to today's separate relay websocket paths and leaves room for a later multiplexed long-connection transport.

## Dependencies / Assumptions

- The relay remains the account-wide live session authority.
- The session metadata model can reliably expose whether a session was created locally or from mobile.
- The CLI can authenticate to the relay for account-level session list and stop operations using an appropriate local credential path.
- Current-machine detection needs a reliable local identity comparison; when that cannot be determined, `Scope` should be `unknown` rather than guessed.

## Outstanding Questions

### Resolve Before Planning

- None.

### Deferred to Planning

- [Affects R8][Technical] What exact local identity should `tunnel session list` compare against relay session metadata to classify `local` versus `remote`?
- [Affects R9][Technical] What explicit metadata field should represent local-created versus mobile-created launch source so the CLI does not infer source from legacy daemon terminate metadata or launch request correlation?
- [Affects R25][Technical] Should the relay expose CLI session list/stop through existing app-facing APIs, agent-token-compatible APIs, or a dedicated CLI-oriented auth path?
- [Affects R22-R24][Technical] What agent-side stop control message and shutdown behavior cleanly stop a `tunnel run` session without conflating session stop with daemon tmux workspace termination?

## Next Steps

-> `/prompts:ce-plan` for structured implementation planning
