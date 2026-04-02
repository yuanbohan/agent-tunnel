# Browser Input TUI Parity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix browser input being ignored during TUI interactive mode by filtering xterm.js terminal auto-responses from the WebSocket input path.

**Architecture:** A new `input_filter.ts` module detects terminal auto-response patterns (CPR, DA1, DA2, OSC color replies) in xterm.js `onData` callbacks and prevents them from being sent to the PTY. The filter is integrated in `main.ts` with a one-line wrapper around the existing `onData` handler. No backend changes.

**Tech Stack:** TypeScript, xterm.js 5.x, Vitest

---

## File Structure

| File | Action | Responsibility |
|---|---|---|
| `web/src/input_filter.ts` | Create | `isTerminalAutoResponse()` — regex-based detection of terminal auto-responses |
| `web/src/input_filter.test.ts` | Create | Unit tests for the filter function |
| `web/src/main.ts` | Modify (line 39) | Wrap `onData` callback with filter |

---

### Task 1: Write the input filter with tests (TDD)

**Files:**
- Create: `web/src/input_filter.test.ts`
- Create: `web/src/input_filter.ts`

- [ ] **Step 1: Write the failing tests**

Create `web/src/input_filter.test.ts`:

```typescript
import { describe, it, expect } from 'vitest'
import { isTerminalAutoResponse } from './input_filter'

describe('isTerminalAutoResponse', () => {
  describe('filters terminal auto-responses', () => {
    it('filters Cursor Position Report (CPR)', () => {
      expect(isTerminalAutoResponse('\x1b[24;80R')).toBe(true)
      expect(isTerminalAutoResponse('\x1b[1;1R')).toBe(true)
      expect(isTerminalAutoResponse('\x1b[999;999R')).toBe(true)
    })

    it('filters Extended CPR (DECXCPR)', () => {
      expect(isTerminalAutoResponse('\x1b[?24;80R')).toBe(true)
      expect(isTerminalAutoResponse('\x1b[?1;1R')).toBe(true)
    })

    it('filters Primary Device Attributes (DA1)', () => {
      expect(isTerminalAutoResponse('\x1b[?1;2c')).toBe(true)
      expect(isTerminalAutoResponse('\x1b[?62;1;2;6;7;8;9;15c')).toBe(true)
    })

    it('filters Secondary Device Attributes (DA2)', () => {
      expect(isTerminalAutoResponse('\x1b[>0;0;0c')).toBe(true)
      expect(isTerminalAutoResponse('\x1b[>1;95;0c')).toBe(true)
    })

    it('filters foreground color query response (OSC 10, ST terminator)', () => {
      expect(isTerminalAutoResponse('\x1b]10;rgb:ffff/ffff/ffff\x1b\\')).toBe(true)
    })

    it('filters foreground color query response (OSC 10, BEL terminator)', () => {
      expect(isTerminalAutoResponse('\x1b]10;rgb:ffff/ffff/ffff\x07')).toBe(true)
    })

    it('filters background color query response (OSC 11, ST terminator)', () => {
      expect(isTerminalAutoResponse('\x1b]11;rgb:0000/0000/0000\x1b\\')).toBe(true)
    })

    it('filters background color query response (OSC 11, BEL terminator)', () => {
      expect(isTerminalAutoResponse('\x1b]11;rgb:0000/0000/0000\x07')).toBe(true)
    })
  })

  describe('passes through user input', () => {
    it('passes regular characters', () => {
      expect(isTerminalAutoResponse('a')).toBe(false)
      expect(isTerminalAutoResponse('Y')).toBe(false)
      expect(isTerminalAutoResponse('n')).toBe(false)
      expect(isTerminalAutoResponse('hello')).toBe(false)
    })

    it('passes Enter, Tab, Escape, Space', () => {
      expect(isTerminalAutoResponse('\r')).toBe(false)
      expect(isTerminalAutoResponse('\t')).toBe(false)
      expect(isTerminalAutoResponse('\x1b')).toBe(false)
      expect(isTerminalAutoResponse(' ')).toBe(false)
    })

    it('passes arrow keys', () => {
      expect(isTerminalAutoResponse('\x1b[A')).toBe(false)
      expect(isTerminalAutoResponse('\x1b[B')).toBe(false)
      expect(isTerminalAutoResponse('\x1b[C')).toBe(false)
      expect(isTerminalAutoResponse('\x1b[D')).toBe(false)
    })

    it('passes modified arrow keys (Ctrl+arrow)', () => {
      expect(isTerminalAutoResponse('\x1b[1;5A')).toBe(false)
      expect(isTerminalAutoResponse('\x1b[1;5C')).toBe(false)
    })

    it('passes application cursor mode arrow keys', () => {
      expect(isTerminalAutoResponse('\x1bOA')).toBe(false)
      expect(isTerminalAutoResponse('\x1bOB')).toBe(false)
      expect(isTerminalAutoResponse('\x1bOC')).toBe(false)
      expect(isTerminalAutoResponse('\x1bOD')).toBe(false)
    })

    it('passes function keys', () => {
      expect(isTerminalAutoResponse('\x1bOP')).toBe(false)
      expect(isTerminalAutoResponse('\x1b[15~')).toBe(false)
    })

    it('passes control characters', () => {
      expect(isTerminalAutoResponse('\x03')).toBe(false) // Ctrl+C
      expect(isTerminalAutoResponse('\x04')).toBe(false) // Ctrl+D
      expect(isTerminalAutoResponse('\x1a')).toBe(false) // Ctrl+Z
    })
  })
})
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/yuanbo/workspace/github.com/agent-tunnel/web && npx vitest run src/input_filter.test.ts`

Expected: FAIL — `input_filter` module does not exist.

- [ ] **Step 3: Write the implementation**

Create `web/src/input_filter.ts`:

```typescript
// Cursor Position Report: \x1b[<digits>;<digits>R
const CPR = /^\x1b\[\d+;\d+R$/

// Extended Cursor Position Report (DECXCPR): \x1b[?<digits>;<digits>R
const DECXCPR = /^\x1b\[\?\d+;\d+R$/

// Primary Device Attributes (DA1): \x1b[?<digits>;<digits>;...c
const DA1 = /^\x1b\[\?[\d;]+c$/

// Secondary Device Attributes (DA2): \x1b[><digits>;<digits>;...c
const DA2 = /^\x1b\[>[\d;]+c$/

// OSC color query response: \x1b]1<0|1>;rgb:...<ST>
// ST is either \x1b\\ (ESC backslash) or \x07 (BEL)
const OSC_COLOR = /^\x1b\]1[01];rgb:[0-9a-fA-F/]+(?:\x1b\\|\x07)$/

/**
 * Returns true if the string is a terminal auto-response that should NOT
 * be forwarded to the PTY. xterm.js generates these responses automatically
 * when it receives query escape sequences in the output stream.
 */
export function isTerminalAutoResponse(data: string): boolean {
  return (
    CPR.test(data) ||
    DECXCPR.test(data) ||
    DA1.test(data) ||
    DA2.test(data) ||
    OSC_COLOR.test(data)
  )
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/yuanbo/workspace/github.com/agent-tunnel/web && npx vitest run src/input_filter.test.ts`

Expected: All tests PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/input_filter.ts web/src/input_filter.test.ts
git commit -m "feat(web): add terminal auto-response filter for TUI input parity"
```

---

### Task 2: Integrate filter into main.ts

**Files:**
- Modify: `web/src/main.ts:39-41`

- [ ] **Step 1: Add the filter to the onData handler**

In `web/src/main.ts`, add the import at the top (after the existing imports):

```typescript
import { isTerminalAutoResponse } from './input_filter'
```

Replace the existing `onData` handler (line 39-41):

```typescript
terminal.onData((str) => {
  conn.send(encodeInput(str))
})
```

With:

```typescript
terminal.onData((str) => {
  if (!isTerminalAutoResponse(str)) {
    conn.send(encodeInput(str))
  }
})
```

- [ ] **Step 2: Run all web tests**

Run: `cd /Users/yuanbo/workspace/github.com/agent-tunnel/web && npx vitest run`

Expected: All tests PASS (input_filter, protocol, session_url).

- [ ] **Step 3: Build web assets**

Run: `make web-build`

Expected: Build succeeds. Embedded assets in `internal/webui/dist/` are updated.

- [ ] **Step 4: Run full project tests and build**

Run: `make test && make build`

Expected: All pass.

- [ ] **Step 5: Commit**

```bash
git add web/src/main.ts
git commit -m "feat(web): integrate auto-response filter into input pipeline"
```

---

### Task 3: Rebuild embedded assets and final commit

**Files:**
- Modify: `internal/webui/dist/*` (generated)

- [ ] **Step 1: Rebuild embedded web assets**

Run: `make web-build`

Expected: `internal/webui/dist/` files are updated.

- [ ] **Step 2: Run full verification**

Run: `make test && make build`

Expected: All pass.

- [ ] **Step 3: Commit embedded assets**

```bash
git add internal/webui/dist/
git commit -m "build: rebuild embedded web assets with input filter"
```
