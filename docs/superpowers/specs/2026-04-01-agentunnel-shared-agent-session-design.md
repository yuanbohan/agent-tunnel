# Agentunnel Shared Agent Session Design

## Summary

`agentunnel` will launch a supported terminal agent (`claude`, `codex`, or `gemini`) inside a PTY, keep the launching terminal fully interactive, and expose the same live session to a localhost web terminal. The browser is a second client attached to the same PTY stream, so the original terminal UX, approval prompts, and interactive behavior remain unchanged.

The first release is intentionally narrow:

- Supported launchers: `claude`, `codex`, `gemini`
- Network scope: localhost only
- Input model: local terminal and web terminal can both type at any time
- Session model: one `agentunnel` process owns one PTY-backed session and serves one web app for that session

## Goals

- Let the user run `agentunnel claude`, `agentunnel codex`, or `agentunnel gemini`
- Preserve the native terminal UX of the launched tool in the local terminal
- Mirror the exact same session into a browser terminal
- Allow the browser to send input into the live session, not just observe it
- Reuse the existing browser terminal work where it fits

## Non-Goals

- Attaching to an already-running Claude/Codex/Gemini session started outside `agentunnel`
- Generic arbitrary-command launching in v1
- Detached daemon or session list UI
- Remote sharing, authentication, or non-localhost exposure
- Input locking, ownership, or conflict prevention between local and browser clients

## User Experience

### CLI shape

The primary entrypoint becomes:

```bash
agentunnel <launcher> [launcher args...]
```

Examples:

```bash
agentunnel claude
agentunnel codex
agentunnel gemini
agentunnel claude --resume
```

`agentunnel` validates that the requested launcher is one of the supported names, resolves the underlying executable from `PATH`, and forwards all remaining arguments unchanged.

### Runtime behavior

When the user runs `agentunnel claude`:

1. `agentunnel` starts `claude` in a PTY
2. `agentunnel` switches the caller terminal into raw mode and proxies that terminal to the PTY
3. `agentunnel` starts a localhost HTTP/WebSocket server for the same session
4. `agentunnel` prints a local URL such as `http://127.0.0.1:43127/`
5. The browser connects to that URL and displays the same live session

From the user's perspective, the local terminal still behaves like the normal Claude/Codex/Gemini terminal UI. The browser is an additional live terminal attached to the same PTY, so typing in either place affects the same session.

## Chosen Architecture

The chosen approach is an embedded per-session server.

Each `agentunnel` invocation owns:

- one launched agent process
- one PTY connected to that process
- one local terminal proxy attached to the PTY
- one embedded HTTP server bound to `127.0.0.1`
- zero or more browser WebSocket clients attached to the PTY

This is preferred over a background daemon because it matches the desired CLI shape, keeps the lifecycle obvious, and avoids building session discovery infrastructure before it is needed.

## Components

### 1. Launcher Registry

A small internal registry maps supported launcher names to executable names and launch behavior.

For v1:

- `claude` -> executable `claude`
- `codex` -> executable `codex`
- `gemini` -> executable `gemini`

Responsibilities:

- validate supported launcher names
- look up executables on `PATH`
- construct the final `exec.Cmd` with passthrough args
- produce clear errors when an executable is unavailable

This is intentionally not a generic plugin system. The design keeps the launcher boundary explicit so future support for more tools can be added without overdesigning v1.

### 2. PTY Session Hub

The PTY session hub is the core abstraction replacing the current single-client model.

Responsibilities:

- start the selected process under a PTY
- read bytes from the PTY and broadcast them to all attached clients
- accept input bytes from any attached client and write them into the PTY
- apply resize events from browser clients to the PTY
- close the session when the child process exits or the owner process shuts down

The hub owns the PTY and process lifecycle. It does not know whether a client is "local" or "web"; both are just input/output peers with different transport adapters.

### 3. Local Terminal Adapter

The local terminal stays interactive by turning stdin/stdout into another hub client.

Responsibilities:

- enter raw mode
- read stdin bytes and forward them to the session hub
- write PTY output bytes to stdout
- restore terminal state on exit, error, or signal

This adapter is what preserves the "works like normal" requirement. The launched tool is not given direct ownership of the user's terminal; instead, `agentunnel` proxies the full byte stream so it can mirror that same session to the browser.

### 4. Embedded Web Server

Each `agentunnel` process serves its own browser client on an ephemeral localhost port.

Routes:

- `GET /` -> web terminal app
- `GET /ws` -> session WebSocket

The server binds to `127.0.0.1` by default and prints the resolved URL to the terminal after startup.

The existing web client can be adapted for this mode rather than requiring a separate dev server. In the packaged flow, static assets should be built once and embedded into the Go binary.

### 5. Browser Terminal Client

The browser terminal remains xterm.js-based and keeps the current wire protocol shape:

- `input` frames send base64-encoded input bytes
- `output` frames carry base64-encoded PTY output bytes
- `resize` frames carry terminal dimensions

Required behavior:

- connect automatically to the session WebSocket served by the current `agentunnel` process
- display connection state
- reconnect manually if needed
- write raw PTY output bytes without altering terminal semantics

The browser must act as a second terminal, not a translated UI layer. Approval prompts and other interactive flows remain whatever the underlying tool renders in the PTY.

## Data Flow

### Startup

1. User invokes `agentunnel <launcher> [args...]`
2. CLI validates the launcher and resolves the executable
3. `agentunnel` starts the child process in a PTY
4. `agentunnel` starts the session hub and embedded web server
5. `agentunnel` enters raw mode and attaches the local terminal adapter
6. Browser clients can connect to the printed localhost URL

### Output path

1. Child process writes bytes to PTY
2. Session hub reads PTY bytes
3. Session hub broadcasts the same bytes to:
   - local stdout adapter
   - all connected browser WebSocket clients

### Input path

1. Local stdin bytes or browser `input` frames arrive at the session hub
2. Session hub writes those bytes into the PTY
3. Child process receives the input as if it came from one terminal

### Resize path

1. Browser terminal reports rows/cols changes
2. Session hub applies the resize to the PTY

For v1, the local terminal remains the primary terminal for startup behavior, but browser resizes are allowed to affect the PTY once connected. This is acceptable because the session is intentionally fully shared and conflict resolution is out of scope.

## Error Handling

### Startup failures

- Unknown launcher: exit with a clear list of supported launchers
- Executable not found: exit with a message that names the missing binary
- PTY start failure: exit with the underlying error
- HTTP bind failure: exit with the underlying error

### Runtime behavior

- Browser disconnect does not stop the PTY session
- Malformed browser frames are ignored and do not crash the session
- If the child process exits, the web session closes and the owner process exits
- On signal or fatal error, `agentunnel` restores the local terminal before exiting

### Input conflicts

Simultaneous typing from local and browser terminals is allowed by design in v1. No serialization, lock, or ownership mechanism is introduced. The system should document this clearly rather than pretending to prevent collisions.

## Compatibility with Existing Repo

The repo already has:

- PTY spawning code in Go
- a JSON/base64 WebSocket protocol
- a browser terminal client built with xterm.js

The main architectural change is replacing the current single WebSocket -> single shell model with a session hub that can fan out PTY output and accept input from both the local terminal and browser clients.

To reduce migration risk:

- existing protocol framing should remain compatible where possible
- existing web terminal code should be reused instead of rewritten
- existing `cmd/agent` and `cmd/client` can remain during the transition, while `agentunnel` becomes the new primary UX for shared live sessions

## Testing Strategy

### Go

- unit tests for launcher validation and executable resolution
- unit tests for session hub fanout behavior using stub client adapters
- unit tests for PTY/session shutdown behavior where practical

### Web

- keep protocol encoding/decoding tests
- verify the web app can connect to a session-relative `/ws` endpoint

### Integration

- smoke test `agentunnel claude`, `agentunnel codex`, and `agentunnel gemini` when the corresponding executables are present
- manual validation that:
  - local terminal remains interactive
  - browser displays identical output
  - browser input reaches the live session
  - terminal approval prompts still behave exactly as rendered by the underlying tool

## Open Decisions Chosen for v1

These decisions were previously ambiguous and are now fixed for v1:

- one `agentunnel` invocation owns one session
- the web app is per-session, not a global dashboard
- localhost only, no auth
- full concurrent input from local and browser terminals is allowed
- support exactly `claude`, `codex`, and `gemini`
- launcher arguments are forwarded unchanged

## Future Extensions

Future work can build on this design without changing the v1 model:

- add more launchers
- introduce a detached daemon and session list
- add remote sharing and authentication
- add explicit input ownership or observer mode
- support attaching to an already-running session
