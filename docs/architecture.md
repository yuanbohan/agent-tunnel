# Agent Tunnel Architecture

This document explains the stable shape of the system.

## System Shape

`agentunnel` owns the real local agent process and its PTY. Every supported launcher follows the same path: one local PTY child, one relay connector, no launcher-specific sidecar. The relay exposes authenticated APIs so external clients can observe or interact with that live session, proxy history reads from the owning agent, and send input back to the PTY.

The local terminal is the primary and most complete view of the PTY session. The remote path is intentionally lighter weight: `GET /api/updates/ws` is a best-effort live channel, while `GET /api/sessions/:id/frames` is the standard recovery path for agent-owned output while the session is `connected`.

`agentunnel` treats relay availability in two phases:

- startup gating: it gives relay registration a bounded first chance before entering the local terminal session
- post-startup continuity: once the local session has begun, relay outages only affect remote visibility and control; local terminal work continues while the connector retries in the background

On macOS, after startup reaches a live local session, `agentunnel` also attempts idle sleep prevention for the lifetime of the `agentunnel` process. That host-level helper is best-effort: startup continues if it cannot be enabled, but the startup banner reports the result.

```text
local machine
┌──────────────────────────────────────────────────────────────┐
│                          agentunnel                          │
│                                                              │
│  launcher resolve                                            │
│        │                                                     │
│        ▼                                                     │
│  local runtime                                               │
│  - claude / codex / gemini PTY child                         │
│        │                                                     │
│        ▼                                                     │
│     session hub                                              │
│  - PTY output fanout                                         │
│  - PTY input routing                                         │
│  - PTY size tracking                                          │
│        │                           │                         │
│        ▼                           ▼                         │
│  local terminal sink         relay connector                 │
│                              - register                      │
│                              - output                        │
└──────────────────────────────────┬───────────────────────────┘
                                   │
                                   ▼
                    ┌────────────────────────────────┐
                    │          relay server          │
                    │  - auth                        │
                    │  - live session registry       │
                    │  - reconnecting grace state    │
                    │  - proxied history requests    │
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
- macOS idle sleep-prevention helper lifecycle
- startup relay wait and background reconnect policy
- fanout of PTY output to the local terminal and relay connector
- bounded in-memory output history for the lifetime of the running session
- agent-authored `seq`, `ts`, and `latest_seq` metadata
- forwarding remote input back into the PTY
- translating structured remote key input into PTY bytes
- attaching current terminal `cols` and `rows` to every uploaded output frame
- low-noise local relay-status presentation during reconnecting periods

### Relay

The relay is a live broker, not durable storage and not a semantic interpreter of terminal content.

It owns:

- client and agent authentication
- current live-session snapshots
- `connected` / `reconnecting` session lifecycle state with a short reconnect grace window
- proxied `GET /api/sessions/:id/frames` requests while the owning agent is connected
- global update fanout for connected clients

The relay does not own:

- session creation beyond registration by an agent
- durable history
- session-history authority
- preview rendering
- content interpretation of terminal output
- end-to-end guarantees that a remote client observed every PTY byte

## Main Data Flows

### Output

```text
PTY output
→ session hub
→ local terminal sink
→ relay connector
→ agent-side history buffer + seq / ts assignment
→ best-effort enqueue toward relay
→ output frame with current cols / rows
→ relay registry
→ global client updates
```

`seq` begins at the PTY owner when `agentunnel` appends an output frame to its local history buffer. It is ordering metadata for agent-owned history, not proof of complete delivery from the PTY to every remote client.

### Input

```text
client input frame
→ relay
→ owning agent websocket
→ agentunnel connector
→ structured input translation:
     - `input_text { submit: false }` -> UTF-8 text bytes
     - `input_text { submit: true }` -> UTF-8 text bytes, then trailing `\r`, as one serialized submit operation
     - supported `input_key` events -> PTY key bytes
→ PTY stdin
```

### Startup and Reconnect

```text
agentunnel launch
→ connector starts trying `/agent/ws`
→ if registration succeeds during the startup wait window:
     local session starts in connected mode
→ if the startup wait window expires first:
     local session still starts
     connector keeps retrying in the background
→ if a later relay disconnect happens:
     local PTY session continues uninterrupted
     connector retries with backoff until connected again
```

### Size Metadata

```text
local PTY resize
→ session hub current size update
→ future output frames carry updated cols / rows
```

### Remote Recovery

```text
client loses /api/updates/ws
→ client reconnects to /api/updates/ws
→ client requests /api/sessions/:id/frames as needed
→ relay proxies the request to the connected owning agent
→ agent returns only the currently retained in-memory frames for that still-live session
```

This recovery path is standard for clients, but it is still bounded by live-only agent retention rather than a durable transcript. During the relay grace window, a session may remain listed as `reconnecting`, but `/frames` and remote input stay unavailable until the agent reconnects.

## Package Map

- `cmd/agentunnel`: local launcher entrypoint
- `session/`: PTY ownership, local terminal handling, output/input hub
- `connector/`: outbound relay connection and message forwarding
- `cmd/relay`: relay entrypoint
- `relay/`: live session registry and HTTP / WebSocket handlers
- `protocol/`: shared wire types

## Related Documents

- [docs/protocol.md](./protocol.md)
