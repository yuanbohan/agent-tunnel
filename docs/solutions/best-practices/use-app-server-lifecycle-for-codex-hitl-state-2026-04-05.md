---
title: Use App Server lifecycle as the source of truth for Codex HITL state
date: 2026-04-05
category: docs/solutions/best-practices
module: agentunnel
problem_type: best_practice
component: tooling
severity: high
applies_when:
  - A client needs reliable session-level action-required notifications for Codex
  - The existing integration only sees PTY output or terminal frames
  - Downstream consumers must recover current waiting state after reconnect
tags: [codex, app-server, action-required, hitl, relay, android]
related_components: [protocol, relay]
---

# Use App Server lifecycle as the source of truth for Codex HITL state

## Context

The Android notification flow needed to know when a Codex session had truly entered a human-in-the-loop blocking state. The existing `agentunnel -> PTY -> relay` integration only exposed terminal bytes, unread counters, and resize/output frames. That was enough for terminal replay, but not enough to answer the real product question: "is Codex currently blocked on human participation?"

During implementation we verified that terminal output is the wrong abstraction boundary for this. Codex can keep printing explanation text while already waiting, and prompt wording is not a stable machine contract.

## Guidance

Treat `action_required` as structured session metadata derived from Codex App Server lifecycle, not from terminal text.

- Run Codex as `codex app-server` plus `codex --remote`, with the wrapper owning both processes.
- Detect waiting from App Server state such as `waitingOnApproval` and `waitingOnUserInput`.
- Publish a session-level state model only: `normal` or `action_required`.
- Carry the current state in the session snapshot API and emit transitions on a separate session-state stream.
- Keep terminal history clean: do not inject state frames into PTY output history.
- Clear `action_required` only when the structured runtime says waiting is resolved, not when the local client merely typed something.

The resulting boundary looks like this:

```text
codex app-server -> structured waiting lifecycle
                -> agentunnel session_state frame
                -> relay snapshot + /api/session-events/ws
                -> Android session list + notification logic
```

## Why This Matters

This keeps the source of truth aligned with the real runtime state.

If the relay guesses from terminal text, false positives and false negatives are unavoidable:

- A message that looks like a prompt may only be explanatory output.
- Codex may stay blocked while continuing to print context.
- Reconnect logic cannot reliably deduplicate one unresolved waiting episode from repeated terminal output.

Using App Server lifecycle solves the right problem:

- Session List can expose current `state` and `action_required_since`.
- Android can notify only when the session actually enters waiting.
- Reconnects can restore the current state without replaying the terminal transcript heuristically.

## When to Apply

- You need reliable human-in-the-loop notifications outside the terminal UI.
- Downstream clients consume session snapshots and live transition events separately.
- The wrapper can own Codex process lifecycle and is allowed to run an App Server sidecar.
- Terminal output remains a presentation stream, not a semantic state channel.

## Examples

Before:

```text
codex TUI -> PTY bytes -> relay
```

The relay sees only terminal output and cannot prove whether a prompt is blocking.

After:

```text
agentunnel
  ├─ codex app-server --listen ws://127.0.0.1:0
  └─ codex --remote ws://127.0.0.1:<port>
```

The wrapper derives session state from the App Server and forwards it separately:

```json
{
  "session_id": "sess-1",
  "state": "action_required",
  "state_changed_at": "2026-04-05T01:33:34Z",
  "action_required_since": "2026-04-05T01:33:34Z"
}
```

Terminal history remains output-only, while live state transitions go through:

```text
GET /api/session-events/ws
```

## Related

- [docs/protocol.md](../../protocol.md) documents the session snapshot fields and session-state event stream.
- [docs/architecture.md](../../architecture.md) describes the App Server sidecar and relay session-state flow.
- [docs/solutions/best-practices/after-only-terminal-output-sync-2026-04-04.md](./after-only-terminal-output-sync-2026-04-04.md) covers the separate relay rule that terminal replay should stay output-centric.
