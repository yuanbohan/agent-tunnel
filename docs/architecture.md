# Agent Tunnel Architecture

This document explains the stable shape of the system.

## System Shape

`agentunnel` owns the real local agent process and its PTY. Every supported launcher follows the same path: one local PTY child, one relay connector, no launcher-specific sidecar. The relay exposes authenticated APIs so external clients can observe or interact with that live session, replay retained output frames, and send input back to the PTY.

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
│  - resize propagation                                        │
│        │                           │                         │
│        ▼                           ▼                         │
│  local terminal sink         relay connector                 │
│                              - register                      │
│                              - output                        │
│                              - resize                        │
└──────────────────────────────────┬───────────────────────────┘
                                   │
                                   ▼
                    ┌────────────────────────────────┐
                    │          relay server          │
                    │  - auth                        │
                    │  - live session registry       │
                    │  - output frame buffer         │
                    │  - output seq metadata         │
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

### Relay

The relay is a live broker, not durable storage and not a semantic interpreter of terminal content.

It owns:

- client and agent authentication
- current live-session snapshots
- rolling in-memory output frames for replay through `GET /api/sessions/:id/frames`
- output sequence metadata
- global update fanout for connected clients

The relay does not own:

- session creation beyond registration by an agent
- durable history
- preview rendering
- content interpretation of terminal output

## Main Data Flows

### Output

```text
PTY output
→ session hub
→ local terminal sink
→ relay connector
→ relay registry
→ retained output frames + seq assignment
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

## Package Map

- `cmd/agentunnel`: local launcher entrypoint
- `session/`: PTY ownership, local terminal handling, output/input hub
- `connector/`: outbound relay connection and message forwarding
- `cmd/relay`: relay entrypoint
- `relay/`: live session registry and HTTP / WebSocket handlers
- `protocol/`: shared wire types

## Related Documents

- [docs/protocol.md](./protocol.md)
