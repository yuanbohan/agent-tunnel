---
title: Keep terminal size on each output for retained output frames
date: 2026-04-04
category: docs/solutions/best-practices
module: relay
problem_type: best_practice
component: tooling
severity: medium
applies_when:
  - The relay is only a transient in-memory cache and the client owns long-term history
  - A native client syncs incrementally with a single sequence cursor
  - Terminal resize events can be missed across disconnect or attach boundaries
tags: [relay, terminal-output, seq-sync, mobile-client, terminal-size]
related_components: [protocol, relay]
---

# Keep terminal size on each output for retained output frames

## Context

The mobile terminal replay work needed a simpler relay contract than the existing web-first history model. The client wants to pull output into local storage, keep the highest `seq` it has seen, and request the retained range it needs later. In that setup, the relay is just a short-lived in-memory cache for live sessions, not the system of record.

That makes standalone `resize` history events fragile. If a resize is missed during a reconnect or attach gap, later output can be replayed with the wrong terminal width even though the output bytes themselves were retained correctly.

## Guidance

Use an output log, not a general terminal event log, when the client is responsible for long-term history.

- Assign `seq` only to output frames.
- Carry `cols` and `rows` on every retained output frame.
- Expose retained output through one range fetch: `GET /api/sessions/:id/frames?from=<seq>&to=<seq>`.
- Allow full sync by omitting both query parameters.
- Keep relay retention in-memory only when session loss on disconnect or restart is acceptable.

This keeps the relay contract narrow:

```json
[
  { "seq": 41, "data_b64": "...", "cols": 120, "rows": 40 },
  { "seq": 42, "data_b64": "...", "cols": 132, "rows": 43 }
]
```

For live output after catch-up, the client keeps one global WebSocket:

```text
GET /api/updates/ws
```

and routes incoming `output` frames by `session_id`.

## Why This Matters

This model matches the actual ownership boundary:

- The client owns durable history and replay.
- The relay only smooths over short disconnects and late attaches.
- `seq` stays a pure output-sync cursor instead of becoming a mixed event-log cursor.

Putting `cols` and `rows` on each output frame removes a major replay failure mode. Even if a resize signal is lost, any later retained output still carries the size it was produced under. The client can `resize(cols, rows)` immediately before writing that frame to its emulator.

This is also simpler than generic pagination for a demo-stage client. There is one fetch shape, one sequence space, and one retained message type.

## When to Apply

- A mobile or desktop client stores terminal history locally after first sync.
- The relay is allowed to drop retained history on process restart or agent disconnect.
- The main goal is correct replay of output, not complete auditability of all terminal events.
- A standalone `resize` event would require extra anchoring rules to make output frame ranges replayable.

## Examples

Before:

```json
{ "type": "resize", "cols": 132, "rows": 43 }
{ "type": "output", "seq": 42, "data": "..." }
```

If the resize frame is missed but the output survives in the relay buffer, the client may replay frame `42` with stale dimensions.

After:

```json
{ "type": "output", "seq": 42, "data": "...", "cols": 132, "rows": 43 }
```

Now the output frame is self-describing. A reconnecting client can either do a full sync or request an exact retained sequence range:

```text
1. First sync: GET /api/sessions/:id/frames
2. Persist outputs locally and store max seq
3. Reconnect later: GET /api/sessions/:id/frames?from=<missing-start>&to=<missing-end>
4. Resume the global `GET /api/updates/ws` connection and continue from live output there
```

## Related

- [docs/protocol.md](../../protocol.md) defines the current output frame range contract and the global live update stream.
- [docs/architecture.md](../../architecture.md) describes how the relay stamps `cols` and `rows` onto retained and live output frames.
