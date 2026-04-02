# Agentunnel Shared Agent Session Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a new `agentunnel` CLI that launches `claude`, `codex`, or `gemini` in a PTY, keeps the local terminal interactive, and mirrors the same live session into a localhost web terminal.

**Architecture:** Build a session hub around a PTY-backed child process, then attach two transports to it: the local terminal and browser WebSocket clients. Serve the browser bundle from the Go process itself on an ephemeral localhost port, using a tracked built bundle so Go embedding stays reproducible in this repo.

**Tech Stack:** Go 1.25, `github.com/creack/pty`, `github.com/gorilla/websocket`, `golang.org/x/term`, Vite 5, TypeScript 5, xterm.js 5, Vitest 2

---

## Corrections From Reviewing The Spec Against The Repo

1. **Do not embed `web/dist/` directly** because [`.gitignore`](/Users/yuanbo/workspace/github.com/agent-tunnel/.gitignore) already ignores that path, which would make `go build` and `go test ./...` depend on an untracked local build. Instead, Vite should emit a tracked bundle into `internal/webui/dist/`, and Go should embed that directory.

2. **Keep the existing `cmd/agent` and `cmd/client` during the transition**. The new work should add `cmd/agentunnel` as the primary shared-session entrypoint without breaking the existing single-websocket tools.

3. **Test the fanout logic separately from PTY startup**. The current repo has no session abstraction, so the hub should be unit-tested as a pure broadcast/write component before wiring it to a PTY.

---

## File Structure

```
cmd/
├── agent/
├── client/
└── agentunnel/
    └── main.go                 ← new CLI entrypoint for shared live sessions

internal/
├── launcher/
│   ├── registry.go            ← supported launcher names and PATH resolution
│   └── registry_test.go       ← unit tests for launcher validation and lookup
├── session/
│   ├── hub.go                 ← output fanout, input forwarding, resize forwarding
│   ├── hub_test.go            ← pure unit tests for hub behavior
│   ├── process.go             ← PTY-backed child-process runtime
│   └── local_terminal.go      ← stdin/stdout adapter using raw mode
├── server/
│   ├── server.go              ← localhost HTTP server and /ws bridge
│   └── server_test.go         ← HTTP + WebSocket tests with a fake session
└── webui/
    ├── embed.go               ← go:embed wrapper for built frontend assets
    └── dist/                  ← tracked generated frontend bundle

web/
├── vite.config.ts             ← build to internal/webui/dist
└── src/
    ├── session_url.ts         ← derive `ws://.../ws` from window.location
    ├── session_url.test.ts    ← unit tests for session-relative URL building
    └── main.ts                ← use relative session URL instead of hardcoded host
```

**Responsibility boundaries:**

- `internal/launcher` only knows launcher names and executable lookup.
- `internal/session/hub.go` is pure transport logic; it does not know about WebSockets or terminals.
- `internal/session/process.go` owns the PTY and child process lifecycle.
- `internal/session/local_terminal.go` adapts stdin/stdout to the hub.
- `internal/server` serves static assets and translates WebSocket frames to hub calls.
- `internal/webui` only exposes embedded static files.

---

## Task 1: Add the Launcher Registry

**Files:**
- Create: `internal/launcher/registry.go`
- Create: `internal/launcher/registry_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/launcher/registry_test.go`:

```go
package launcher

import (
	"errors"
	"strings"
	"testing"
)

func TestResolveSupportedLauncher(t *testing.T) {
	cmd, err := resolveWithLookPath("claude", []string{"--resume"}, func(file string) (string, error) {
		if file != "claude" {
			t.Fatalf("lookPath called with %q, want claude", file)
		}
		return "/usr/local/bin/claude", nil
	})
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if cmd.Name != "claude" {
		t.Fatalf("Name = %q, want claude", cmd.Name)
	}
	if cmd.Path != "/usr/local/bin/claude" {
		t.Fatalf("Path = %q, want /usr/local/bin/claude", cmd.Path)
	}
	if len(cmd.Args) != 1 || cmd.Args[0] != "--resume" {
		t.Fatalf("Args = %#v, want [--resume]", cmd.Args)
	}
}

func TestResolveRejectsUnsupportedLauncher(t *testing.T) {
	_, err := resolveWithLookPath("python", nil, func(string) (string, error) {
		t.Fatal("lookPath should not be called for unsupported launchers")
		return "", nil
	})
	if err == nil {
		t.Fatal("expected an error for unsupported launcher")
	}
	if !strings.Contains(err.Error(), "supported launchers: claude, codex, gemini") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveReportsMissingExecutable(t *testing.T) {
	_, err := resolveWithLookPath("gemini", nil, func(string) (string, error) {
		return "", errors.New("not found")
	})
	if err == nil {
		t.Fatal("expected an error for missing executable")
	}
	if !strings.Contains(err.Error(), "gemini executable not found in PATH") {
		t.Fatalf("unexpected error: %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run:

```bash
go test ./internal/launcher
```

Expected: FAIL with `cannot find package` or `undefined: resolveWithLookPath`

- [ ] **Step 3: Write the minimal implementation**

Create `internal/launcher/registry.go`:

```go
package launcher

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

type Command struct {
	Name string
	Path string
	Args []string
}

var supported = map[string]string{
	"claude": "claude",
	"codex":  "codex",
	"gemini": "gemini",
}

func Resolve(name string, args []string) (Command, error) {
	return resolveWithLookPath(name, args, exec.LookPath)
}

func resolveWithLookPath(name string, args []string, lookPath func(string) (string, error)) (Command, error) {
	executable, ok := supported[name]
	if !ok {
		names := make([]string, 0, len(supported))
		for launcherName := range supported {
			names = append(names, launcherName)
		}
		sort.Strings(names)
		return Command{}, fmt.Errorf("unsupported launcher %q (supported launchers: %s)", name, strings.Join(names, ", "))
	}

	path, err := lookPath(executable)
	if err != nil {
		return Command{}, fmt.Errorf("%s executable not found in PATH", executable)
	}

	return Command{
		Name: name,
		Path: path,
		Args: append([]string(nil), args...),
	}, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run:

```bash
go test ./internal/launcher
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/launcher/registry.go internal/launcher/registry_test.go
git commit -m "feat(agentunnel): add launcher registry"
```

---

## Task 2: Build the Session Hub

**Files:**
- Create: `internal/session/hub.go`
- Create: `internal/session/hub_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/session/hub_test.go`:

```go
package session

import (
	"bytes"
	"testing"
)

type recordingSink struct {
	chunks [][]byte
}

func (s *recordingSink) WriteOutput(data []byte) error {
	cp := append([]byte(nil), data...)
	s.chunks = append(s.chunks, cp)
	return nil
}

func TestHubBroadcastsOutputToAllSinks(t *testing.T) {
	hub := NewHub(func([]byte) error { return nil }, func(int, int) error { return nil })

	left := &recordingSink{}
	right := &recordingSink{}
	hub.AddSink("left", left)
	hub.AddSink("right", right)

	hub.BroadcastOutput([]byte("hello"))

	if got := bytes.Join(left.chunks, nil); string(got) != "hello" {
		t.Fatalf("left sink got %q, want hello", string(got))
	}
	if got := bytes.Join(right.chunks, nil); string(got) != "hello" {
		t.Fatalf("right sink got %q, want hello", string(got))
	}
}

func TestHubWriteInputPassesBytesToWriter(t *testing.T) {
	var got []byte
	hub := NewHub(func(data []byte) error {
		got = append([]byte(nil), data...)
		return nil
	}, func(int, int) error { return nil })

	if err := hub.WriteInput([]byte("input")); err != nil {
		t.Fatalf("WriteInput returned error: %v", err)
	}
	if string(got) != "input" {
		t.Fatalf("got input %q, want input", string(got))
	}
}

func TestHubResizeRejectsInvalidDimensions(t *testing.T) {
	hub := NewHub(func([]byte) error { return nil }, func(int, int) error { return nil })

	if err := hub.Resize(0, 24); err == nil {
		t.Fatal("expected an error for zero columns")
	}
	if err := hub.Resize(80, 0); err == nil {
		t.Fatal("expected an error for zero rows")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run:

```bash
go test ./internal/session
```

Expected: FAIL with `undefined: NewHub`

- [ ] **Step 3: Write the minimal implementation**

Create `internal/session/hub.go`:

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

	mu    sync.RWMutex
	sinks map[string]OutputSink
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
	defer h.mu.RUnlock()
	for _, sink := range h.sinks {
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
	return h.resizePTY(cols, rows)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run:

```bash
go test ./internal/session
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/session/hub.go internal/session/hub_test.go
git commit -m "feat(agentunnel): add shared session hub"
```

---

## Task 3: Add the PTY Runtime and Local Terminal Adapter

**Files:**
- Create: `internal/session/process.go`
- Create: `internal/session/local_terminal.go`
- Modify: `internal/session/hub_test.go`

- [ ] **Step 1: Extend the tests with one PTY-backed smoke test**

Append to `internal/session/hub_test.go`:

```go
func TestStartCommandBridgesInputAndOutput(t *testing.T) {
	running, err := StartCommand(context.Background(), "/bin/sh", []string{
		"-c",
		"read line; printf %s \"$line\"",
	})
	if err != nil {
		t.Fatalf("StartCommand returned error: %v", err)
	}
	defer running.Close()

	sink := &recordingSink{}
	running.Hub.AddSink("test", sink)

	if err := running.Hub.WriteInput([]byte("hello\n")); err != nil {
		t.Fatalf("WriteInput returned error: %v", err)
	}
	if err := running.Wait(); err != nil {
		t.Fatalf("Wait returned error: %v", err)
	}

	if got := string(bytes.Join(sink.chunks, nil)); !strings.Contains(got, "hello") {
		t.Fatalf("output %q does not contain hello", got)
	}
}
```

Also update the imports in `internal/session/hub_test.go` to:

```go
import (
	"bytes"
	"context"
	"strings"
	"testing"
)
```

- [ ] **Step 2: Run the test to verify it fails**

Run:

```bash
go test ./internal/session -run TestStartCommandBridgesInputAndOutput -v
```

Expected: FAIL with `undefined: StartCommand`

- [ ] **Step 3: Implement the PTY-backed runtime and local terminal proxy**

Create `internal/session/process.go`:

```go
package session

import (
	"context"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
)

type Running struct {
	Hub *Hub

	ptmx      *os.File
	cmd       *exec.Cmd
	closeOnce sync.Once
}

func StartCommand(ctx context.Context, path string, args []string) (*Running, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}

	running := &Running{
		ptmx: ptmx,
		cmd:  cmd,
	}
	running.Hub = NewHub(
		func(data []byte) error {
			_, err := ptmx.Write(data)
			return err
		},
		func(cols, rows int) error {
			return pty.Setsize(ptmx, &pty.Winsize{
				Cols: uint16(cols),
				Rows: uint16(rows),
			})
		},
	)

	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				running.Hub.BroadcastOutput(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	return running, nil
}

func (r *Running) Wait() error {
	return r.cmd.Wait()
}

func (r *Running) Close() error {
	var err error
	r.closeOnce.Do(func() {
		_ = r.ptmx.Close()
		if r.cmd.Process != nil {
			_ = r.cmd.Process.Kill()
		}
	})
	return err
}
```

Create `internal/session/local_terminal.go`:

```go
package session

import (
	"context"
	"os"

	clientterm "yuanbohan/tunnel/internal/client"
)

type outputSinkFunc func([]byte) error

func (f outputSinkFunc) WriteOutput(data []byte) error {
	return f(data)
}

func AttachLocalTerminal(ctx context.Context, hub *Hub) (restore func(), done <-chan struct{}, err error) {
	restore, err = clientterm.EnterRawMode()
	if err != nil {
		return nil, nil, err
	}

	hub.AddSink("local-terminal", outputSinkFunc(func(data []byte) error {
		_, writeErr := os.Stdout.Write(data)
		return writeErr
	}))

	finished := make(chan struct{})
	go func() {
		defer close(finished)
		defer hub.RemoveSink("local-terminal")

		type stdinMessage struct {
			data []byte
			err  error
		}

		stdinCh := make(chan stdinMessage, 1)
		go func() {
			buf := make([]byte, 256)
			for {
				n, readErr := os.Stdin.Read(buf)
				if n > 0 {
					cp := append([]byte(nil), buf[:n]...)
					stdinCh <- stdinMessage{data: cp}
				}
				if readErr != nil {
					stdinCh <- stdinMessage{err: readErr}
					return
				}
			}
		}()

		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-stdinCh:
				if msg.err != nil {
					return
				}
				_ = hub.WriteInput(msg.data)
			}
		}
	}()

	return restore, finished, nil
}
```

- [ ] **Step 4: Run the session package tests**

Run:

```bash
go test ./internal/session -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/session/process.go internal/session/local_terminal.go internal/session/hub_test.go
git commit -m "feat(agentunnel): add PTY process session"
```

---

## Task 4: Make the Browser Client Session-Relative and Build an Embeddable Bundle

**Files:**
- Create: `web/src/session_url.ts`
- Create: `web/src/session_url.test.ts`
- Modify: `web/src/main.ts`
- Modify: `web/vite.config.ts`
- Create: `internal/webui/dist/` via `npm run build`

- [ ] **Step 1: Write the failing browser URL tests**

Create `web/src/session_url.test.ts`:

```typescript
import { describe, expect, it } from 'vitest'
import { sessionWebSocketURL } from './session_url'

describe('sessionWebSocketURL', () => {
  it('uses ws for http pages', () => {
    expect(sessionWebSocketURL({ protocol: 'http:', host: '127.0.0.1:43127' })).toBe(
      'ws://127.0.0.1:43127/ws',
    )
  })

  it('uses wss for https pages', () => {
    expect(sessionWebSocketURL({ protocol: 'https:', host: 'example.com' })).toBe(
      'wss://example.com/ws',
    )
  })
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run:

```bash
cd web && npm test -- session_url.test.ts
```

Expected: FAIL with `Cannot find module './session_url'`

- [ ] **Step 3: Implement the session-relative URL helper and switch the frontend to it**

Create `web/src/session_url.ts`:

```typescript
export interface BrowserLocationLike {
  protocol: string
  host: string
}

export function sessionWebSocketURL(location: BrowserLocationLike): string {
  const scheme = location.protocol === 'https:' ? 'wss' : 'ws'
  return `${scheme}://${location.host}/ws`
}
```

Modify `web/src/main.ts` to:

```typescript
import './style.css'
import { createTerminal } from './terminal'
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

terminal.onData((str) => {
  conn.send(encodeInput(str))
})

terminal.onResize((cols, rows) => {
  conn.send(JSON.stringify({ type: 'resize', cols, rows }))
})

conn.onMessage((msg: Message) => {
  if (msg.type === 'output') {
    terminal.write(decodeOutput(msg))
  }
})

conn.onStatusChange((status: ConnectionStatus) => {
  statusDot.className = status
  reconnectBtn.hidden = status !== 'disconnected'

  switch (status) {
    case 'connecting':
      statusText.textContent = `Connecting to ${AGENT_URL}...`
      break
    case 'connected': {
      statusText.textContent = `Connected to ${AGENT_URL}`
      const { cols, rows } = terminal.currentSize()
      conn.send(JSON.stringify({ type: 'resize', cols, rows }))
      break
    }
    case 'disconnected':
      statusText.textContent = `Disconnected from ${AGENT_URL}`
      break
  }
})

reconnectBtn.addEventListener('click', () => conn.reconnect())
```

Modify `web/vite.config.ts` to:

```typescript
import { defineConfig } from 'vite'
import { fileURLToPath } from 'node:url'

const outDir = fileURLToPath(new URL('../internal/webui/dist', import.meta.url))

export default defineConfig({
  server: {
    port: 3000,
  },
  build: {
    outDir,
    emptyOutDir: true,
  },
})
```

- [ ] **Step 4: Run the web tests and build the tracked bundle**

Run:

```bash
cd web && npm test -- session_url.test.ts && npm run build
```

Expected:

- `session_url.test.ts` PASS
- Vite emits built files into `internal/webui/dist/`

- [ ] **Step 5: Commit**

```bash
git add web/src/session_url.ts web/src/session_url.test.ts web/src/main.ts web/vite.config.ts internal/webui/dist
git commit -m "feat(agentunnel): make web client session-relative"
```

---

## Task 5: Serve the Embedded Web App and Bridge WebSockets to the Session Hub

**Files:**
- Create: `internal/webui/embed.go`
- Create: `internal/server/server.go`
- Create: `internal/server/server_test.go`

- [ ] **Step 1: Write the failing server tests**

Create `internal/server/server_test.go`:

```go
package server

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/internal/protocol"
	"yuanbohan/tunnel/internal/session"
)

type fakeSession struct {
	input  []byte
	cols   int
	rows   int
	sinks  map[string]session.OutputSink
}

func (f *fakeSession) AddSink(id string, sink session.OutputSink) {
	if f.sinks == nil {
		f.sinks = make(map[string]session.OutputSink)
	}
	f.sinks[id] = sink
}

func (f *fakeSession) RemoveSink(id string) {
	delete(f.sinks, id)
}

func (f *fakeSession) WriteInput(data []byte) error {
	f.input = append([]byte(nil), data...)
	return nil
}

func (f *fakeSession) Resize(cols, rows int) error {
	f.cols = cols
	f.rows = rows
	return nil
}

func TestNewHandlerServesIndex(t *testing.T) {
	handler := NewHandler(&fakeSession{})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("content-type = %q, want text/html", rec.Header().Get("Content-Type"))
	}
}

func TestWebSocketBridgeForwardsInputAndOutput(t *testing.T) {
	sess := &fakeSession{}
	server := httptest.NewServer(NewHandler(sess))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(protocol.Message{
		Type: "input",
		Data: base64.StdEncoding.EncodeToString([]byte("hello")),
	}); err != nil {
		t.Fatalf("WriteJSON returned error: %v", err)
	}
	if string(sess.input) != "hello" {
		t.Fatalf("input = %q, want hello", string(sess.input))
	}

	for _, sink := range sess.sinks {
		if err := sink.WriteOutput([]byte("world")); err != nil {
			t.Fatalf("WriteOutput returned error: %v", err)
		}
	}

	var msg protocol.Message
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("ReadJSON returned error: %v", err)
	}
	out, err := protocol.DecodeData(msg)
	if err != nil {
		t.Fatalf("DecodeData returned error: %v", err)
	}
	if string(out) != "world" {
		t.Fatalf("output = %q, want world", string(out))
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run:

```bash
go test ./internal/server -v
```

Expected: FAIL with `undefined: NewHandler`

- [ ] **Step 3: Implement the embedded asset package and HTTP/WebSocket bridge**

Create `internal/webui/embed.go`:

```go
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var assets embed.FS

func Files() fs.FS {
	sub, err := fs.Sub(assets, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
```

Create `internal/server/server.go`:

```go
package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
	"yuanbohan/tunnel/internal/protocol"
	"yuanbohan/tunnel/internal/session"
	"yuanbohan/tunnel/internal/webui"
)

type LiveSession interface {
	AddSink(string, session.OutputSink)
	RemoveSink(string)
	WriteInput([]byte) error
	Resize(int, int) error
}

type Running struct {
	URL      string
	server   *http.Server
	listener net.Listener
}

type wsSink struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (s *wsSink) WriteOutput(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.WriteJSON(protocol.EncodeOutput(data))
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(*http.Request) bool { return true },
}

var nextSinkID uint64

func NewHandler(sess LiveSession) http.Handler {
	fileServer := http.FileServer(http.FS(webui.Files()))
	mux := http.NewServeMux()
	mux.Handle("/", fileServer)
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		sinkID := fmt.Sprintf("ws-%d", atomic.AddUint64(&nextSinkID, 1))
		sess.AddSink(sinkID, &wsSink{conn: conn})
		defer sess.RemoveSink(sinkID)

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
			case "resize":
				_ = sess.Resize(msg.Cols, msg.Rows)
			}
		}
	})
	return mux
}

func StartLocal(sess LiveSession) (*Running, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}

	srv := &http.Server{
		Handler: NewHandler(sess),
	}
	go func() {
		_ = srv.Serve(listener)
	}()

	return &Running{
		URL:      "http://" + listener.Addr().String(),
		server:   srv,
		listener: listener,
	}, nil
}

func (r *Running) Close(ctx context.Context) error {
	return r.server.Shutdown(ctx)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run:

```bash
go test ./internal/server -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/webui/embed.go internal/server/server.go internal/server/server_test.go
git commit -m "feat(agentunnel): serve embedded web session"
```

---

## Task 6: Wire Up `cmd/agentunnel`, Build Targets, and Documentation

**Files:**
- Create: `cmd/agentunnel/main.go`
- Modify: `Makefile`
- Modify: `README.md`

- [ ] **Step 1: Write the failing CLI smoke check**

Run:

```bash
go build ./cmd/agentunnel
```

Expected: FAIL with `stat .../cmd/agentunnel: directory not found`

- [ ] **Step 2: Implement the new CLI entrypoint**

Create `cmd/agentunnel/main.go`:

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"yuanbohan/tunnel/internal/launcher"
	"yuanbohan/tunnel/internal/server"
	"yuanbohan/tunnel/internal/session"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: agentunnel <claude|codex|gemini> [args...]\n")
		os.Exit(2)
	}

	command, err := launcher.Resolve(os.Args[1], os.Args[2:])
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	running, err := session.StartCommand(ctx, command.Path, command.Args)
	if err != nil {
		log.Fatal(err)
	}
	defer running.Close()

	web, err := server.StartLocal(running.Hub)
	if err != nil {
		log.Fatal(err)
	}
	defer web.Close(context.Background())

	fmt.Fprintf(
		os.Stderr,
		"▶ agentunnel — %s\n  open %s\n  local terminal and browser share the same live session\n\n",
		command.Name,
		web.URL,
	)

	restore, done, err := session.AttachLocalTerminal(ctx, running.Hub)
	if err != nil {
		log.Fatal(err)
	}
	defer restore()

	waitErr := make(chan error, 1)
	go func() {
		waitErr <- running.Wait()
	}()

	select {
	case <-ctx.Done():
	case <-done:
	case err := <-waitErr:
		if err != nil {
			restore()
			log.Fatal(err)
		}
	}
}
```

- [ ] **Step 3: Add build targets and user-facing docs**

Modify `Makefile` to:

```make
.PHONY: agent client agentunnel build clean vet test web web-install web-build

agent:
	go run ./cmd/agent

client:
	go run ./cmd/client

agentunnel: web-build
	@test -n "$(LAUNCHER)" || (echo "usage: make agentunnel LAUNCHER=claude" && exit 1)
	go run ./cmd/agentunnel $(LAUNCHER)

web-install:
	cd web && npm install

web-build:
	cd web && npm run build

web:
	cd web && npm run dev

build: web-build
	go build -o bin/agent ./cmd/agent
	go build -o bin/client ./cmd/client
	go build -o bin/agentunnel ./cmd/agentunnel

clean:
	rm -rf bin/

vet:
	go vet ./...

test:
	go test ./...
	cd web && npm test
```

Modify `README.md` so the top-level run instructions become:

````md
## Run

### Shared live session mode

Use `agentunnel` to launch a supported terminal agent and mirror the same session into the browser.

```bash
make web-install
make agentunnel LAUNCHER=claude
```

Expected stderr output:

```text
▶ agentunnel — claude
  open http://127.0.0.1:43127
  local terminal and browser share the same live session
```

The local terminal remains interactive. Open the printed URL in a browser to view and type into the same live session.

### Legacy tools

The original `agent` and `client` commands remain available:

```bash
make agent
make client
```
````

- [ ] **Step 4: Run the full verification suite**

Run:

```bash
make test
go build ./cmd/agentunnel
```

Expected:

- all Go tests PASS
- all web tests PASS
- `cmd/agentunnel` builds successfully

Then run one manual smoke test for each installed launcher:

```bash
go run ./cmd/agentunnel claude
go run ./cmd/agentunnel codex
go run ./cmd/agentunnel gemini
```

Expected manual behavior:

- a localhost URL is printed
- the launching terminal remains interactive
- opening the URL shows the same live PTY session
- typing in the browser affects the same live session

- [ ] **Step 5: Commit**

```bash
git add cmd/agentunnel/main.go Makefile README.md
git commit -m "feat(agentunnel): add shared live agent launcher"
```

---

## Task 7: Final Cleanup and Regression Check

**Files:**
- Review only: `cmd/agent/main.go`
- Review only: `cmd/client/main.go`
- Review only: `internal/agent/handler.go`
- Review only: `web/src/connection.ts`

- [ ] **Step 1: Verify the legacy commands still build**

Run:

```bash
go build ./cmd/agent
go build ./cmd/client
```

Expected: both commands build successfully

- [ ] **Step 2: Verify the tracked frontend bundle is current**

Run:

```bash
cd web && npm run build && git diff -- ../internal/webui/dist
```

Expected: no diff after rebuilding `internal/webui/dist`

- [ ] **Step 3: Verify there are no unexpected changes**

Run:

```bash
git status --short
```

Expected: clean working tree

- [ ] **Step 4: Commit if the rebuild changed generated assets**

```bash
git add internal/webui/dist
git commit -m "chore(agentunnel): refresh embedded web bundle"
```

Skip this step if `git status --short` is already clean after Step 2.

---

## Spec Coverage Check

- **Launcher support for `claude`, `codex`, `gemini`:** Task 1, Task 6
- **PTY-owned live session:** Task 2, Task 3
- **Local terminal remains interactive:** Task 3, Task 6
- **Browser attaches to same session:** Task 4, Task 5, Task 6
- **Localhost HTTP/WebSocket server:** Task 5
- **Keep original browser terminal behavior:** Task 4, Task 5
- **Build and documentation updates:** Task 6, Task 7
