# Agent Tunnel Architecture

This document describes the architecture of `agentunnel` -- how Go packages and web client modules interact to launch a terminal agent, keep the local terminal interactive, and stream the live session to a remote relay for browser access, lightweight live history replay, and shared unread tracking.

## High-Level Overview

```
                          agentunnel process
 ┌──────────────────────────────────────────────────────────────┐
 │                                                              │
 │  ┌──────────┐   resolves    ┌──────────┐   spawns  ┌──────┐ │
 │  │ Launcher │──────────────>│ Process  │──────────>│ PTY  │ │
 │  │ Registry │  executable   │ Manager  │ child cmd │master│ │
 │  └──────────┘               └────┬─────┘           └──┬───┘ │
 │                                  │ owns               │     │
 │                              ┌───▼───┐   read bytes   │     │
 │                              │  Hub  │<───────────────┘     │
 │                              │(fanout│   write bytes         │
 │                              │center)│────────────────>PTY  │
 │                              └┬────┬─┘                      │
 │                broadcast /    │    │    broadcast /          │
 │                write input    │    │    write input          │
 │       ┌───────────────────────┘    └──────────────┐         │
 │       │                                           │         │
 │  ┌────▼─────────┐                          ┌──────▼──────┐  │
 │  │   Local      │                          │  Connector  │  │
 │  │   Terminal   │                          │  (relay     │  │
 │  │   Adapter    │                          │   sink)     │  │
 │  └──────────────┘                          └──────┬──────┘  │
 │       ▲  │                                        │         │
 │  stdin│  │stdout                      outbound WS │         │
 │       │  ▼                                        │         │
 │  ┌──────────┐                                     │         │
 │  │  User's  │                                     │         │
 │  │ Terminal │                                     │         │
 │  └──────────┘                                     │         │
 └───────────────────────────────────────────────────┼─────────┘
                                                     │
                              ┌───────────────────────────────────┐
                              │           Relay Server            │
                              │                                   │
                              │  /agent/ws  ← agent connects here │
                              │  /          ← dashboard            │
                              │  /sessions/:id ← session terminal  │
                              │  /api/sessions ← session list API  │
                              │  /api/sessions/:id/history         │
                              │  /api/sessions/:id/read            │
                              │  /api/sessions/:id/ws ← browser WS│
                              │                                   │
                              │  ┌──────────┐   ┌──────────────┐  │
                              │  │ Registry │   │ Basic Auth   │  │
                              │  │ (live    │   │ (browsers)   │  │
                              │  │ sessions,│   │ Bearer Auth  │  │
                              │  │ history, │   │ (agents)     │  │
                              │  │ unread)  │   └──────────────┘  │
                              │                 └──────────────┘  │
                              └──────────────────┬────────────────┘
                                                 │
                                           ┌─────▼──────┐
                                           │  Browser   │
                                           │ (xterm.js  │
                                           │  client)   │
                                           └────────────┘
```

## Package Dependency Graph

```
cmd/agentunnel
├── connector       ← mandatory outbound relay connector
├── protocol        ← wire types (Message, SessionInfo, AgentFrame)
├── launcher        ← resolves executable name via PATH
├── session         ← PTY lifecycle, Hub, local terminal
│   └── (no package deps)
└── (stdlib: context, os, syscall, signal)

cmd/relay
├── relay           ← auth, registry, preview extraction, HTTP/WS handlers
│   ├── protocol    ← browser/agent terminal traffic + session payloads
│   └── webui       ← embedded web assets (web/dist)
└── (stdlib: net/http, os, flag, time)
```

## Go Packages

### `cmd/agentunnel` -- Orchestrator

**Entry point.** Wires together all components in the correct startup sequence. The relay connection is mandatory; `agentunnel` will not start without `--relay-addr` or `AGENTUNNEL_RELAY_ADDR` and `AGENTUNNEL_RELAY_TOKEN`.

```
main()
  └─> runWithArgs(args, stderr)
        │
        ├─ 1. parseRunArgs(args)                 → runArgs{Label, RelayAddr, RelayToken, Launcher, LauncherArgs}
        ├─ 2. launcher.Resolve(name, args)       → launcher.Command
        ├─ 3. session.PrepareLocalTerminal()     → LocalTerminal
        ├─ 4. build initial sink map             → {local stdout, relay connector}
        ├─ 5. session.StartCommandWithInitialSinks(ctx, path, args, sinks)
        │                                        → session.Running
        ├─ 6. connector.BindHub(hub) + go connector.Run(ctx)
        ├─ 7. localTerminal.Start(ctx, hub)      → <-chan struct{}
        └─ 8. waitForProcessOrShutdown()         → blocks until exit
```

Key design choices:
- the local terminal sink and relay connector are both registered *before* the child process starts, so no early output is lost
- relay connection is mandatory; there is no localhost HTTP server
- relay metadata (`label`, `cwd`, `command_preview`, `started_at`) is constructed in `cmd/agentunnel` and sent once per registration

### `launcher/` -- Launcher Registry

Validates that the requested launcher name (`claude`, `codex`, `gemini`) is supported and resolves it to an executable via `exec.LookPath`.

```go
type Command struct {
    Name string   // "claude", "codex", or "gemini"
    Path string   // resolved absolute path from PATH
    Args []string // forwarded arguments
}
```

```
Resolve("claude", ["--resume"])
  ├─ validate name ∈ {claude, codex, gemini}
  ├─ exec.LookPath("claude") → /usr/local/bin/claude
  └─ return Command{Name: "claude", Path: "/usr/local/bin/claude", Args: ["--resume"]}
```

### `session/` -- PTY Session Management

This is the core package. It contains three tightly related components. Before diving in, it helps to understand the PTY abstraction they all depend on.

#### What is a PTY?

A PTY (pseudo-terminal) is a kernel-provided pair of virtual devices that act like a pipe with terminal semantics:

```
┌─────────────────────────────────────────────────────┐
│                   Kernel PTY pair                    │
│                                                     │
│   PTY Master (ptmx)          PTY Slave (pts)        │
│   ┌──────────────┐           ┌──────────────┐       │
│   │  *os.File    │◄────────►│  looks like   │       │
│   │  read/write  │  kernel   │  a real       │       │
│   │  byte stream │  pipe     │  terminal     │       │
│   └──────┬───────┘           └──────┬───────┘       │
│          │                          │                │
└──────────┼──────────────────────────┼────────────────┘
           │                          │
           ▼                          ▼
      agentunnel                 claude process
      (the controller)          (the child, cmd)
```

**PTY master (`ptmx`)** -- the controlling side. `agentunnel` holds this file descriptor. Writing bytes to it delivers input to the child. Reading bytes from it captures the child's output. It's just a raw byte stream with no terminal behavior.

**PTY slave (`pts`)** -- the terminal side. The kernel assigns this to the child process as its stdin/stdout/stderr. From `claude`'s perspective, it believes it's connected to a real terminal -- it can query terminal size, detect color support, render TUI elements, etc. The slave side is where the kernel applies terminal processing (line editing, signal generation, echo).

**Child process (`cmd`)** -- the actual `claude`/`codex`/`gemini` process. It's attached to the slave side. It never sees or interacts with the master.

In the code, this is set up by:

```go
cmd  = exec.Command("claude", "--resume")  // the process to run
ptmx = pty.Start(cmd)                      // starts cmd with its stdin/stdout/stderr
                                           // connected to a new PTY slave;
                                           // returns the master side to us
```

After this:

| Operation | What happens |
|---|---|
| `ptmx.Read(buf)` | Captures what `claude` writes to its "terminal" (its stdout/stderr) |
| `ptmx.Write(buf)` | Sends keystrokes into `claude`'s "terminal" (its stdin) |
| `pty.Setsize(ptmx, ...)` | Changes the terminal dimensions, causing the kernel to send SIGWINCH to `claude` |

The key insight: **`claude` never knows it's not a real terminal.** The PTY slave is indistinguishable from a hardware terminal. That's why all the TUI rendering, color codes, and interactive prompts work unchanged -- `agentunnel` just intercepts the byte stream on the master side and fans it out to both the local terminal and browser clients via the relay.

#### Hub (`hub.go`)

The central fanout coordinator. All PTY output flows through the Hub to reach every connected sink. All input (from any source) flows through the Hub into the PTY.

```
                   OutputSink interface
                   ┌──────────────────┐
                   │ WriteOutput([]byte) error │
                   └──────────────────┘
                         ▲
                ┌────────┼────────┐
                │                 │
           localStdout      connector
                │                 │
                └────────┼────────┘
                         │ registered in
                    ┌────▼────┐
                    │   Hub   │
                    ├─────────┤
                    │ sinks   │ map[string]OutputSink
                    │ writeIn │ func([]byte) error  → writes to PTY
                    │ resize  │ func(int,int) error  → resizes PTY
                    └─────────┘
                         │
          BroadcastOutput(data)
          ├─ lock sinks (RLock)
          ├─ for each sink:
          │    copy data → sink.WriteOutput(copy)
          └─ unlock
```

Key properties:
- **Defensive copying**: each sink gets its own copy of the byte slice
- **Thread-safe**: `sync.RWMutex` protects the sinks map
- **Transport-agnostic**: the Hub doesn't know if a sink is stdout or a relay connector

#### Process (`process.go`)

Manages PTY creation, the child process lifecycle, and the PTY read loop.

```
StartCommandWithInitialSinks(ctx, path, args, initialSinks)
  │
  ├─ exec.CommandContext(ctx, path, args...)
  ├─ pty.Start(cmd)               → ptmx (PTY master file)
  ├─ NewHub(ptmx.Write, pty.Setsize)
  ├─ register initialSinks on Hub
  └─ goroutine: readLoop
       │
       └─ loop:
            buf := make([]byte, 4096)
            n, err := ptmx.Read(buf)
            hub.BroadcastOutput(buf[:n])
            if err → close readDone channel, return
```

```go
type Running struct {
    Hub      *Hub       // the fanout hub
    ptmx     *os.File   // PTY master
    cmd      *exec.Cmd  // child process
    waitDone chan struct{}
    readDone chan struct{}
}
```

Shutdown sequence:
1. `Wait()` waits for the child process to exit
2. Closes the PTY master (`ptmx.Close()`)
3. Waits for the read loop to finish (`<-readDone`)
4. Returns the exit error (nil on clean exit)

#### LocalTerminal (`local_terminal.go`)

Turns the user's terminal into a raw-mode Hub client.

```
PrepareLocalTerminal()
  ├─ term.MakeRaw(stdin)          → saves restore func
  └─ creates stdoutSink (writes to os.Stdout)

Start(ctx, hub)
  ├─ goroutine: copyInput
  │    └─ loop:
  │         unix.Poll(stdin, 100ms)
  │         os.Stdin.Read(buf)
  │         hub.WriteInput(copy of buf)
  │
  ├─ goroutine: handleResize
  │    └─ on SIGWINCH:
  │         term.GetSize(stdin)
  │         hub.Resize(cols, rows)
  │
  └─ returns <-chan struct{} (done when input loop exits)
```

### `protocol/` -- Wire Format and Relay Types

Defines the JSON message structure shared between Go and TypeScript, plus the relay-specific session and agent frame types.

```go
type Message struct {
    Type string `json:"type"`           // "input" | "output" | "resize"
    Data string `json:"data,omitempty"` // base64-encoded bytes
    Cols int    `json:"cols,omitempty"` // resize only
    Rows int    `json:"rows,omitempty"` // resize only
}
```

Relay types in the same package:

```go
type SessionInfo struct {
    SessionID      string
    Launcher       string
    Label          string
    CWD            string
    CommandPreview string
    StartedAt      time.Time
    LastPreview    string
    LastActiveAt   *time.Time
}

type AgentFrame struct {
    Type    string
    Session *SessionInfo
    Data    string
    Cols    int
    Rows    int
}
```

`RegisterFrame(info)` builds the initial `{"type":"register"}` message that an `agentunnel` process sends when it first attaches to the relay.

Example messages:

```json
{"type":"output","data":"SGVsbG8gV29ybGQ="}
{"type":"input","data":"bHM="}
{"type":"resize","cols":120,"rows":40}
```

### `webui/` -- Embedded Assets

Uses Go's `//go:embed` to bundle the built web client (`web/dist/`) into the binary. Exposes a single function:

```go
func Files() fs.FS  // returns the embedded dist/ filesystem
```

### `cmd/relay` -- Relay Server Entry Point

`cmd/relay` is the standalone relay server entrypoint. It reads auth configuration from environment variables, binds to `0.0.0.0` on the requested port, creates a relay registry, and serves the dashboard UI plus list/history/read/attach endpoints.

```go
type mainConfig struct {
    ListenAddr      string
    BrowserUser     string
    BrowserPassword string
    AgentToken      string
}
```

Environment variables:
- `AGENTUNNEL_BASIC_USER` (required)
- `AGENTUNNEL_BASIC_PASSWORD` (required)
- `AGENTUNNEL_AGENT_TOKEN` (required)

Listen address:

```bash
go run ./cmd/relay --port 9000   # listens on 0.0.0.0:9000
```

### `connector/` -- Outbound Relay Connector

This package makes a live `agentunnel` session visible to the remote relay. The relay connection is mandatory.

Responsibilities:
- open one outbound WebSocket to `.../agent/ws`
- send a single registration frame first
- forward PTY output to the relay as `output`
- route relay `input` and `resize` messages back into the session Hub
- reconnect with backoff when the relay connection drops

The connector is also an `OutputSink`, so `cmd/agentunnel` registers it in the initial sink map alongside the local terminal sink.

### `relay/` -- Relay HTTP / WS Bridge

This package is the relay server's core logic:

- `auth.go`
  - validates browser Basic Auth
  - validates agent bearer token auth
- `registry.go`
  - stores only live sessions
  - retains rolling in-memory output history per live session
  - tracks `latestSeq`, `lastReadSeq`, `unreadCount`, and latest preview frame
  - preserves browser sinks across same-session live replacement
  - sorts list output by `LastActiveAt`
  - serializes input/resize routing safely across peer replacement
- `history.go`
  - appends raw PTY output as whole retained frames
  - evicts oldest whole frames once the 10 MB per-session budget is exceeded
  - serves newest-page, older-page, and bounded `after` snapshots for gap recovery
- `preview.go`
  - strips ANSI noise and extracts a rolling textual preview from raw terminal output
- `server.go`
  - serves `/`, `/sessions/:id`, `/api/sessions`, `/api/sessions/:id/history`, `/api/sessions/:id/read`, `/agent/ws`, and `/api/sessions/:id/ws`
  - applies same-origin checks for browser WebSockets
  - applies heartbeat/read-deadline cleanup for stale agent sockets
  - serves `index.html` from embedded assets

The relay server keeps a strict live-session model: if the owning agent socket goes away, the session disappears from the list along with its retained history and shared read state.

## Web Client Modules

The browser client is a TypeScript application built with Vite, using xterm.js for terminal rendering. There is a single entrypoint:

- `index.html` / `app.ts` -- relay dashboard + session detail

### Module Interaction

```
index.html
  │
  ▼
app.ts
  ├─ routes.ts
  ├─ api.ts
  ├─ dashboard.ts
  ├─ dashboard_preview.ts
  ├─ dashboard_view.ts
  ├─ session_page.ts
  ├─ session_runtime.ts
  ├─ input_filter.ts
  └─ style.css
  │
  ├─ connection.ts
  ├─ terminal.ts
  ├─ protocol.ts
  └─ types.ts

Data flow:
  dashboard poll       ──> api.fetchSessions()                     ──> dashboard.ts + dashboard_preview.ts
  detail init          ──> api.fetchSessionHistory()               ──> session_page.ts ──> session_runtime.ts
  terminal.onData      ──> input_filter ──> protocol.encodeInput() ──> connection.send()
  terminal.onResize    ──> JSON resize frame                       ──> connection.send()
  connection.onMessage ──> protocol.decodeOutput()                 ──> session_page.ts ──> session_runtime.ts
```

### `app.ts` -- Application Entry

Creates the relay UI and handles routing between dashboard and session detail views.

Route handling:
- `/` -> poll `/api/sessions` every 5 seconds, render the live dashboard, and remount mini previews as cards change
- `/sessions/:id` -> fetch recent history, attach xterm.js to `/api/sessions/:id/ws`, then mark the session read once history replay and live attach are active

### Session Page Behavior

- terminal starts in read-only mode
- the compact state chip toggles between `Read-only` and `Input on`
- browser input is only forwarded when the chip is enabled
- known xterm auto-response sequences (CPR, DA reports) are filtered before forwarding
- a resize frame is sent once the socket reaches `connected`
- the newest retained history page is rendered before live output is attached
- if output arrives between history fetch and WebSocket attach, the page bridges that gap by fetching bounded history `after` the last loaded frame and deduping overlaps
- older retained history pages are loaded when the user scrolls near the top
- unread state is exposed as a floating `Jump to N unread` action targeting the first unread sequence

### `terminal.ts` -- xterm.js Wrapper

Creates and configures an xterm.js Terminal instance. Returns a `TerminalHandle` interface that hides xterm.js internals from the rest of the app.

```typescript
interface TerminalHandle {
  write(data: Uint8Array): void
  onData(cb: (data: string) => void): void
  onResize(cb: (cols: number, rows: number) => void): void
  currentSize(): {cols: number, rows: number}
  setDisplayMode(mode: 'scroll' | 'wrap', ptyCols?: number, ptyRows?: number): void
  dispose(): void
}
```

Features:
- FitAddon for responsive resizing via ResizeObserver
- WebLinksAddon for clickable URLs
- scroll/wrap display mode switching
- resize callback support

### `connection.ts` -- WebSocket Manager

Manages the WebSocket lifecycle with explicit reconnect support.

```
ConnectionManager
  │
  ├─ state: 'connecting' | 'connected' | 'disconnected'
  │
  ├─ connect()
  │    ├─ new WebSocket(url)
  │    ├─ onopen  → state = 'connected'
  │    ├─ onclose → state = 'disconnected'
  │    └─ onmessage → parse JSON → invoke onMessage callbacks
  │
  ├─ send(json: string)
  │    └─ if socket.readyState === OPEN → socket.send(json)
  │
  └─ reconnect()
       └─ close current socket → connect()
```

### `protocol.ts` -- Message Encoding

Mirrors the Go `protocol` package:

```typescript
type Message =
  | { type: 'input';  data: string }                         // base64
  | { type: 'output'; seq?: number; data: string }          // base64
  | { type: 'resize'; cols: number; rows: number }

encodeInput(str: string): string
  └─ TextEncoder → Uint8Array → base64 → JSON {type:"input", data:...}

decodeOutput(msg: Message): Uint8Array
  └─ base64 string → Uint8Array (binary PTY output)
```

### Frontend Modules

- `types.ts` -- shared TypeScript shapes for relay session cards, history pages, and read-state responses
- `api.ts` -- fetches `/api/sessions`, `/api/sessions/:id/history`, posts `/api/sessions/:id/read`, and builds browser attach URLs for `/api/sessions/:id/ws`
- `routes.ts` -- parses `/` versus `/sessions/:id`
- `dashboard.ts` -- renders compact session cards with launcher favicon, deduplicated identity copy, unread badge, and preview container
- `dashboard_preview.ts` -- mounts read-only mini xterm previews that render only the latest output frame in wrap mode
- `dashboard_view.ts` -- owns dashboard polling, rerendering, and preview disposal/remounting
- `session_page.ts` -- owns history bootstrap, upward paging, unread-jump logic, and the state-chip helpers (`Read-only` / `Input on`)
- `session_runtime.ts` -- bridges the shared xterm wrapper with session-page frame anchors, scrolling, and unread highlighting
- `input_filter.ts` -- filters xterm-generated auto-responses such as CPR / DA reports before input forwarding
- `style.css` -- mobile-first dashboard + terminal-detail styling

## Data Flow Diagrams

### Startup Sequence

```
User runs: agentunnel --relay-addr 127.0.0.1:8586 claude --resume
    │
    ▼
┌─ cmd/agentunnel ────────────────────────────────────────────┐
│                                                              │
│  1. parseRunArgs(args)                                       │
│     └─ validates relay addr + token are present              │
│                                                              │
│  2. launcher.Resolve("claude", ["--resume"])                 │
│     └─ validates name, finds /usr/local/bin/claude           │
│                                                              │
│  3. session.PrepareLocalTerminal()                           │
│     └─ enters raw mode, saves restore func                   │
│                                                              │
│  4. build initial sink map                                   │
│     ├─ local stdout sink                                     │
│     └─ relay connector sink                                  │
│                                                              │
│  5. session.StartCommandWithInitialSinks(ctx, path, args,    │
│         initialSinks)                                        │
│     ├─ pty.Start(exec.Command("claude", "--resume"))         │
│     ├─ creates Hub with ptmx.Write and pty.Setsize           │
│     ├─ registers local + relay sinks                         │
│     └─ starts read loop goroutine                            │
│                                                              │
│  6. connector.BindHub(hub) + go connector.Run(ctx)           │
│     ├─ dials relay at /agent/ws                              │
│     └─ sends registration frame with session metadata        │
│                                                              │
│  7. localTerminal.Start(ctx, hub)                            │
│     ├─ starts stdin→hub input forwarding goroutine           │
│     └─ starts SIGWINCH resize handler goroutine              │
│                                                              │
│  8. waitForProcessOrShutdown()                               │
│     └─ blocks until child exits or signal received           │
│                                                              │
│  cleanup:                                                    │
│     └─ localTerminal.Restore()                               │
└──────────────────────────────────────────────────────────────┘
```

### Output Path (PTY to all clients)

```
claude process writes to PTY
    │
    │  raw bytes
    ▼
┌─ PTY master (ptmx) ─┐
│  process.go read loop│
│  buf := read(ptmx)   │
└──────────┬───────────┘
           │
           ▼
    hub.BroadcastOutput(buf)
           │
    ┌──────┴──────┐
    │             │
    ▼             ▼
  copy₁        copy₂
    │             │
    ▼             ▼
 stdout      connector
    │             │
    │             │  relay WS → relay server
    │             │       → browser wsSink
    │             │       → JSON encode
    │             │       → browser WebSocket
    │             │
    ▼             ▼
 User's       Browser
Terminal      (via relay)
```

### Input Path (keystroke to PTY)

```
  User's Terminal                         Browser (via relay)
       │                                    │
  os.Stdin.Read()                    xterm.onData("ls\r")
       │                                    │
       ▼                              input_filter check
  hub.WriteInput(buf)                       │
       │                              encodeInput("ls\r")
       │                              {type:"input",data:"bHMN"}
       │                                    │
       │                              WebSocket.send()
       │                                    │
       │                                    ▼
       │                              relay server
       │                              routes to agent connector
       │                                    │
       │                              hub.WriteInput(buf)
       │                                    │
       └──────────────┬────────────────────┘
                      │
                      ▼
               ptmx.Write(buf)
                      │
                      ▼
              claude process
           receives keystrokes
```

### Resize Path

```
  Browser window resized
       │
       ▼
  FitAddon.fit()
  xterm recalculates cols/rows
       │
       ▼
  terminal.onResize({cols: 120, rows: 40})
       │
       ▼
  conn.send({type:"resize", cols:120, rows:40})
       │
       ▼
  relay server
  routes to agent connector
       │
       ▼
  hub.Resize(120, 40)
       │
       ▼
  pty.Setsize(ptmx, {Rows: 40, Cols: 120})
       │
       ▼
  claude process receives SIGWINCH
  and redraws at new dimensions
```

## Concurrency Model

```
Main goroutine
  │
  ├─ [goroutine] PTY read loop (session/process.go)
  │    reads ptmx → hub.BroadcastOutput
  │    exits when ptmx is closed
  │
  ├─ [goroutine] stdin copy loop (session/local_terminal.go)
  │    polls stdin (100ms) → hub.WriteInput
  │    exits on ctx cancel
  │
  ├─ [goroutine] SIGWINCH handler (session/local_terminal.go)
  │    signal.Notify → hub.Resize
  │    exits on ctx cancel
  │
  └─ [goroutine] connector.Run (connector/connector.go)
       ├─ outbound WS write loop
       │    reads from outbound chan → WS write
       └─ inbound WS read loop
            reads JSON frames → hub.WriteInput / hub.Resize
            reconnects with backoff on disconnect
```

Thread safety is maintained through:
- `sync.RWMutex` in Hub protects the sinks map
- `sync.Mutex` in Running protects shutdown state
- Buffered channels in connector provide backpressure (cap=128)

## External Dependencies

| Dependency | Purpose |
|---|---|
| `github.com/creack/pty` | Cross-platform PTY creation and resize |
| `github.com/gorilla/websocket` | WebSocket protocol for Go |
| `golang.org/x/term` | Raw mode, terminal size queries |
| `golang.org/x/sys/unix` | `unix.Poll()` for non-blocking stdin reads |
| `@xterm/xterm` | Browser terminal emulator |
| `@xterm/addon-fit` | Auto-fit terminal to container |
| `@xterm/addon-web-links` | Clickable URL detection |
