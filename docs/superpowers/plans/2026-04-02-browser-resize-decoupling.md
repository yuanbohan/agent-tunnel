# Browser Resize Decoupling Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Decouple PTY resize ownership so only the local terminal controls the PTY size, while the browser gets a toggle between Scroll (match PTY width) and Wrap (fit to screen) display modes.

**Architecture:** The Hub stores PTY dimensions after each resize and exposes `CurrentSize()`. The server sends PTY size to WebSocket clients on connect and subscribes to a resize callback on the Hub to forward live resize updates. The browser removes outbound resize, handles inbound resize, and adds a floating Scroll/Wrap toggle button.

**Tech Stack:** Go (Hub, server), TypeScript (xterm.js, FitAddon, CSS)

**Important note:** The Hub cannot broadcast resize notifications via `WriteOutput()` because the local terminal sink writes raw bytes to stdout — JSON resize messages would appear as garbage in the user's terminal. Instead, the Hub exposes a separate `OnResize` callback that only the server subscribes to.

---

### Task 1: Hub stores PTY dimensions and exposes CurrentSize and OnResize

**Files:**
- Modify: `internal/session/hub.go:12-64`
- Modify: `internal/session/hub_test.go`

- [ ] **Step 1: Write failing test for Hub.CurrentSize()**

Add to `internal/session/hub_test.go`:

```go
func TestHubCurrentSizeReturnsZeroBeforeFirstResize(t *testing.T) {
	hub := NewHub(func([]byte) error { return nil }, func(int, int) error { return nil })

	cols, rows := hub.CurrentSize()
	if cols != 0 || rows != 0 {
		t.Fatalf("CurrentSize = %dx%d, want 0x0", cols, rows)
	}
}
```

- [ ] **Step 2: Write failing test for Hub storing size after Resize()**

Add to `internal/session/hub_test.go`:

```go
func TestHubResizeStoresCurrentSize(t *testing.T) {
	hub := NewHub(func([]byte) error { return nil }, func(int, int) error { return nil })

	if err := hub.Resize(120, 40); err != nil {
		t.Fatalf("Resize returned error: %v", err)
	}

	cols, rows := hub.CurrentSize()
	if cols != 120 || rows != 40 {
		t.Fatalf("CurrentSize = %dx%d, want 120x40", cols, rows)
	}
}
```

- [ ] **Step 3: Write failing test for Hub.OnResize callback**

Add to `internal/session/hub_test.go`:

```go
func TestHubResizeCallsOnResizeCallback(t *testing.T) {
	hub := NewHub(func([]byte) error { return nil }, func(int, int) error { return nil })

	var gotCols, gotRows int
	hub.OnResize(func(cols, rows int) {
		gotCols = cols
		gotRows = rows
	})

	if err := hub.Resize(100, 50); err != nil {
		t.Fatalf("Resize returned error: %v", err)
	}

	if gotCols != 100 || gotRows != 50 {
		t.Fatalf("OnResize callback got %dx%d, want 100x50", gotCols, gotRows)
	}
}
```

- [ ] **Step 4: Run all three new tests to verify they fail**

Run: `cd /Users/yuanbo/workspace/github.com/agent-tunnel && go test ./internal/session/ -run "TestHubCurrentSize|TestHubResizeStores|TestHubResizeCallsOnResize" -v`
Expected: FAIL — `CurrentSize` and `OnResize` methods do not exist.

- [ ] **Step 5: Implement Hub changes**

Replace the entire content of `internal/session/hub.go`:

```go
package session

import (
	"fmt"
	"sync"
)

type OutputSink interface {
	WriteOutput([]byte) error
}

type Hub struct {
	writeInput func([]byte) error
	resizePTY  func(int, int) error

	mu       sync.RWMutex
	sinks    map[string]OutputSink
	cols     int
	rows     int
	onResize func(int, int)
}

func NewHub(writeInput func([]byte) error, resizePTY func(int, int) error) *Hub {
	return &Hub{
		writeInput: writeInput,
		resizePTY:  resizePTY,
		sinks:      make(map[string]OutputSink),
	}
}

func (h *Hub) AddSink(id string, sink OutputSink) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sinks[id] = sink
}

func (h *Hub) RemoveSink(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.sinks, id)
}

func (h *Hub) BroadcastOutput(data []byte) {
	h.mu.RLock()
	sinks := make([]OutputSink, 0, len(h.sinks))
	for _, sink := range h.sinks {
		sinks = append(sinks, sink)
	}
	h.mu.RUnlock()

	for _, sink := range sinks {
		cp := append([]byte(nil), data...)
		_ = sink.WriteOutput(cp)
	}
}

func (h *Hub) WriteInput(data []byte) error {
	cp := append([]byte(nil), data...)
	return h.writeInput(cp)
}

func (h *Hub) Resize(cols, rows int) error {
	if cols <= 0 || rows <= 0 {
		return fmt.Errorf("invalid resize %dx%d", cols, rows)
	}
	if err := h.resizePTY(cols, rows); err != nil {
		return err
	}

	h.mu.Lock()
	h.cols = cols
	h.rows = rows
	cb := h.onResize
	h.mu.Unlock()

	if cb != nil {
		cb(cols, rows)
	}
	return nil
}

func (h *Hub) CurrentSize() (int, int) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.cols, h.rows
}

func (h *Hub) OnResize(cb func(cols, rows int)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onResize = cb
}
```

- [ ] **Step 6: Run all Hub tests to verify they pass**

Run: `cd /Users/yuanbo/workspace/github.com/agent-tunnel && go test ./internal/session/ -v`
Expected: All PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/session/hub.go internal/session/hub_test.go
git commit -m "feat(hub): store PTY dimensions, add CurrentSize and OnResize"
```

---

### Task 2: Server stops accepting browser resize, sends PTY size on connect, forwards live resize

**Files:**
- Modify: `internal/server/server.go:22-27,154-196`
- Modify: `internal/server/server_test.go`

- [ ] **Step 1: Update fakeSession for new LiveSession interface**

In `internal/server/server_test.go`, update `fakeSession`:

Remove the `Resize` method, `ResizeSize` method, and `resizeCh` field. Add `CurrentSize` and `OnResize` methods:

```go
type fakeSession struct {
	mu sync.Mutex

	input      []byte
	cols       int
	rows       int
	sinks      map[string]session.OutputSink
	inputCh    chan struct{}
	onResizeCb func(int, int)
}

func (f *fakeSession) AddSink(id string, sink session.OutputSink) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.sinks == nil {
		f.sinks = make(map[string]session.OutputSink)
	}
	f.sinks[id] = sink
}

func (f *fakeSession) RemoveSink(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	delete(f.sinks, id)
}

func (f *fakeSession) WriteInput(data []byte) error {
	f.mu.Lock()
	f.input = append([]byte(nil), data...)
	f.mu.Unlock()

	if f.inputCh != nil {
		select {
		case f.inputCh <- struct{}{}:
		default:
		}
	}
	return nil
}

func (f *fakeSession) CurrentSize() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cols, f.rows
}

func (f *fakeSession) OnResize(cb func(int, int)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.onResizeCb = cb
}

func (f *fakeSession) Input() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.input...)
}

func (f *fakeSession) Sinks() map[string]session.OutputSink {
	f.mu.Lock()
	defer f.mu.Unlock()

	sinks := make(map[string]session.OutputSink, len(f.sinks))
	for id, sink := range f.sinks {
		sinks[id] = sink
	}
	return sinks
}
```

- [ ] **Step 2: Write test for server sending PTY size on connect**

Add to `internal/server/server_test.go`:

```go
func TestWebSocketSendsPTYSizeOnConnect(t *testing.T) {
	sess := &fakeSession{cols: 120, rows: 40}

	server := httptest.NewServer(NewHandler(sess))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()

	var msg protocol.Message
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("ReadJSON returned error: %v", err)
	}

	if msg.Type != "resize" || msg.Cols != 120 || msg.Rows != 40 {
		t.Fatalf("initial message = %+v, want resize 120x40", msg)
	}
}
```

- [ ] **Step 3: Write test verifying server ignores browser resize messages**

Add to `internal/server/server_test.go`:

```go
func TestWebSocketIgnoresBrowserResize(t *testing.T) {
	sess := &fakeSession{cols: 80, rows: 24, inputCh: make(chan struct{}, 1)}

	server := httptest.NewServer(NewHandler(sess))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()

	// Read and discard the initial PTY size message
	var initial protocol.Message
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if err := conn.ReadJSON(&initial); err != nil {
		t.Fatalf("ReadJSON returned error: %v", err)
	}

	// Send a resize from browser — should be ignored
	if err := conn.WriteJSON(protocol.Message{
		Type: "resize",
		Cols: 200,
		Rows: 60,
	}); err != nil {
		t.Fatalf("WriteJSON returned error: %v", err)
	}

	// Send an input message to verify the connection is still working
	if err := conn.WriteJSON(protocol.Message{
		Type: "input",
		Data: base64.StdEncoding.EncodeToString([]byte("test")),
	}); err != nil {
		t.Fatalf("WriteJSON returned error: %v", err)
	}

	select {
	case <-sess.inputCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for input after ignored resize")
	}

	// Verify the session size was NOT changed
	cols, rows := sess.CurrentSize()
	if cols != 80 || rows != 24 {
		t.Fatalf("session resized to %dx%d, want 80x24 (unchanged)", cols, rows)
	}
}
```

- [ ] **Step 4: Run new tests to verify they fail**

Run: `cd /Users/yuanbo/workspace/github.com/agent-tunnel && go test ./internal/server/ -run "TestWebSocketSendsPTYSize|TestWebSocketIgnoresBrowserResize" -v`
Expected: FAIL — `CurrentSize()` and `OnResize()` not in `LiveSession` interface.

- [ ] **Step 5: Implement server changes**

Update `internal/server/server.go`. Change the `LiveSession` interface and update `NewHandler`:

Update the interface (replace lines 22-27):

```go
type LiveSession interface {
	AddSink(string, session.OutputSink)
	RemoveSink(string)
	WriteInput([]byte) error
	CurrentSize() (int, int)
	OnResize(func(int, int))
}
```

Update `NewHandler` (replace lines 154-196):

```go
func NewHandler(sess LiveSession) http.Handler {
	fileServer := http.FileServer(http.FS(webui.Files()))

	var wsMu sync.Mutex
	wsConns := make(map[string]*websocket.Conn)

	sess.OnResize(func(cols, rows int) {
		msg := protocol.Message{Type: "resize", Cols: cols, Rows: rows}
		wsMu.Lock()
		conns := make([]*websocket.Conn, 0, len(wsConns))
		for _, c := range wsConns {
			conns = append(conns, c)
		}
		wsMu.Unlock()

		for _, c := range conns {
			_ = c.WriteJSON(msg)
		}
	})

	mux := http.NewServeMux()
	mux.Handle("/", fileServer)
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		sinkID := fmt.Sprintf("ws-%d", atomic.AddUint64(&nextSinkID, 1))
		sink := newWSSink(conn)
		sess.AddSink(sinkID, sink)

		wsMu.Lock()
		wsConns[sinkID] = conn
		wsMu.Unlock()

		defer func() {
			sess.RemoveSink(sinkID)
			wsMu.Lock()
			delete(wsConns, sinkID)
			wsMu.Unlock()
			_ = sink.Close()
		}()

		// Send current PTY size to browser on connect
		cols, rows := sess.CurrentSize()
		if cols > 0 && rows > 0 {
			_ = conn.WriteJSON(protocol.Message{
				Type: "resize",
				Cols: cols,
				Rows: rows,
			})
		}

		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}

			var msg protocol.Message
			if err := json.Unmarshal(raw, &msg); err != nil {
				continue
			}

			switch msg.Type {
			case "input":
				data, err := base64.StdEncoding.DecodeString(msg.Data)
				if err != nil {
					continue
				}
				_ = sess.WriteInput(data)
			}
		}
	})
	return mux
}
```

Add `"sync"` to the imports if not already present (it is already imported).

- [ ] **Step 6: Delete TestWebSocketBridgeForwardsResize**

Remove the `TestWebSocketBridgeForwardsResize` test from `internal/server/server_test.go` (lines 339-369). This test verified the old behavior of browser resize being forwarded to the session, which is no longer supported.

- [ ] **Step 7: Run all server tests to verify they pass**

Run: `cd /Users/yuanbo/workspace/github.com/agent-tunnel && go test ./internal/server/ -v`
Expected: All PASS.

- [ ] **Step 8: Run full test suite**

Run: `cd /Users/yuanbo/workspace/github.com/agent-tunnel && make test`
Expected: All PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/server/server.go internal/server/server_test.go
git commit -m "feat(server): send PTY size on connect, stop accepting browser resize"
```

---

### Task 3: Browser removes outbound resize, handles inbound PTY size

**Files:**
- Modify: `web/src/terminal.ts:1-69`
- Modify: `web/src/main.ts:1-53`

- [ ] **Step 1: Update terminal.ts — remove resize callback, add display mode control**

Replace the entire content of `web/src/terminal.ts`:

```typescript
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import '@xterm/xterm/css/xterm.css'

export type DisplayMode = 'scroll' | 'wrap'

export interface TerminalHandle {
  write(data: Uint8Array): void
  onData(callback: (str: string) => void): void
  setDisplayMode(mode: DisplayMode, ptyCols?: number, ptyRows?: number): void
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

  let currentMode: DisplayMode = 'scroll'

  const observer = new ResizeObserver(() => {
    if (currentMode === 'wrap') {
      fitAddon.fit()
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
    setDisplayMode(mode: DisplayMode, ptyCols?: number, ptyRows?: number) {
      currentMode = mode
      if (mode === 'scroll' && ptyCols && ptyRows) {
        term.resize(ptyCols, ptyRows)
      } else if (mode === 'wrap') {
        fitAddon.fit()
      }
    },
    dispose() {
      observer.disconnect()
      term.dispose()
    },
  }
}
```

- [ ] **Step 2: Update main.ts — handle inbound resize, add toggle button**

Replace the entire content of `web/src/main.ts`:

```typescript
import './style.css'
import { createTerminal, type DisplayMode } from './terminal'
import { ConnectionManager, type ConnectionStatus } from './connection'
import { encodeInput, decodeOutput } from './protocol'
import type { Message } from './protocol'
import { sessionWebSocketURL } from './session_url'

const AGENT_URL = sessionWebSocketURL(window.location)

const statusDot = document.getElementById('status-dot')!
const statusText = document.getElementById('status-text')!
const reconnectBtn = document.getElementById('reconnect-btn') as HTMLButtonElement

const termContainer = document.getElementById('terminal')!
const terminal = createTerminal(termContainer)
const conn = new ConnectionManager(AGENT_URL)

let ptyCols = 0
let ptyRows = 0
let displayMode: DisplayMode = 'scroll'

// Floating toggle button
const toggleBtn = document.createElement('button')
toggleBtn.id = 'wrap-toggle'
toggleBtn.textContent = 'Wrap'
toggleBtn.title = 'Toggle line wrapping'
document.body.appendChild(toggleBtn)

toggleBtn.addEventListener('click', () => {
  displayMode = displayMode === 'scroll' ? 'wrap' : 'scroll'
  toggleBtn.textContent = displayMode === 'scroll' ? 'Wrap' : 'Scroll'
  terminal.setDisplayMode(displayMode, ptyCols, ptyRows)
  termContainer.classList.toggle('scroll-mode', displayMode === 'scroll')
})

// Start in scroll mode
termContainer.classList.add('scroll-mode')

terminal.onData((str) => {
  conn.send(encodeInput(str))
})

conn.onMessage((msg: Message) => {
  if (msg.type === 'output') {
    terminal.write(decodeOutput(msg))
  } else if (msg.type === 'resize') {
    ptyCols = msg.cols
    ptyRows = msg.rows
    if (displayMode === 'scroll') {
      terminal.setDisplayMode('scroll', ptyCols, ptyRows)
    }
  }
})

conn.onStatusChange((status: ConnectionStatus) => {
  statusDot.className = status
  reconnectBtn.hidden = status !== 'disconnected'

  switch (status) {
    case 'connecting':
      statusText.textContent = `Connecting to ${AGENT_URL}…`
      break
    case 'connected':
      statusText.textContent = `Connected to ${AGENT_URL}`
      break
    case 'disconnected':
      statusText.textContent = `Disconnected from ${AGENT_URL}`
      break
  }
})

reconnectBtn.addEventListener('click', () => conn.reconnect())
```

- [ ] **Step 3: Run TypeScript type check**

Run: `cd /Users/yuanbo/workspace/github.com/agent-tunnel/web && npx tsc --noEmit`
Expected: No errors.

- [ ] **Step 4: Commit**

```bash
git add web/src/terminal.ts web/src/main.ts
git commit -m "feat(web): remove outbound resize, handle inbound PTY size"
```

---

### Task 4: Add floating button styles and scroll-mode CSS

**Files:**
- Modify: `web/src/style.css`

- [ ] **Step 1: Add styles for the floating button and scroll mode**

Append to the end of `web/src/style.css`:

```css
#wrap-toggle {
  position: fixed;
  bottom: 16px;
  right: 16px;
  width: 44px;
  height: 44px;
  border-radius: 50%;
  background: rgba(42, 43, 61, 0.8);
  color: #a9b1d6;
  border: 1px solid #3a3b4d;
  font-size: 11px;
  cursor: pointer;
  z-index: 100;
  display: flex;
  align-items: center;
  justify-content: center;
  backdrop-filter: blur(4px);
}

#wrap-toggle:hover {
  background: rgba(58, 59, 77, 0.9);
}

#terminal.scroll-mode {
  overflow-x: auto;
  overflow-y: hidden;
}
```

- [ ] **Step 2: Commit**

```bash
git add web/src/style.css
git commit -m "feat(web): add floating toggle button and scroll-mode styles"
```

---

### Task 5: Rebuild embedded web assets and run full verification

**Files:**
- Modify: `internal/webui/dist/` (rebuilt by `make web-build`)

- [ ] **Step 1: Rebuild web assets**

Run: `cd /Users/yuanbo/workspace/github.com/agent-tunnel && make web-build`
Expected: Build completes successfully, files in `internal/webui/dist/` are updated.

- [ ] **Step 2: Run full test suite**

Run: `cd /Users/yuanbo/workspace/github.com/agent-tunnel && make test`
Expected: All PASS.

- [ ] **Step 3: Run full build**

Run: `cd /Users/yuanbo/workspace/github.com/agent-tunnel && make build`
Expected: Build succeeds.

- [ ] **Step 4: Commit rebuilt assets**

```bash
git add internal/webui/dist/
git commit -m "build: rebuild embedded web assets with resize decoupling"
```
