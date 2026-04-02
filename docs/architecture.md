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
- relay maintains a live in-memory session registry
- browsers authenticate to the relay, list live sessions, and attach to one session terminal stream

The localhost single-session server remains available for local use. Relay mode is additive.

## Package Dependency Graph

```
cmd/agentunnel
├── internal/launcher       ← resolves executable name to PATH
├── internal/session        ← PTY lifecycle, Hub, local terminal
│   └── (no internal deps)
├── internal/server         ← HTTP server, WebSocket bridge
│   ├── internal/session    ← implements LiveSession interface
│   ├── internal/protocol   ← message encoding (JSON + base64)
│   └── internal/webui      ← embedded static assets (web/dist)
└── (stdlib: context, os, syscall, signal)

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
        ├─ 1. launcher.Resolve(name, args)      → launcher.Command
        ├─ 2. session.PrepareLocalTerminal()     → LocalTerminal
        ├─ 3. session.StartCommandWithInitialSinks(ctx, path, args, sinks)
        │                                        → session.Running
        ├─ 4. server.StartLocal(hub)             → server.Running
        ├─ 5. localTerminal.Start(ctx, hub)      → <-chan struct{}
        └─ 6. waitForProcessOrShutdown()         → blocks until exit
```

Key design choice: the local terminal is registered as an initial sink *before* the process starts, ensuring no output is lost between process start and sink registration.

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

## Web Client Modules

The browser client is a TypeScript application built with Vite, using xterm.js for terminal rendering.

### Module Interaction

```
index.html
  └─ <script type="module" src="main.ts">

main.ts (orchestrator)
  ├─ imports session_url.ts     → sessionWebSocketURL()
  ├─ imports connection.ts      → ConnectionManager
  ├─ imports terminal.ts        → createTerminal()
  └─ imports protocol.ts        → encodeInput(), decodeOutput()

Wiring:
  terminal.onData ──> encodeInput() ──> connection.send()     (user types)
  terminal.onResize ──> JSON ──> connection.send()            (terminal resized)
  connection.onMessage ──> decodeOutput() ──> terminal.write() (PTY output)
  connection.onStatusChange ──> update status bar UI           (connected/disconnected)
```

### `main.ts` -- Application Entry

Creates all components and wires them together:

```
boot()
  ├─ sessionWebSocketURL(window.location) → ws://127.0.0.1:PORT/ws
  ├─ createTerminal(container)            → TerminalHandle
  ├─ new ConnectionManager(wsURL)         → conn
  │
  ├─ terminal.onData(str)
  │    └─ conn.send(encodeInput(str))
  │
  ├─ terminal.onResize({cols, rows})
  │    └─ conn.send(JSON.stringify({type:"resize", cols, rows}))
  │
  ├─ conn.onMessage(msg)
  │    └─ if msg.type === "output"
  │         terminal.write(decodeOutput(msg))
  │
  └─ conn.onStatusChange(status)
       └─ update DOM indicator + reconnect button
```

### `terminal.ts` -- xterm.js Wrapper

Creates and configures an xterm.js Terminal instance. Returns a `TerminalHandle` interface that hides xterm.js internals from the rest of the app.

```typescript
interface TerminalHandle {
  write(data: Uint8Array): void   // display PTY output
  onData(cb: (data: string) => void): void  // user keyboard input
  onResize(cb: (size: {cols, rows}) => void): void
  currentSize(): {cols: number, rows: number}
  dispose(): void
}
```

Features:
- FitAddon for responsive resizing via ResizeObserver
- WebLinksAddon for clickable URLs
- Dark theme (Tokyonight-inspired)

### `connection.ts` -- WebSocket Manager

Manages the WebSocket lifecycle with automatic reconnection.

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
│  2. session.PrepareLocalTerminal()                           │
│     └─ enters raw mode, saves restore func                   │
│                                                              │
│  3. session.StartCommandWithInitialSinks(ctx, path, args,    │
│         {"local": stdoutSink})                               │
│     ├─ pty.Start(exec.Command("claude", "--resume"))         │
│     ├─ creates Hub with ptmx.Write and pty.Setsize           │
│     ├─ registers stdoutSink as "local" sink                  │
│     └─ starts read loop goroutine                            │
│                                                              │
│  4. server.StartLocal(hub)                                   │
│     ├─ binds to 127.0.0.1:0 (ephemeral port)                │
│     ├─ prints URL: http://127.0.0.1:43127                   │
│     └─ starts serving in background goroutine                │
│                                                              │
│  5. localTerminal.Start(ctx, hub)                            │
│     ├─ starts stdin→hub input forwarding goroutine           │
│     └─ starts SIGWINCH resize handler goroutine              │
│                                                              │
│  6. waitForProcessOrShutdown()                               │
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
