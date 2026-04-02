# Browser Resize Decoupling

## Problem

The current implementation shares PTY resize control between all sinks (local terminal and browser). When the user resizes the browser (e.g. on a mobile phone), it changes the PTY size, which affects the local terminal's display. This creates a surprising experience: the user resizes their phone browser, walks back to their desk, and finds their desktop terminal reformatted to phone dimensions.

## Use Case

The primary workflow is desktop-first: the user runs `agentunnel` with a CLI tool (claude, codex, gemini) and works in their local terminal. When away from the desk (eating, walking), they check the browser on their phone to monitor output and occasionally confirm prompts. The local terminal stays open and running the entire time.

The browser is a **monitoring + occasional interaction** tool, not a primary workspace.

## Design

### Resize Ownership

Only the local terminal controls the PTY size. The browser is a passive viewer.

- `forwardLocalTerminalResizes` in `local_terminal.go` continues to call `Hub.Resize()` on SIGWINCH -- no change.
- The WebSocket handler in `server.go` stops routing `"resize"` messages to `Hub.Resize()`. The `case "resize"` is removed from the WebSocket read loop.
- The browser stops sending `"resize"` messages over WebSocket.

### PTY Size Notification (Server to Browser)

The `"resize"` message type is reused, but the direction flips: it becomes server-to-browser only.

- The Hub stores the current `cols` and `rows` after each successful `Resize()` call.
- After updating the PTY, the Hub broadcasts a `{"type": "resize", "cols": N, "rows": N}` message to all sinks. The local terminal sink ignores message types it does not handle, so this is safe -- only WebSocket sinks act on it.
- On initial WebSocket connection, the server sends the current PTY size immediately so the browser knows the dimensions before any local terminal resize occurs.

No new message types are added. The protocol stays at three types: `"input"`, `"output"`, `"resize"`.

### Browser Display Modes

Two modes, toggled by a floating button:

**Scroll mode (default):**

- xterm.js cols/rows are set to match the PTY size (received via `"resize"` message from server).
- If the phone screen is narrower than the terminal, the container uses `overflow-x: auto` for horizontal scrolling.
- Output looks identical to the desktop terminal.
- When a `"resize"` message arrives from the server, xterm.js updates its cols/rows to match.

**Wrap mode:**

- xterm.js cols are set to fit the phone screen width (using FitAddon).
- PTY output wraps at the phone's column width.
- Incoming `"resize"` messages from the server are stored but do not change xterm.js cols.
- Switching back to Scroll mode applies the stored PTY size immediately.

### Floating Toggle Button

- Semi-transparent icon positioned in the bottom-right corner of the terminal container.
- Displays a wrap/scroll icon indicating the current mode.
- One tap toggles between Scroll and Wrap modes.
- Sized for touch targets (~40x40px), small enough to not obstruct terminal content.

## Changes Per File

### `internal/session/hub.go`

- Add `cols` and `rows` fields to the Hub struct.
- After a successful `resizePTY()` call in `Resize()`, store the new cols/rows.
- Broadcast a `"resize"` message to all sinks with the new dimensions.
- Add a `CurrentSize() (int, int)` method for the server to query on new WebSocket connections.

### `internal/server/server.go`

- Remove the `case "resize"` from the WebSocket read loop.
- On new WebSocket connection, send the current PTY size as a `{"type": "resize", ...}` message immediately after adding the sink.

### `web/src/terminal.ts`

- Remove the `ResizeObserver` callback that emits resize events.
- Add a method to set xterm.js cols/rows externally (for Scroll mode).
- Keep FitAddon for Wrap mode.
- Expose a method to toggle between Scroll and Wrap modes.

### `web/src/main.ts`

- Remove the `terminal.onResize()` handler that sends resize to WebSocket.
- Remove the resize send on reconnect.
- Add a handler for incoming `"resize"` messages that stores PTY size and applies it based on current display mode.
- Add floating button creation and toggle logic.

### `web/index.html`

- Add the floating button element and styling.

### No changes

- `internal/session/local_terminal.go` -- unchanged, still forwards SIGWINCH to Hub.
- `internal/session/process.go` -- unchanged, Hub initialization stays the same.
- `internal/agent/handler.go` -- unchanged, legacy remote agent path.
- `internal/agent/pty.go` -- unchanged, core PTY resize function.
- `internal/client/session.go` -- unchanged, legacy client.
- `internal/protocol/message.go` -- unchanged, same Message struct.
