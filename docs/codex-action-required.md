# Codex App Server and Action Required Lifecycle

This document explains how `agentunnel` runs Codex, how `action_required` is derived, and how that state reaches the relay and downstream clients.

## Runtime Model

When the launcher is `codex`, `agentunnel` owns two local child processes instead of one:

```text
agentunnel
├─ codex app-server --listen ws://127.0.0.1:0
└─ codex --remote ws://127.0.0.1:<dynamic-port> ...
```

The `app-server` is a loopback-only sidecar. The PTY-attached process is the real interactive Codex session that the operator sees in the terminal.

`agentunnel` also runs two in-process loops around those children:

- a relay connector that forwards terminal bytes, resize updates, and session-state updates
- an app-server monitor that polls structured Codex thread state and converts it into relay session state

## Startup Sequence

The Codex startup path is:

1. Start `codex app-server --listen ws://127.0.0.1:0`.
2. Read its stdout/stderr until it reports both:
   - `listening on: <ws-url>`
   - `readyz: <http-url>`
3. Poll `readyz` until it returns `200 OK`.
4. Rewrite the PTY child command to `codex --remote <ws-url> ...`.
5. Start that rewritten command under the local PTY.
6. Start the app-server monitor and relay connector.

This ordering matters: the PTY session does not start until the sidecar is ready to accept `--remote` connections.

## Ownership and Shutdown

`agentunnel` is the supervisor for both Codex children.

- If the PTY child exits first, `agentunnel` returns from the main wait loop and then closes the app-server during deferred cleanup.
- If the app-server exits first, `agentunnel` treats that as an unexpected failure, returns from the main wait loop, and then kills the PTY child during deferred cleanup.
- On intentional app-server shutdown, `agentunnel` sends `SIGTERM`, waits up to 3 seconds, then escalates to `SIGKILL` if needed.

The important property is that `agentunnel` also calls `Wait()` on both child processes. That means the wrapper reaps the direct children it started instead of leaving zombie processes behind.

Practical caveat:

- This protects the normal shutdown path.
- If `agentunnel` itself is hard-killed by something outside its control, child reaping guarantees no longer apply because the supervisor is gone.

## Where `action_required` Comes From

`action_required` is derived from structured Codex runtime state, not terminal text.

The app-server monitor periodically opens a WebSocket connection to the local Codex app-server and calls:

1. `initialize`
2. `thread/list`

If any returned thread is:

- `status.type == "active"`, and
- `status.activeFlags` contains `waitingOnApproval` or `waitingOnUserInput`

then the session is considered `action_required`.

Otherwise the session is considered `normal`.

This gives a stable machine boundary:

```text
Codex app-server thread state
→ agentunnel monitor
→ session_state frame
→ relay session snapshot + client update stream
```

`agentunnel` does not infer waiting from PTY prompts, prompt wording, or terminal output timing.

## Relay Integration

The relay receives `session_state` frames over `/agent/ws` alongside terminal `output` and `resize` frames.

When the relay accepts a `session_state` update, it:

1. updates the live session snapshot
2. stores:
   - `state`
   - `state_changed_at`
   - `action_required_since`
3. broadcasts a `session_state` client update over `/api/updates/ws`

The relay does **not** insert session-state transitions into terminal history. Terminal replay remains output-only.

If a live `action_required` session disconnects, the relay clears the snapshot state internally and emits a `session_removed` client update so clients can evict the dead session from local state.

## State Semantics

Two session states are currently exposed:

- `normal`
- `action_required`

Interpret them as:

- `normal`: Codex is not currently blocked on explicit human participation
- `action_required`: Codex is currently waiting on approval or direct user input

`action_required_since` represents the start of the current unresolved waiting episode.

- It is set when the session enters `action_required`.
- It stays stable while that waiting episode remains unresolved.
- It becomes `null` again when the session returns to `normal`.

## Client Contract

For external clients, especially mobile:

- `GET /api/sessions` is the source of truth for the current state snapshot.
- `GET /api/updates/ws` is the preferred live transition stream.
- PTY output must not be parsed to infer whether the session is waiting.

Recommended client flow:

1. Fetch `/api/sessions`.
2. Render `state` and `action_required_since` from the session snapshot.
3. Subscribe to `/api/updates/ws` for future transitions.
4. On reconnect, re-fetch `/api/sessions` before trusting newly received events.

The live stream is not a replay log. It only carries transitions observed after the WebSocket is attached.

## Design Boundary

This model intentionally separates:

- terminal presentation state: PTY output and replay
- session coordination state: `normal` / `action_required`

That separation is what lets mobile notifications, session lists, and reconnect behavior stay reliable without parsing terminal text.
