# Browser Input TUI Parity

**Date:** 2026-04-02
**Status:** Approved

## Problem

When Claude Code, Codex, or similar TUI applications run inside agentunnel, interactive UI elements (menus, toggles, option lists) render correctly in the browser but cannot be controlled from the browser. Arrow keys, Tab, Space, Enter, and Escape are all ignored. The same input works fine from the local terminal.

Regular shell input from the browser works normally. The issue is specific to TUI interactive mode.

## Root Cause

The architecture connects **two terminal emulators** (local terminal + browser xterm.js) to a **single PTY** simultaneously. For simple character I/O this works fine, but TUI applications send **terminal query escape sequences** (cursor position, device attributes, color queries, etc.) to detect capabilities and state.

Both terminal emulators respond to these queries independently, causing the application to receive **duplicate, conflicting responses**. This corrupts the application's input parser state, making it unable to process subsequent keystrokes arriving from the browser. The local terminal continues to work because its responses are consistent with the PTY's actual state.

## Approach: Filter Terminal Auto-Responses from Browser

Prevent xterm.js's automatic query responses from being forwarded to the PTY via WebSocket. The browser remains a full-fidelity renderer, but its auto-responses are suppressed so they don't collide with the local terminal's responses. Regular user keystrokes (characters, arrows, tab, space, enter, escape) continue to flow through normally.

### Why this approach

- Surgical fix — one new file, one small edit, zero backend changes
- Browser rendering is unaffected (xterm.js still processes all escape sequences internally)
- Local terminal remains the authoritative responder for terminal queries
- Appropriate for v1 where a local terminal is always attached

### Rejected alternatives

- **Make browser the sole terminal:** Would break the current UX where local terminal is primary. More complex.
- **Terminal multiplexer (tmux-style):** Requires building a terminal emulator in Go server-side. Massive complexity, not practical for v1.

## Design

### New file: `web/src/input_filter.ts`

A single exported function:

```typescript
export function isTerminalAutoResponse(data: string): boolean
```

Returns `true` if the string is a terminal auto-response that should NOT be forwarded to the PTY.

### Patterns to filter

| Pattern | Meaning |
|---|---|
| `\x1b[<digits>;<digits>R` | Cursor Position Report (CPR) |
| `\x1b[?<digits>;<digits>R` | Extended CPR (DECXCPR) |
| `\x1b[?<digits>;<digits>;...c` | Primary Device Attributes (DA1) |
| `\x1b[><digits>;<digits>;...c` | Secondary Device Attributes (DA2) |
| `\x1b]10;rgb:...<ST>` | Foreground color query response |
| `\x1b]11;rgb:...<ST>` | Background color query response |

Where `<ST>` is either `\x1b\\` (ESC backslash) or `\x07` (BEL).

### Patterns that must NOT be filtered

| Pattern | Meaning |
|---|---|
| Regular characters (`a`, `Y`, `n`) | User typing |
| `\r`, `\t`, `\x1b`, ` ` | Enter, Tab, Escape, Space |
| `\x1b[A` through `\x1b[D` | Arrow keys |
| `\x1b[1;5C` etc. | Modified arrow keys (Ctrl+arrow) |
| `\x1bOP` through `\x1bOS` | F1-F4 |
| `\x1b[15~` etc. | F5-F12 |

### Integration point: `web/src/main.ts`

Wrap the existing `onData` handler:

```typescript
import { isTerminalAutoResponse } from './input_filter'

terminal.onData((str) => {
  if (!isTerminalAutoResponse(str)) {
    conn.send(encodeInput(str))
  }
})
```

### Backend changes

None. The Go server, protocol, hub, and session code are unchanged.

## Testing

### Unit tests: `web/src/input_filter.test.ts`

Test `isTerminalAutoResponse` with:

**Filtered (returns true):**
- `\x1b[24;80R` — CPR
- `\x1b[?24;80R` — extended CPR
- `\x1b[?1;2c` — DA1
- `\x1b[>0;0;0c` — DA2
- `\x1b]10;rgb:ffff/ffff/ffff\x1b\\` — foreground color (ESC terminator)
- `\x1b]11;rgb:0000/0000/0000\x07` — background color (BEL terminator)

**Not filtered (returns false):**
- `a`, `Y`, `n` — characters
- `\r`, `\t`, `\x1b`, ` ` — enter, tab, escape, space
- `\x1b[A` through `\x1b[D` — arrow keys
- `\x1b[1;5C` — Ctrl+Right (digits;digits + letter, but letter is C not R)

### Existing tests

`make test` and `make build` must continue to pass.

### Manual verification

1. Run `agentunnel` with Claude Code
2. Open browser client
3. Trigger `/plugins` or any interactive menu
4. Verify arrow keys, space, tab, enter, escape all work from browser
5. Verify rendering still matches local terminal
