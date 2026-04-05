# Agent Tunnel Architecture

This document explains the stable shape of the system. It stays intentionally high level so small transport or package changes do not force constant rewrites.

## System Shape

`agentunnel` owns the real local agent process and its PTY. The relay exposes authenticated APIs so external clients can observe or interact with that live session.

```text
local machine
┌──────────────────────────────────────────────────────────────┐
│                          agentunnel                          │
│                                                              │
│  launcher resolve                                            │
│        │                                                     │
│        ▼                                                     │
│  local runtime                                               │
│  - claude / gemini: PTY child                                │
│  - codex: app-server sidecar + codex --remote PTY child      │
│        │                                                     │
│        ▼                                                     │
│     session hub                                              │
│  - PTY output fanout                                         │
│  - PTY input routing                                         │
│  - resize propagation                                        │
│        │                           │                         │
│        ▼                           ▼                         │
│  local terminal sink         relay connector                 │
│                              - register                      │
│                              - output                        │
│                              - resize                        │
│                              - session_state                 │
└──────────────────────────────────┬───────────────────────────┘
                                   │
                                   ▼
                    ┌────────────────────────────────┐
                    │          relay server          │
                    │  - auth                        │
                    │  - live session registry       │
                    │  - output replay buffer        │
                    │  - unread / seq metadata       │
                    │  - global client update fanout │
                    └───────────────┬────────────────┘
                                    │
                   ┌────────────────┴────────────────┐
                   ▼                                 ▼
            mobile / web client A             mobile / web client B
```

## Major Responsibilities

### `agentunnel`

`agentunnel` is the local supervisor.

It owns:

- launcher resolution
- PTY lifecycle and local terminal raw mode
- fanout of PTY output to the local terminal and relay connector
- forwarding remote input back into the PTY
- forwarding resize metadata
- Codex sidecar management when the launcher is `codex`

### Relay

The relay is a live broker, not durable storage and not a semantic interpreter of terminal content.

It owns:

- client and agent authentication
- current live-session snapshots
- rolling in-memory output history for replay
- unread and sequence metadata
- structured session-state tracking such as `action_required`
- global update fanout for connected clients

The relay does not own:

- session creation beyond registration by an agent
- durable history
- preview rendering
- content interpretation of terminal output

### External Clients

External clients own presentation and local persistence.

They are responsible for:

- listing live sessions
- maintaining a foreground global WebSocket if they want cross-session freshness
- rendering terminal output
- computing or persisting local previews if their product needs them
- optionally sending input into one live session

## Runtime Variants

### Standard Launchers

For `claude` and `gemini`, `agentunnel` runs one PTY child:

```text
agentunnel
└─ PTY child process
```

### Codex

For `codex`, `agentunnel` supervises two children:

```text
agentunnel
├─ codex app-server --listen ws://127.0.0.1:0
└─ codex --remote ws://127.0.0.1:<dynamic-port> ...
```

The app-server provides structured runtime state. `agentunnel` monitors it and emits `session_state` updates without parsing PTY text.

## Main Data Flows

### Output

```text
PTY output
→ session hub
→ local terminal sink
→ relay connector
→ relay registry
→ retained output history + seq/unread update
→ global client updates
```

### Input

```text
client input frame
→ relay
→ owning agent websocket
→ agentunnel connector
→ PTY stdin
```

### Resize

```text
local PTY resize
→ relay connector resize frame
→ relay current terminal size metadata
→ future output frames carry updated cols / rows
```

### Session State

```text
Codex app-server thread state
→ codexapp monitor
→ connector session_state frame
→ relay live session snapshot
→ global client update stream
```

This state path is intentionally separate from terminal output and replay.

## Client Model

The preferred client topology is:

- one foreground `GET /api/updates/ws` connection for all sessions
- `GET /api/sessions` for bootstrap and reconnect correction
- `GET /api/sessions/:id/history` for per-session catch-up

This keeps list freshness, terminal output, and cross-session notifications on one global channel while leaving history catch-up as an HTTP concern.

## Stable Boundaries

The architecture depends on a few long-lived boundaries:

- PTY output is an opaque presentation stream.
- Session state is structured metadata.
- The relay may retain output bytes for replay, but it should not interpret them.
- Relay history is live-only and in-memory.
- External clients may persist richer local state than the relay does.
- Terminal size is agent-owned metadata, not client-owned state.

As long as those boundaries hold, the internal package structure can evolve without changing the mental model.

## Package Map

- `cmd/agentunnel`: local launcher entrypoint
- `session/`: PTY ownership, local terminal handling, output/input hub
- `connector/`: outbound relay connection and message forwarding
- `codexapp/`: Codex app-server lifecycle and structured waiting-state monitor
- `cmd/relay`: relay entrypoint
- `relay/`: live session registry and HTTP / WebSocket handlers
- `protocol/`: shared wire types

## Related Documents

- [docs/protocol.md](./protocol.md)
- [docs/codex-action-required.md](./codex-action-required.md)
