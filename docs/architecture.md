# Agent Tunnel Architecture

This document describes the internal architecture of `agentunnel` -- how Go packages and web client modules interact to launch a terminal agent, keep the local terminal interactive, and mirror the same live session to a browser.

## High-Level Overview

```
                            agentunnel process
 ┌──────────────────────────────────────────────────────────────────┐
 │                                                                  │
 │  ┌──────────┐   resolves    ┌──────────┐   spawns    ┌────────┐ │
 │  │ Launcher │──────────────>│ Process  │────────────>│  PTY   │ │
 │  │ Registry │  executable   │ Manager  │  child cmd  │(master)│ │
 │  └──────────┘               └────┬─────┘             └───┬────┘ │
 │                                  │ owns                  │      │
 │                              ┌───▼───┐    read bytes     │      │
 │                              │  Hub  │<──────────────────┘      │
 │                              │(fanout│    write bytes            │
 │                              │center)│───────────────────>PTY   │
 │                              └┬──┬──┬┘                          │
 │                broadcast /    │  │  │    broadcast /             │
 │                write input    │  │  │    write input             │
 │       ┌───────────────────────┘  │  └───────────────────┐       │
 │       │                          │                      │       │
 │  ┌────▼─────────┐          ┌─────▼──────┐         ┌────▼─────┐ │
 │  │   Local      │          │  Browser   │         │ Browser  │ │
 │  │   Terminal   │          │  Client 1  │   ...   │ Client N │ │
 │  │   Adapter    │          │  (wsSink)  │         │ (wsSink) │ │
 │  └──────────────┘          └────────────┘         └──────────┘ │
 │       ▲  │                      ▲  │                            │
 │  stdin│  │stdout           WS   │  │  WS                       │
 │       │  ▼                      │  ▼                            │
 │  ┌──────────┐              ┌──────────┐                         │
 │  │  User's  │              │ Embedded │                         │
 │  │ Terminal │              │   HTTP   │                         │
 │  └──────────┘              │  Server  │                         │
 │                            └────┬─────┘                         │
 └─────────────────────────────────┼───────────────────────────────┘
                                   │ :port on 127.0.0.1
                              ┌────▼─────┐
                              │ Browser  │
                              │ (xterm.js│
                              │  client) │
                              └──────────┘
```

## Relay Extension

Remote mode adds a second server role:

- local `agentunnel` keeps owning the PTY and local terminal
- local `agentunnel` optionally opens one outbound websocket to the relay
- relay authenticates browser traffic with Basic Auth and agent traffic with a bearer token
- relay maintains a live in-memory session registry with rolling preview metadata
- browsers can either list live sessions from the relay dashboard or attach to one session terminal stream

The localhost single-session server remains available for local use. Relay mode is additive.

## Package Dependency Graph

```
cmd/agentunnel
├── internal/relayclient     ← optional outbound relay connector + config
├── internal/relayapi        ← shared relay session/register payloads
├── internal/launcher       ← resolves executable name to PATH
├── internal/session        ← PTY lifecycle, Hub, local terminal
│   └── (no internal deps)
├── internal/server         ← HTTP server, WebSocket bridge
│   ├── internal/session    ← implements LiveSession interface
│   ├── internal/protocol   ← message encoding (JSON + base64)
│   └── internal/webui      ← embedded static assets (web/dist)
└── (stdlib: context, os, syscall, signal)

cmd/relay
├── internal/relayserver    ← auth, registry, preview extraction, relay HTTP/WS handlers
│   ├── internal/protocol   ← browser/agent terminal traffic
│   ├── internal/relayapi   ← register/session payloads
│   └── internal/webui      ← embedded local + relay web assets
└── (stdlib: net/http, os, time)

--- legacy (independent) ---
cmd/agent  → internal/agent   (PTY + WebSocket handler)
cmd/client → internal/client  (WebSocket client + raw terminal)
```

## Go Packages

### `cmd/agentunnel` -- Orchestrator

**Entry point.** Wires together all components in the correct startup sequence.

```
main()
  └─> runWithArgs(args, stderr)
        │
        ├─ 1. parseRunArgs(args)                 → runArgs{Label, RelayURL, Launcher, Args}
        ├─ 2. launcher.Resolve(name, args)       → launcher.Command
        ├─ 3. relayclient.LoadConfig(...)        → optional relay config
        ├─ 4. session.PrepareLocalTerminal()     → LocalTerminal
        ├─ 5. session.StartCommandWithInitialSinks(ctx, path, args, sinks)
        │                                        → session.Running
        ├─ 6. optionally bind/start relay connector
        ├─ 7. server.StartLocal(hub)             → server.Running
        ├─ 8. localTerminal.Start(ctx, hub)      → <-chan struct{}
        └─ 9. waitForProcessOrShutdown()         → blocks until exit
```

Key design choices:
- the local terminal sink is registered *before* the child process starts, so no early output is lost
- relay mode is optional and additive; localhost mode still starts for every `agentunnel` session
- relay metadata (`label`, `cwd`, `command_preview`, `started_at`) is constructed in `cmd/agentunnel` and sent once per relay registration

### `internal/launcher` -- Launcher Registry

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

### `internal/session` -- PTY Session Management

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

The key insight: **`claude` never knows it's not a real terminal.** The PTY slave is indistinguishable from a hardware terminal. That's why all the TUI rendering, color codes, and interactive prompts work unchanged -- `agentunnel` just intercepts the byte stream on the master side and fans it out to both the local terminal and browser clients.

#### Hub (`hub.go`)

The central fanout coordinator. All PTY output flows through the Hub to reach every connected sink. All input (from any source) flows through the Hub into the PTY.

```
                   OutputSink interface
                   ┌──────────────────┐
                   │ WriteOutput([]byte) error │
                   └──────────────────┘
                         ▲
        ┌────────────────┼────────────────┐
        │                │                │
   localStdout      wsSink #1        wsSink #N
        │                │                │
        └────────────────┼────────────────┘
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
- **Transport-agnostic**: the Hub doesn't know if a sink is stdout or a WebSocket

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

### `internal/server` -- HTTP & WebSocket Server

Serves the embedded web UI and bridges browser WebSocket connections into the Hub.

```
StartLocal(session LiveSession)
  │
  ├─ net.Listen("tcp", "127.0.0.1:0")   → ephemeral port
  ├─ NewHandler(session)                 → http.Handler
  │    ├─ GET /     → serves embedded web UI (internal/webui)
  │    └─ GET /ws   → WebSocket upgrade
  │         ├─ origin check (localhost only)
  │         ├─ create wsSink
  │         ├─ session.AddSink(id, wsSink)
  │         └─ read loop:
  │              parse JSON message
  │              switch msg.Type:
  │                "input"  → session.WriteInput(decoded bytes)
  │                "resize" → session.Resize(cols, rows)
  │              on disconnect → session.RemoveSink(id)
  └─ go http.Serve(listener, handler)
```

#### LiveSession Interface

The server depends on the session package only through this interface:

```go
type LiveSession interface {
    AddSink(id string, sink session.OutputSink)
    RemoveSink(id string)
    WriteInput(data []byte) error
    Resize(cols, rows int) error
}
```

`session.Hub` satisfies this interface directly.

#### wsSink -- WebSocket Output Adapter

```
wsSink
  ├─ outbound chan []byte  (buffered, cap=64)
  │
  ├─ WriteOutput(data)
  │    └─ non-blocking send to outbound channel
  │       if channel full → close sink (backpressure)
  │
  └─ run() goroutine
       └─ loop:
            data := <-outbound
            protocol.EncodeOutput(data) → JSON
            conn.WriteMessage(TextMessage, json)
            (5s write timeout)
```

### `internal/protocol` -- Wire Format

Defines the JSON message structure shared between Go and TypeScript:

```go
type Message struct {
    Type string `json:"type"`           // "input" | "output" | "resize"
    Data string `json:"data,omitempty"` // base64-encoded bytes
    Cols int    `json:"cols,omitempty"` // resize only
    Rows int    `json:"rows,omitempty"` // resize only
}
```

Example messages:

```json
{"type":"output","data":"SGVsbG8gV29ybGQ="}
{"type":"input","data":"bHM="}
{"type":"resize","cols":120,"rows":40}
```

### `internal/webui` -- Embedded Assets

Uses Go's `//go:embed` to bundle the built web client (`web/dist/`) into the binary. Exposes a single function:

```go
func Files() fs.FS  // returns the embedded dist/ filesystem
```

### `cmd/relay` -- Relay Server Entry Point

`cmd/relay` is the standalone remote access entrypoint. It reads configuration from environment variables, creates one process-wide relay registry, and serves the relay UI plus attach/list endpoints.

```go
type mainConfig struct {
    ListenAddr      string
    BrowserUser     string
    BrowserPassword string
    AgentToken      string
}
```

Environment variables:
- `AGENTUNNEL_RELAY_ADDR` (defaults to `:8586`)
- `AGENTUNNEL_BASIC_USER`
- `AGENTUNNEL_BASIC_PASSWORD`
- `AGENTUNNEL_AGENT_TOKEN`

### `internal/relayapi` -- Shared Relay Payloads

Defines the shared wire shapes used by the local connector, relay registry, and relay UI list API.

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

### `internal/relayclient` -- Outbound Relay Connector

This package makes a live `agentunnel` session visible to a remote relay without changing the local PTY ownership model.

Responsibilities:
- load optional relay config from `--relay-url` or `AGENTUNNEL_RELAY_URL`
- require `AGENTUNNEL_RELAY_TOKEN` when relay mode is enabled
- open one outbound WebSocket to `.../agent/ws`
- send a single registration frame first
- forward PTY output to the relay as `output`
- route relay `input` and `resize` messages back into the session hub
- reconnect with backoff when the relay connection drops

The connector is also an `OutputSink`, so `cmd/agentunnel` can register it in the same initial sink map as the local terminal sink.

### `internal/relayserver` -- Relay HTTP / WS Bridge

This package is the remote analogue of `internal/server`, but with extra control-plane responsibilities:

- `auth.go`
  - validates browser Basic Auth
  - validates agent bearer token auth
- `registry.go`
  - stores only live sessions
  - preserves browser sinks across same-session live replacement
  - sorts list output by `LastActiveAt`
  - serializes input/resize routing safely across peer replacement
- `preview.go`
  - strips ANSI noise and extracts a rolling textual preview from raw terminal output
- `server.go`
  - serves `/`, `/sessions/:id`, `/api/sessions`, `/agent/ws`, and `/api/sessions/:id/ws`
  - applies same-origin checks for browser WebSockets
  - applies heartbeat/read-deadline cleanup for stale agent sockets
  - serves `relay.html` when present, otherwise a relay-specific fallback shell

The relay server keeps a strict live-session model: if the owning agent socket goes away, the session disappears from the list.

## Web Client Modules

The browser client is a TypeScript application built with Vite, using xterm.js for terminal rendering. It now has two entrypoints:

- `index.html` / `main.ts` for localhost single-session mode
- `relay.html` / `relay_app.ts` for relay dashboard + mobile session detail mode

### Module Interaction

```
index.html                relay.html
  │                         │
  ▼                         ▼
main.ts                 relay_app.ts
  │                         ├─ relay_routes.ts
  │                         ├─ relay_api.ts
  │                         ├─ relay_dashboard.ts
  │                         ├─ relay_session_page.ts
  │                         └─ relay.css
  │
  ├─ session_url.ts
  ├─ input_filter.ts
  ├─ connection.ts
  ├─ terminal.ts
  └─ protocol.ts

Shared runtime pieces:
  terminal.onData      ──> protocol.encodeInput() ──> connection.send()
  terminal.onResize    ──> JSON resize frame      ──> connection.send()
  connection.onMessage ──> protocol.decodeOutput() ──> terminal.write()
```

### `main.ts` -- Application Entry

Creates the localhost single-session UI and wires it to `/ws`.

```
boot()
  ├─ sessionWebSocketURL(window.location) → ws://127.0.0.1:PORT/ws
  ├─ createTerminal(container)            → TerminalHandle
  ├─ new ConnectionManager(wsURL)         → conn
  ├─ create "Wrap / Scroll" display toggle
  │
  ├─ terminal.onData(str)
  │    └─ if !isTerminalAutoResponse(str)
  │         conn.send(encodeInput(str))
  │
  ├─ conn.onMessage(msg)
  │    ├─ if msg.type === "output"
  │    │    terminal.write(decodeOutput(msg))
  │    └─ if msg.type === "resize"
  │         update local scroll-mode viewport size
  │
  └─ conn.onStatusChange(status)
       └─ update DOM indicator + reconnect button
```

The local page intentionally filters known xterm auto-response sequences before forwarding input to the PTY, which avoids feedback loops from terminal query responses.

### `relay_app.ts` -- Relay Dashboard And Session Entry

This is the mobile-first remote UI bootstrap.

Route handling:
- `/` → fetch `/api/sessions` and render the live dashboard
- `/sessions/:id` → attach xterm.js to `/api/sessions/:id/ws`

Session-page behavior:
- terminal starts in read-only mode
- the compact state chip toggles between `Read-only` and `Input on`
- browser input is only forwarded when the chip is enabled
- a resize frame is sent once the socket reaches `connected`

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
- scroll/wrap display mode switching for the localhost page
- resize callback support reused by the relay detail page

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

It does not auto-retry in the browser. The localhost page exposes a reconnect button; the relay session page currently only re-sends the initial resize frame after a successful connection.

### `protocol.ts` -- Message Encoding

Mirrors the Go `internal/protocol` package:

```typescript
type Message =
  | { type: 'input';  data: string }    // base64
  | { type: 'output'; data: string }    // base64
  | { type: 'resize'; cols: number; rows: number }

encodeInput(str: string): string
  └─ TextEncoder → Uint8Array → base64 → JSON {type:"input", data:...}

decodeOutput(msg: Message): Uint8Array
  └─ base64 string → Uint8Array (binary PTY output)
```

### Relay-Specific Frontend Modules

- `relay_types.ts`
  - shared TypeScript shape for relay session cards
- `relay_api.ts`
  - fetches `/api/sessions`
  - builds browser attach URLs for `/api/sessions/:id/ws`
- `relay_routes.ts`
  - parses `/` versus `/sessions/:id`
- `relay_dashboard.ts`
  - renders compact session cards with launcher icon, label, command clue, cwd, preview, and last-active hint
- `relay_session_page.ts`
  - owns the state-chip helpers (`Read-only` / `Input on`)
- `relay.css`
  - mobile-first dashboard + terminal-detail styling
- `input_filter.ts`
  - filters xterm-generated auto-responses such as CPR / DA reports before localhost input forwarding

### `session_url.ts` -- URL Construction

Derives the WebSocket URL from the current page location:

```
https://127.0.0.1:43127/  →  wss://127.0.0.1:43127/ws
http://127.0.0.1:43127/   →  ws://127.0.0.1:43127/ws
```

## Data Flow Diagrams

### Startup Sequence

```
User runs: agentunnel claude --resume
    │
    ▼
┌─ cmd/agentunnel ────────────────────────────────────────────┐
│                                                              │
│  1. launcher.Resolve("claude", ["--resume"])                 │
│     └─ validates name, finds /usr/local/bin/claude           │
│                                                              │
│  2. relayclient.LoadConfig(...)                              │
│     └─ decides whether relay mode is enabled                 │
│                                                              │
│  3. session.PrepareLocalTerminal()                           │
│     └─ enters raw mode, saves restore func                   │
│                                                              │
│  4. build initial sink map                                   │
│     └─ always includes local stdout sink                     │
│     └─ optionally adds relay connector sink                  │
│                                                              │
│  5. session.StartCommandWithInitialSinks(ctx, path, args,    │
│         initialSinks)                                        │
│     ├─ pty.Start(exec.Command("claude", "--resume"))         │
│     ├─ creates Hub with ptmx.Write and pty.Setsize           │
│     ├─ registers local + optional relay sinks                │
│     └─ starts read loop goroutine                            │
│                                                              │
│  6. optionally bind relay connector to Hub and start it      │
│                                                              │
│  7. server.StartLocal(hub)                                   │
│     ├─ binds to 127.0.0.1:0 (ephemeral port)                │
│     ├─ prints URL: http://127.0.0.1:43127                   │
│     └─ starts serving in background goroutine                │
│                                                              │
│  8. localTerminal.Start(ctx, hub)                            │
│     ├─ starts stdin→hub input forwarding goroutine           │
│     └─ starts SIGWINCH resize handler goroutine              │
│                                                              │
│  9. waitForProcessOrShutdown()                               │
│     └─ blocks until child exits or signal received           │
│                                                              │
│  cleanup:                                                    │
│     ├─ server.Close()                                        │
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
    ┌──────┼──────────────┐
    │      │              │
    ▼      ▼              ▼
 copy₁   copy₂         copy₃
    │      │              │
    ▼      ▼              ▼
 stdout  wsSink₁       wsSink₂
    │      │              │
    │      │  ┌───────────┘
    │      │  │
    │      ▼  ▼
    │    JSON encode
    │    {type:"output", data: base64(copy)}
    │      │  │
    │      ▼  ▼
    │    WebSocket.send()
    │      │  │
    ▼      ▼  ▼
 User's  Browser  Browser
Terminal  Tab 1   Tab 2
```

### Input Path (keystroke to PTY)

```
  User's Terminal                        Browser
       │                                    │
  os.Stdin.Read()                    xterm.onData("ls\r")
       │                                    │
       ▼                                    ▼
  hub.WriteInput(buf)              encodeInput("ls\r")
       │                           {type:"input",data:"bHMN"}
       │                                    │
       │                              WebSocket.send()
       │                                    │
       │                                    ▼
       │                           server: read loop
       │                           JSON parse → decodeData
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
  server: read loop
  JSON parse
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
  ├─ [goroutine] PTY read loop (process.go)
  │    reads ptmx → hub.BroadcastOutput
  │    exits when ptmx is closed
  │
  ├─ [goroutine] stdin copy loop (local_terminal.go)
  │    polls stdin (100ms) → hub.WriteInput
  │    exits on ctx cancel
  │
  ├─ [goroutine] SIGWINCH handler (local_terminal.go)
  │    signal.Notify → hub.Resize
  │    exits on ctx cancel
  │
  ├─ [goroutine] HTTP server (server.go)
  │    accepts connections, serves static files
  │
  └─ per WebSocket connection:
       ├─ [goroutine] WS read loop (server.go NewHandler)
       │    reads JSON frames → hub.WriteInput / hub.Resize
       │    exits on disconnect
       │
       └─ [goroutine] wsSink.run() (server.go)
            reads from outbound chan → WS write
            exits when sink is closed
```

Thread safety is maintained through:
- `sync.RWMutex` in Hub protects the sinks map
- `sync.Mutex` in wsSink serializes WebSocket writes
- `sync.Mutex` in Running protects shutdown state
- Buffered channels in wsSink provide backpressure (cap=64)

## Legacy Components

`cmd/agent` and `cmd/client` are the original shell-over-WebSocket pair. They use a simpler model: one PTY, one WebSocket, no Hub fanout.

```
cmd/agent                          cmd/client
  │                                     │
  ▼                                     ▼
internal/agent                    internal/client
  ├─ SpawnShell() → PTY              ├─ EnterRawMode()
  └─ Handler():                      └─ Connect():
     PTY ←→ single WebSocket            stdin/stdout ←→ WebSocket
```

These remain functional but `agentunnel` is the primary interface going forward.

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
