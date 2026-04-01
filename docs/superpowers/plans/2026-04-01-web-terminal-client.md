# Web Terminal Client Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Go CLI client with a browser-based terminal that connects to the existing Go agent at `ws://localhost:8585/ws`.

**Architecture:** A Vite + TypeScript project in `web/` inside the existing repo. xterm.js renders the terminal in the browser and speaks the same JSON/base64 protocol the Go client already uses — zero changes to the agent.

**Tech Stack:** Vite 5, TypeScript 5, @xterm/xterm 5, @xterm/addon-fit, @xterm/addon-web-links

---

## Corrections from reviewing the original proposal

**What changed and why:**

1. **Location is `web/` not `agent-tunnel-web/`** — keep everything in one repo; a sibling directory creates unnecessary split.

2. **xterm CSS must be imported explicitly** — `@xterm/xterm` does not auto-inject its stylesheet. Without `import '@xterm/xterm/css/xterm.css'`, the terminal renders blank. This was missing from the original plan.

3. **Encoding for non-ASCII input is wrong in the original** — `btoa(str)` throws on any non-ASCII character (e.g., typing `é`, paste, or CJK). The correct path is `TextEncoder → Uint8Array → manual binary string → btoa`. Confirmed against the Go agent which uses `base64.StdEncoding` (same as `btoa`).

4. **Output decoding should produce `Uint8Array`, not string** — `atob` → loop `charCodeAt` → `Uint8Array` → pass directly to `terminal.write()`. This preserves the raw PTY byte stream. Original plan mentioned this as a "gotcha" but didn't show the implementation.

5. **Status bar needs a reconnect button** — when `ws://localhost:8585` is not running yet, the user needs a way to retry without refreshing. The original plan exposed a `reconnect()` method but didn't wire it to any UI element.

6. **`tsconfig.json` must include `"lib": ["dom", "es2020"]`** — without `dom`, TypeScript won't know `WebSocket`, `ResizeObserver`, `TextEncoder`, etc.

7. **Tests cover `protocol.ts` only** — the encoding/decoding functions are pure and testable with Vitest. DOM-bound code (`terminal.ts`, `connection.ts`) is not unit-tested at this stage.

---

## File Structure

```
web/
├── package.json            ← deps: xterm, addons, vite, typescript, vitest
├── tsconfig.json           ← target: es2020, lib: dom + es2020, strict
├── vite.config.ts          ← port 3000, no proxy needed (CheckOrigin=true)
├── index.html              ← #status-bar + #terminal + <script type=module>
└── src/
    ├── protocol.ts         ← Message type + encodeInput() + decodeOutput()
    ├── protocol.test.ts    ← Vitest tests for encoding/decoding
    ├── terminal.ts         ← xterm.Terminal setup, FitAddon, ResizeObserver
    ├── connection.ts       ← WebSocket lifecycle, status events, reconnect
    ├── main.ts             ← wires terminal ↔ connection, updates status bar
    └── style.css           ← full-viewport layout, status bar, dark base
```

**Responsibility boundaries:**
- `protocol.ts` — pure encoding/decoding, no DOM, no WebSocket
- `terminal.ts` — xterm setup only, no network
- `connection.ts` — WebSocket only, no terminal
- `main.ts` — glue only, no logic of its own

---

## Task 1: Scaffold the project

**Files:**
- Create: `web/package.json`
- Create: `web/tsconfig.json`
- Create: `web/vite.config.ts`
- Create: `web/index.html`

- [ ] **Step 1: Create `web/package.json`**

```json
{
  "name": "agent-tunnel-web",
  "private": true,
  "version": "0.1.0",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "tsc && vite build",
    "test": "vitest run"
  },
  "dependencies": {
    "@xterm/addon-fit": "^0.10.0",
    "@xterm/addon-web-links": "^0.11.0",
    "@xterm/xterm": "^5.5.0"
  },
  "devDependencies": {
    "typescript": "^5.4.0",
    "vite": "^5.4.0",
    "vitest": "^1.6.0"
  }
}
```

- [ ] **Step 2: Create `web/tsconfig.json`**

```json
{
  "compilerOptions": {
    "target": "ES2020",
    "lib": ["dom", "ES2020"],
    "module": "ESNext",
    "moduleResolution": "bundler",
    "strict": true,
    "noUnusedLocals": true,
    "noUnusedParameters": true,
    "noImplicitReturns": true,
    "skipLibCheck": true
  },
  "include": ["src"]
}
```

- [ ] **Step 3: Create `web/vite.config.ts`**

```typescript
import { defineConfig } from 'vite'

export default defineConfig({
  server: {
    port: 3000,
  },
})
```

- [ ] **Step 4: Create `web/index.html`**

```html
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>agent-tunnel</title>
</head>
<body>
  <div id="status-bar">
    <span id="status-dot"></span>
    <span id="status-text">Connecting…</span>
    <button id="reconnect-btn" hidden>Reconnect</button>
  </div>
  <div id="terminal"></div>
  <script type="module" src="/src/main.ts"></script>
</body>
</html>
```

- [ ] **Step 5: Install dependencies**

```bash
cd web && npm install
```

Expected: `node_modules/` created, no errors.

- [ ] **Step 6: Commit**

```bash
git add web/package.json web/tsconfig.json web/vite.config.ts web/index.html web/package-lock.json
git commit -m "feat(web): scaffold Vite + TypeScript project"
```

---

## Task 2: Protocol encoding (with tests)

**Files:**
- Create: `web/src/protocol.ts`
- Create: `web/src/protocol.test.ts`

- [ ] **Step 1: Write the failing tests**

Create `web/src/protocol.test.ts`:

```typescript
import { describe, it, expect } from 'vitest'
import { encodeInput, decodeOutput } from './protocol'

describe('encodeInput', () => {
  it('encodes ASCII input to a JSON input message', () => {
    const msg = encodeInput('ls\r')
    const parsed = JSON.parse(msg)
    expect(parsed.type).toBe('input')
    // base64 of UTF-8 bytes for "ls\r"
    expect(parsed.data).toBe(btoa('ls\r'))
  })

  it('encodes non-ASCII input (é) without throwing', () => {
    const msg = encodeInput('é')
    const parsed = JSON.parse(msg)
    expect(parsed.type).toBe('input')
    // "é" is 0xc3 0xa9 in UTF-8, base64 = "w6k="
    expect(parsed.data).toBe('w6k=')
  })
})

describe('decodeOutput', () => {
  it('decodes a base64 output message to Uint8Array', () => {
    const msg = JSON.stringify({ type: 'output', data: btoa('hello') })
    const bytes = decodeOutput(JSON.parse(msg))
    expect(bytes).toBeInstanceOf(Uint8Array)
    expect(Array.from(bytes)).toEqual([104, 101, 108, 108, 111])
  })
})
```

- [ ] **Step 2: Run to verify tests fail**

```bash
cd web && npm test
```

Expected: FAIL — `Cannot find module './protocol'`

- [ ] **Step 3: Implement `web/src/protocol.ts`**

```typescript
export type Message =
  | { type: 'input'; data: string }
  | { type: 'output'; data: string }
  | { type: 'resize'; cols: number; rows: number }

// encodeInput takes the raw string from xterm.js onData and returns a JSON
// string ready to send over WebSocket.
// Uses TextEncoder so non-ASCII characters (multi-byte UTF-8) are handled
// correctly. btoa(str) would throw on anything outside Latin-1.
export function encodeInput(str: string): string {
  const bytes = new TextEncoder().encode(str)
  let binary = ''
  for (let i = 0; i < bytes.length; i++) {
    binary += String.fromCharCode(bytes[i])
  }
  const msg: Message = { type: 'input', data: btoa(binary) }
  return JSON.stringify(msg)
}

// decodeOutput takes an output Message and returns a Uint8Array of raw PTY
// bytes. Pass directly to terminal.write() to preserve the byte stream.
export function decodeOutput(msg: Extract<Message, { type: 'output' }>): Uint8Array {
  const binary = atob(msg.data)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i)
  }
  return bytes
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd web && npm test
```

Expected: PASS — 3 tests pass

- [ ] **Step 5: Commit**

```bash
git add web/src/protocol.ts web/src/protocol.test.ts
git commit -m "feat(web): add protocol encoding/decoding with tests"
```

---

## Task 3: Terminal component

**Files:**
- Create: `web/src/terminal.ts`

- [ ] **Step 1: Create `web/src/terminal.ts`**

```typescript
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import '@xterm/xterm/css/xterm.css'

export interface TerminalHandle {
  write(data: Uint8Array): void
  onData(callback: (str: string) => void): void
  onResize(callback: (cols: number, rows: number) => void): void
  currentSize(): { cols: number; rows: number }
  dispose(): void
}

export function createTerminal(container: HTMLElement): TerminalHandle {
  const term = new Terminal({
    theme: {
      background: '#1a1b26',
      foreground: '#a9b1d6',
      cursor: '#c0caf5',
      selectionBackground: '#33467c',
      black: '#15161e',
      red: '#f7768e',
      green: '#9ece6a',
      yellow: '#e0af68',
      blue: '#7aa2f7',
      magenta: '#bb9af7',
      cyan: '#7dcfff',
      white: '#a9b1d6',
    },
    fontFamily: "'JetBrains Mono', 'Fira Code', 'SF Mono', monospace",
    fontSize: 14,
    cursorBlink: true,
  })

  const fitAddon = new FitAddon()
  term.loadAddon(fitAddon)
  term.loadAddon(new WebLinksAddon())
  term.open(container)
  fitAddon.fit()

  let resizeCallback: ((cols: number, rows: number) => void) | null = null

  const observer = new ResizeObserver(() => {
    fitAddon.fit()
    if (resizeCallback) {
      resizeCallback(term.cols, term.rows)
    }
  })
  observer.observe(container)

  return {
    write(data: Uint8Array) {
      term.write(data)
    },
    onData(callback: (str: string) => void) {
      term.onData(callback)
    },
    onResize(callback: (cols: number, rows: number) => void) {
      resizeCallback = callback
    },
    currentSize() {
      return { cols: term.cols, rows: term.rows }
    },
    dispose() {
      observer.disconnect()
      term.dispose()
    },
  }
}
```

- [ ] **Step 2: Verify TypeScript compiles**

```bash
cd web && npx tsc --noEmit
```

Expected: no errors (xterm types resolve)

- [ ] **Step 3: Commit**

```bash
git add web/src/terminal.ts
git commit -m "feat(web): add xterm terminal component"
```

---

## Task 4: Connection manager

**Files:**
- Create: `web/src/connection.ts`

- [ ] **Step 1: Create `web/src/connection.ts`**

```typescript
import type { Message } from './protocol'

export type ConnectionStatus = 'connecting' | 'connected' | 'disconnected'

export class ConnectionManager {
  private ws: WebSocket | null = null
  private readonly url: string
  private messageCallback: ((msg: Message) => void) | null = null
  private statusCallback: ((status: ConnectionStatus) => void) | null = null

  constructor(url: string) {
    this.url = url
    this.connect()
  }

  private connect() {
    this.emitStatus('connecting')
    const ws = new WebSocket(this.url)
    this.ws = ws

    ws.onopen = () => {
      this.emitStatus('connected')
    }

    ws.onmessage = (event: MessageEvent<string>) => {
      try {
        const msg = JSON.parse(event.data) as Message
        this.messageCallback?.(msg)
      } catch {
        // ignore malformed frames
      }
    }

    ws.onclose = () => {
      this.ws = null
      this.emitStatus('disconnected')
    }

    ws.onerror = () => {
      // onclose always fires after onerror, so status update happens there
    }
  }

  send(json: string) {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(json)
    }
  }

  reconnect() {
    this.ws?.close()
    this.connect()
  }

  onMessage(callback: (msg: Message) => void) {
    this.messageCallback = callback
  }

  onStatusChange(callback: (status: ConnectionStatus) => void) {
    this.statusCallback = callback
  }

  private emitStatus(status: ConnectionStatus) {
    this.statusCallback?.(status)
  }
}
```

- [ ] **Step 2: Verify TypeScript compiles**

```bash
cd web && npx tsc --noEmit
```

Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add web/src/connection.ts
git commit -m "feat(web): add WebSocket connection manager"
```

---

## Task 5: CSS layout

**Files:**
- Create: `web/src/style.css`

- [ ] **Step 1: Create `web/src/style.css`**

```css
* {
  box-sizing: border-box;
  margin: 0;
  padding: 0;
}

html, body {
  height: 100%;
  background: #1a1b26;
  color: #a9b1d6;
  font-family: 'JetBrains Mono', 'Fira Code', 'SF Mono', monospace;
  overflow: hidden;
}

body {
  display: flex;
  flex-direction: column;
}

#status-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 12px;
  height: 28px;
  background: #13141f;
  border-bottom: 1px solid #2a2b3d;
  font-size: 12px;
  flex-shrink: 0;
}

#status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #e0af68; /* yellow = connecting */
  flex-shrink: 0;
}

#status-dot.connected    { background: #9ece6a; }
#status-dot.disconnected { background: #f7768e; }

#reconnect-btn {
  margin-left: auto;
  background: #2a2b3d;
  color: #a9b1d6;
  border: 1px solid #3a3b4d;
  border-radius: 4px;
  padding: 2px 10px;
  font-size: 12px;
  cursor: pointer;
}

#reconnect-btn:hover {
  background: #3a3b4d;
}

#terminal {
  flex: 1;
  overflow: hidden;
  padding: 4px;
}
```

- [ ] **Step 2: Commit**

```bash
git add web/src/style.css
git commit -m "feat(web): add full-viewport dark terminal layout"
```

---

## Task 6: Wire everything in main.ts

**Files:**
- Create: `web/src/main.ts`

- [ ] **Step 1: Create `web/src/main.ts`**

```typescript
import './style.css'
import { createTerminal } from './terminal'
import { ConnectionManager, type ConnectionStatus } from './connection'
import { encodeInput, decodeOutput } from './protocol'
import type { Message } from './protocol'

const AGENT_URL = 'ws://localhost:8585/ws'

const statusDot  = document.getElementById('status-dot')!
const statusText = document.getElementById('status-text')!
const reconnectBtn = document.getElementById('reconnect-btn') as HTMLButtonElement

const termContainer = document.getElementById('terminal')!
const terminal = createTerminal(termContainer)
const conn = new ConnectionManager(AGENT_URL)

// terminal → agent
terminal.onData((str) => {
  conn.send(encodeInput(str))
})

// terminal resize → agent
terminal.onResize((cols, rows) => {
  conn.send(JSON.stringify({ type: 'resize', cols, rows }))
})

// agent → terminal
conn.onMessage((msg: Message) => {
  if (msg.type === 'output') {
    terminal.write(decodeOutput(msg))
  }
})

// status bar updates
conn.onStatusChange((status: ConnectionStatus) => {
  statusDot.className = status
  reconnectBtn.hidden = status !== 'disconnected'

  switch (status) {
    case 'connecting':
      statusText.textContent = `Connecting to ${AGENT_URL}…`
      break
    case 'connected':
      statusText.textContent = `Connected to ${AGENT_URL}`
      // send current terminal size so the agent sets the PTY correctly
      const { cols, rows } = terminal.currentSize()
      conn.send(JSON.stringify({ type: 'resize', cols, rows }))
      break
    case 'disconnected':
      statusText.textContent = `Disconnected from ${AGENT_URL}`
      break
  }
})

reconnectBtn.addEventListener('click', () => conn.reconnect())
```

- [ ] **Step 2: Verify full TypeScript compile**

```bash
cd web && npx tsc --noEmit
```

Expected: no errors

- [ ] **Step 3: Start agent and dev server, smoke-test**

Terminal 1:
```bash
cd /path/to/agent-tunnel && go run ./cmd/agent
```

Terminal 2:
```bash
cd web && npm run dev
```

Open `http://localhost:3000`. Expected:
- Status bar shows green dot and "Connected to ws://localhost:8585/ws"
- Terminal renders with dark background
- Type `ls` and press Enter — output appears
- Resize the browser window — terminal reflows and PTY adjusts

- [ ] **Step 4: Commit**

```bash
git add web/src/main.ts
git commit -m "feat(web): wire terminal, connection, and status bar"
```

---

## Task 7: Gitignore

**Files:**
- Modify: `.gitignore` (or create if missing)

- [ ] **Step 1: Add `web/node_modules` to `.gitignore`**

Add to `.gitignore`:
```
web/node_modules/
web/dist/
```

- [ ] **Step 2: Commit**

```bash
git add .gitignore
git commit -m "chore: ignore web build artifacts"
```

---

## Self-review

**Spec coverage:**
- Dark theme ✓ (Tokyo Night in terminal.ts + style.css)
- Fit-to-window ✓ (FitAddon + ResizeObserver)
- Connection status ✓ (status bar with colored dot + text)
- Reconnect button ✓ (appears on disconnect, wired to conn.reconnect())
- xterm CSS import ✓ (in terminal.ts)
- Non-ASCII encoding ✓ (TextEncoder path in protocol.ts)
- Binary-safe output ✓ (Uint8Array path in protocol.ts + terminal.write)
- Protocol matches Go agent ✓ (base64.StdEncoding = btoa)
- Zero agent changes ✓

**Placeholder scan:** None found — every step has code.

**Type consistency:**
- `encodeInput` returns `string` (JSON), used as `conn.send(encodeInput(...))` ✓
- `decodeOutput` takes `Extract<Message, {type:'output'}>`, called with `msg` after `msg.type === 'output'` check ✓
- `TerminalHandle.write` takes `Uint8Array`, `decodeOutput` returns `Uint8Array` ✓
- `ConnectionManager.send` takes `string`, `encodeInput` returns `string`, resize uses `JSON.stringify` ✓
