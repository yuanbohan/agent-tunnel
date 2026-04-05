# Agent Tunnel Architecture

This document describes the current `agentunnel` architecture: a local PTY owner process connects to a remote relay, and external clients consume the relay's HTTP and WebSocket APIs. The relay does not ship a bundled frontend.

## High-Level Model

```
                         agentunnel process
 ┌──────────────────────────────────────────────────────────────┐
 │                                                              │
 │  launcher.Resolve()                                          │
 │         │                                                    │
 │         ▼                                                    │
 │    exec.Command()                                            │
 │         │                                                    │
 │         ▼                                                    │
 │     PTY child  <───────>  PTY master                         │
 │         │                     │                              │
 │         │                     ▼                              │
 │         │                 session.Hub                        │
 │         │              ┌───────────────┐                     │
 │         │              │ output fanout │                     │
 │         │              │ input routing │                     │
 │         │              │ resize hook   │                     │
 │         │              └──────┬────────┘                     │
 │         │                     │                              │
 │         │          ┌──────────┴──────────┐                   │
 │         │          │                     │                   │
 │         ▼          ▼                     ▼                   │
 │    local terminal stdout sink      relay connector          │
 │    local terminal stdin/input      outbound /agent/ws       │
 └──────────────────────────────────────────────┬───────────────┘
                                                │
                                                ▼
                                ┌─────────────────────────────┐
                                │         relay server        │
                                │                             │
                                │  auth + HTTP/WS handlers    │
                                │  live session registry      │
                                │  in-memory output history   │
                                │  unread + preview tracking  │
                                └──────────────┬──────────────┘
                                               │
                          ┌────────────────────┴────────────────────┐
                          ▼                                         ▼
                external client A                          external client B
                list / history / ws                        list / history / ws
```

## Runtime Boundaries

- `agentunnel` owns the PTY, local terminal raw mode, and local resize/input forwarding.
- The relay owns only live in-memory session state. It is not durable storage.
- External clients own presentation, terminal rendering, and any durable local history.
- Terminal size is owned by the agent side. Clients never send resize frames.

## Package Layout

```
cmd/agentunnel
├── connector
├── launcher
├── protocol
└── session

cmd/relay
├── protocol
└── relay
```

## Package Responsibilities

### `cmd/agentunnel`

Entry point for launching a supported terminal agent and binding it to the relay.

Startup flow:

1. Parse CLI args and required relay env.
2. Resolve the launcher executable with `launcher.Resolve`.
3. Put the local terminal into raw mode with `session.PrepareLocalTerminal`.
4. For `codex`, start a dedicated local `codex app-server`, discover its loopback WebSocket URL, and change the PTY child command to `codex --remote <app-server-url> ...`.
5. Start the child process under a PTY with `session.StartCommandWithInitialSinks`.
6. Register both the local stdout sink and relay connector sink before output starts.
7. Run the outbound relay connector.
8. For `codex`, run a sidecar app-server monitor that derives session-level `action_required` state from structured thread status.
9. Forward local stdin and local resize events through the session hub.

Important constraint:

- Relay connectivity is mandatory. There is no localhost fallback UI or standalone mode.

### `launcher/`

Resolves the supported launcher name to a real executable on `PATH`.

Supported launchers:

- `claude`
- `codex`
- `gemini`

### `session/`

Owns the PTY and local terminal behavior.

Main pieces:

- `process.go`: starts the child command under a PTY and reads PTY output.
- `hub.go`: fans PTY output out to sinks and routes input/resize back to the PTY.
- `local_terminal.go`: manages raw mode, stdin forwarding, stdout writing, and SIGWINCH handling.

Key behavior:

- PTY output is read once and copied defensively to each sink.
- Local terminal resize updates the PTY immediately.
- The relay connector receives both PTY output and resize notifications from the same hub.

### `connector/`

Maintains the mandatory outbound WebSocket from `agentunnel` to the relay at `/agent/ws`.

Responsibilities:

- Connect and authenticate with bearer token auth.
- Send one `register` frame when connected.
- Forward PTY output as relay `output` frames.
- Forward local resize changes as relay `resize` frames.
- Forward structured session-state changes as relay `session_state` frames.
- Accept relay `input` frames and push them into the PTY via the hub.
- Reconnect with backoff if the relay connection drops.

### `protocol/`

Defines the wire structures shared by the agent side and relay.

Important types:

- `Message`
  - `input`
  - `output`
  - `resize`
  - `session_state`
- `SessionInfo`
- `AgentFrame`

Relay-specific contract:

- Client-visible `output` frames carry `seq`, `cols`, and `rows`.
- Agent-originated `resize` frames are relay-internal state updates; they are not forwarded to clients as standalone history items.

### `cmd/relay`

Standalone relay entrypoint.

Responsibilities:

- Read auth configuration from environment variables.
- Listen on `0.0.0.0:<port>`.
- Construct the registry and HTTP handler.
- Expose client APIs and the agent WebSocket.

### `relay/`

Core relay server implementation.

Primary modules:

- `auth.go`
  - Basic Auth for client endpoints
  - Bearer token auth for `/agent/ws`
- `registry.go`
  - live session ownership
  - current PTY size
  - latest sequence number
  - shared read marker
  - preview metadata
  - attached client sinks
  - atomic backlog preload plus live attach
- `history.go`
  - retained in-memory output frames
  - `after`-only history snapshots
  - per-output `cols` / `rows`
- `preview.go`
  - ANSI-stripped preview extraction from recent output
- `server.go`
  - `/api/sessions`
  - `/api/sessions/:id/history`
  - `/api/sessions/:id/read`
  - `/api/sessions/:id/ws`
  - `/agent/ws`
  - `/healthz`

## Relay State Model

The relay keeps only live, in-memory state per session:

- session metadata from the initial `register`
- current PTY size from the latest agent `resize`
- rolling retained output frames
- monotonic `latestSeq`
- shared `lastReadSeq`
- unread count derived from `latestSeq - lastReadSeq`
- latest text preview and raw preview frame
- current session state (`normal` or `action_required`)
- optional `action_required_since` timestamp for the current unresolved waiting episode

If the owning agent disconnects:

- the session is removed
- retained history is dropped
- unread state is dropped
- session-state snapshot is dropped
- attached clients stop receiving output

## History And Replay Model

The current replay model is intentionally narrow:

- history sync uses only `after`
- `after=0` means "return the entire currently retained buffer"
- only output frames get sequence numbers
- each retained output frame carries the PTY size active when that output was produced

Example history frame:

```json
{
  "seq": 42,
  "data_b64": "SGVsbG8=",
  "cols": 132,
  "rows": 43
}
```

Why the size lives on each output:

- clients can replay output correctly even if a prior resize signal was missed
- pagination anchors or replay-time resize lookups are unnecessary
- the client needs only one durable cursor: highest applied `seq`

## Client Attach Semantics

Client attach uses:

```text
GET /api/sessions/:id/ws?after=<seq>
```

The relay guarantees this order:

1. load retained output with `seq > after`
2. preload those frames into the client sink
3. register that sink with the live session
4. start streaming new live output

This avoids the classic gap where output produced between "history fetch" and "live attach" could be lost.

Session-state transitions use a separate client WebSocket:

```text
GET /api/session-events/ws
```

This stream carries machine-readable `normal` / `action_required` transitions and is intentionally not mixed into terminal replay history.

## End-To-End Data Flows

### Output Path

```text
PTY output
→ session read loop
→ session.Hub.BroadcastOutput
→ local stdout sink
→ relay connector
→ relay registry
→ retained history + preview + unread update
→ attached client sinks
```

### Input Path

```text
external client input frame
→ relay WebSocket handler
→ registry route-to-agent
→ connector inbound loop
→ session.Hub.WriteInput
→ PTY stdin
```

The input path is byte-transparent. Relay and agent components do not perform content-level filtering on input frames.

### Resize Path

```text
local SIGWINCH
→ session local terminal handler
→ session.Hub.Resize
→ PTY resize
→ connector sends resize frame
→ relay updates current PTY size
→ future output frames inherit updated cols/rows
```

Clients do not receive standalone resize events. They apply the size attached to each output frame.

## Concurrency Notes

Important concurrent loops:

- PTY read loop in `session/process.go`
- local stdin polling loop in `session/local_terminal.go`
- local resize signal loop in `session/local_terminal.go`
- connector outbound and inbound WebSocket loops
- relay heartbeat / read-deadline management for agent sockets

Safety properties:

- session hub uses a mutex-protected sink map
- registry serializes session mutation under lock
- live attach preloads backlog before sink registration under the same registry critical section
- retained history stores complete output frames rather than partial byte slices

## External Dependencies

| Dependency | Purpose |
|---|---|
| `github.com/creack/pty` | PTY creation and resize |
| `github.com/gorilla/websocket` | WebSocket transport |
| `golang.org/x/sys/unix` | non-blocking stdin polling |
| `golang.org/x/term` | raw mode and terminal size |
