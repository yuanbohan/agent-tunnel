# Agent Tunnel Architecture

This document explains the stable shape of the system.

## System Shape

`agentunnel` owns the real local agent process, its PTY, and the current session transcript. Every supported launcher follows the same path: one local PTY child, one session hub, one outbound relay connector, and no launcher-specific sidecar.

The relay exposes authenticated APIs so external clients can observe live output, send input, list live sessions, and fetch replay for a connected session. The relay is not the history authority. It proxies replay reads to the owning agent.

The local terminal is the primary and most complete view of the PTY session. The remote path is intentionally lighter weight: `GET /api/updates/ws` is a best-effort live channel, while `GET /api/sessions/:id/frames` is the standard recovery path for the agent-owned transcript while the session is `connected`.

`agentunnel` treats relay availability in two phases:

- startup gating: it gives relay registration a bounded first chance before entering the local terminal session
- post-startup continuity: once the local session has begun, relay outages only affect remote visibility and control; local terminal work continues while the connector retries in the background

On macOS, after startup reaches a live local session, `agentunnel` also attempts idle sleep prevention for the lifetime of the `agentunnel` process. That host-level helper is best-effort: startup continues if it cannot be enabled, but the startup banner reports the result.

```text
local machine
┌─────────────────────────────────────────────────────────────────────┐
│                             agentunnel                              │
│                                                                     │
│  launcher resolve                                                   │
│        │                                                            │
│        ▼                                                            │
│  local runtime                                                      │
│  - claude / codex / gemini PTY child                                │
│        │                                                            │
│        ▼                                                            │
│     session hub                                                     │
│  - PTY output fanout                                                │
│  - PTY input routing                                                │
│  - PTY size tracking                                                │
│        │                           │                                │
│        ▼                           ▼                                │
│  local terminal sink         relay connector                        │
│                              - history buffer ownership             │
│                              - seq / ts / latest_seq authorship     │
│                              - register / output / history_response │
└───────────────────────────────────┬─────────────────────────────────┘
                                    │
                                    ▼
                     ┌────────────────────────────────────┐
                     │            relay server            │
                     │  - auth                            │
                     │  - live session registry           │
                     │  - reconnect grace state           │
                     │  - pending history request waiters │
                     │  - /frames proxy                   │
                     │  - global client update fanout     │
                     └─────────────────┬──────────────────┘
                                       │
                    ┌──────────────────┴──────────────────┐
                    ▼                                     ▼
             mobile / web client A                 mobile / web client B
```

## Major Responsibilities

### `agentunnel`

`agentunnel` is the PTY owner and the history authority for one running session.

It owns:

- launcher resolution
- PTY lifecycle and local terminal raw mode
- macOS idle sleep-prevention helper lifecycle
- startup relay wait and background reconnect policy
- fanout of PTY output to the local terminal and relay connector
- bounded in-memory output history for the lifetime of the running session
- agent-authored `seq`, `ts`, `last_active_at`, and `latest_seq` metadata
- forwarding remote input back into the PTY
- translating structured remote key input into PTY bytes
- attaching current terminal `cols` and `rows` to every uploaded output frame
- answering proxied `history_request` messages with snapshots of its local history buffer

### Relay

The relay is a live broker, not durable storage and not a semantic interpreter of terminal content.

It owns:

- client and agent authentication
- current live-session snapshots
- `connected` / `reconnecting` session lifecycle state with a short reconnect grace window
- the owner websocket for each live session
- pending `/frames` request bookkeeping keyed by relay-issued `request_id`
- proxied `GET /api/sessions/:id/frames` requests while the owning agent is connected
- global update fanout for connected clients

The relay does not own:

- session creation beyond registration by an agent
- durable history
- the frame array backing replay
- session-history authority
- preview rendering
- content interpretation of terminal output
- end-to-end guarantees that a remote client observed every PTY byte

### Client

The client is responsible for combining best-effort live output with replay recovery.

It should:

- use `GET /api/updates/ws` as the foreground live channel
- use `GET /api/sessions/:id/frames` to recover missed transcript while the session is `connected`
- use `GET /api/sessions` to discover `connected` versus `reconnecting`
- treat the replay data as terminal transcript, not as an exact input log

## How The Agent Maintains Frames

The transcript is maintained entirely on the agent side.

1. PTY output enters `session.Hub`.
2. `connector.WriteOutput()` reads the current terminal size from the hub and appends the bytes into `session.HistoryBuffer`.
3. `HistoryBuffer.AppendOutput()` creates a `ReplayFrame` with the next strictly increasing `seq`, the current UTC `ts`, and the current `cols` / `rows`.
4. The history buffer keeps those replay frames in memory under a byte budget and evicts the oldest frames when necessary.
5. The connector updates the session snapshot metadata it will advertise to the relay, including `latest_seq` and `last_active_at`.
6. The connector emits the same frame as a live `output` message over `/agent/ws`.

This means live output and replay are two views of the same agent-authored frame model. The relay does not re-sequence or timestamp frames on the way through.

The retained transcript is output-only. Input may appear inside replay only when the terminal application echoes it back as output.

## Remote Input Flow

Remote input still flows through the relay, but translation into PTY bytes remains agent-owned.

```text
client input frame
→ relay
→ owning agent websocket
→ agentunnel connector
→ structured input translation:
     - input_text { submit: false } -> UTF-8 text bytes
     - input_text { submit: true } -> UTF-8 text bytes, then trailing \r, as one serialized submit operation
     - supported input_key events -> PTY key bytes
→ PTY stdin
```

This keeps terminal behavior close to the PTY owner and avoids embedding terminal emulation inside the relay.

## How The Relay Proxies History To Mobile

`GET /api/sessions/:id/frames` is an HTTP proxy into the owning agent's in-memory history buffer.

1. A mobile client calls `GET /api/sessions/:id/frames` with optional inclusive `from` and `to`.
2. The relay authenticates the request, validates query bounds, and looks up the live session in the registry.
3. If the session is missing, the relay returns `404 session_not_found`. If the session is currently `reconnecting`, the relay returns `409 session_reconnecting`.
4. For a `connected` session, the registry allocates a pending waiter keyed by a relay-issued `request_id` and binds it to the current owner websocket.
5. The relay sends `history_request { request_id, from, to }` over `/agent/ws`.
6. `agentunnel` snapshots its local `HistoryBuffer` for the requested bounds and replies with `history_response { request_id, frames }`.
7. The relay matches the response to the pending waiter and returns the `frames` array as the HTTP response body.
8. If the agent disconnects before replying, the pending request fails and the HTTP request resolves as reconnecting or timeout behavior. If the agent returns malformed replay payloads, the relay returns `502 invalid_agent_response`.

The relay validates request correlation and basic replay-frame shape before returning replay data, but it still relies on the owning agent as the source of truth for transcript contents.

## Live Output And Recovery

The live path and recovery path are intentionally separate.

### Live Path

```text
PTY output
→ session hub
→ local terminal sink
→ connector history append + seq / ts assignment
→ output frame over /agent/ws
→ relay registry metadata update
→ /api/updates/ws fanout
→ clients
```

`GET /api/updates/ws` is a best-effort stream. It is optimized for foreground observation, not guaranteed delivery.

### Recovery Path

```text
client loses /api/updates/ws
→ client reconnects /api/updates/ws
→ client fetches /api/sessions/:id/frames as needed
→ relay proxies history_request to connected owning agent
→ agent snapshots local history buffer
→ relay returns replay frames
```

Clients should think of `/frames` as "ask the current agent what transcript it still has", not "read history stored inside the relay."

## Startup And Relay Continuity

Relay availability has a bounded effect on startup, but not on the already-running local terminal session.

```text
agentunnel launch
→ connector starts trying /agent/ws
→ if registration succeeds during the startup wait window:
     local session starts in connected mode
→ if the startup wait window expires first:
     local session still starts
     connector keeps retrying in the background
→ if a later relay disconnect happens:
     local PTY session continues uninterrupted
     connector retries with backoff until connected again
```

## Reconnect Lifecycle

The session lifecycle is centered on one running agent process.

1. The agent registers over `/agent/ws`; the relay marks the session `connected`.
2. If the agent websocket drops, the relay keeps the session in the registry as `reconnecting` for a bounded grace window.
3. During `reconnecting`, the session remains discoverable in `GET /api/sessions`, but `/frames`, live output, and remote input are unavailable.
4. If the same running agent reconnects with the same `session_id`, it re-registers, the relay swaps ownership back to the new websocket, and the session becomes `connected` again without changing transcript authority.
5. If the reconnect grace window expires, the relay removes the session and emits `session_removed` to live clients.

Closing the agent process ends the session. A later agent launch starts a different session with a different `session_id` and a fresh in-memory history buffer.

## Package Map

- `cmd/agentunnel`: local launcher entrypoint
- `session/`: PTY ownership, local terminal handling, output/input hub, and history buffer
- `connector/`: outbound relay connection, live output upload, and history-request handling
- `cmd/relay`: relay entrypoint
- `relay/`: live session registry, reconnect state, pending replay bookkeeping, and HTTP / WebSocket handlers
- `protocol/`: shared wire types

## Related Documents

- [docs/protocol.md](./protocol.md)
