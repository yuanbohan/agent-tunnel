# Agent Tunnel Architecture

This document describes the current system shape for the attach-based protocol.

## System Shape

`tunnel` owns the real local agent process, its PTY, and the authoritative current terminal state for that session. Every supported launcher follows the same path: one local PTY child, one session hub, one headless terminal mirror, one outbound relay connector, and no launcher-specific sidecar.

The relay exposes authenticated APIs so external clients can discover live sessions, attach to one online session, and send structured input. The relay is not the terminal-state authority and it does not retain transcript history.

Protocol-facing timestamps such as `started_at` are Unix timestamps encoded as JSON integers in seconds.

The local terminal is still the primary and most complete view of the PTY session. Remote access is session-scoped: a client attaches to one session, receives a current-screen snapshot, and then receives subsequent live PTY bytes on that same attach.

`tunnel` treats relay availability in two phases:

- startup gating: it gives relay registration a bounded first chance before entering the local terminal session
- post-startup continuity: once the local session has begun, relay outages only affect remote visibility and control; local terminal work continues while the connector retries in the background

```text
local machine
┌──────────────────────────────────────────────────────────────────┐
│                              tunnel                              │
│                                                                  │
│  launcher resolve                                                │
│        │                                                         │
│        ▼                                                         │
│  local runtime                                                   │
│  - claude / codex / gemini PTY child                             │
│        │                                                         │
│        ▼                                                         │
│     session hub                                                  │
│  - PTY output fanout                                             │
│  - PTY input routing                                             │
│  - PTY size tracking                                             │
│        │                    │                     │              │
│        ▼                    ▼                     ▼              │
│  local terminal sink   terminal mirror      relay connector      │
│                         - current screen     - register           │
│                         - snapshot bytes     - resize             │
│                         - live attach fanout - attach routing     │
└────────────────────────────────┬─────────────────────────────────┘
                                 │
                                 ▼
                    ┌───────────────────────────────────┐
                    │           relay server            │
                    │  - auth                           │
                    │  - live session registry          │
                    │  - online session registry        │
                    │  - session attach websocket       │
                    │  - agent/client routing           │
                    └────────────────┬──────────────────┘
                                     │
                   ┌─────────────────┴─────────────────┐
                   ▼                                   ▼
            mobile / web client A               mobile / web client B
```

## Major Responsibilities

### `tunnel`

`tunnel` is the PTY owner and the authority for current terminal state for one running session.

It owns:

- launcher resolution
- PTY lifecycle and local terminal raw mode
- startup relay wait and background reconnect policy
- fanout of PTY output to the local terminal, terminal mirror, and relay connector
- the authoritative headless terminal mirror for the currently visible screen
- session-scoped attach snapshot creation
- forwarding remote input back into the PTY
- translating structured remote key input into PTY bytes
- session-wide resize authority, which continues to follow the local terminal in this phase

### Relay

The relay is a live broker, not durable storage and not a semantic interpreter of terminal content.

It owns:

- client and agent authentication
- current live-session snapshots for discovery
- current online-session discovery and immediate offline removal when the owning agent disconnects
- the owner websocket for each live session
- client attach websockets for online sessions
- routing JSON control messages and client-scoped binary terminal bytes between clients and the owning agent
- closing active attaches promptly when the owning agent disappears

The relay does not own:

- session creation beyond registration by an agent
- transcript history
- terminal emulation
- snapshot generation
- preview rendering
- content interpretation of terminal output
- end-to-end guarantees that a remote client observed every PTY byte

### Client

The client is responsible for rendering a session-scoped attach correctly.

It should:

- use `GET /api/sessions` to discover currently online sessions
- use `GET /api/sessions/:id/attach/ws` to attach to one online session
- when running in a browser, open the attach websocket from the same origin as the relay; native clients may omit `Origin`
- size its terminal emulator from the initial `attached` control message before feeding subsequent binary bytes
- treat binary bytes before `snapshot_done` as snapshot bytes and binary bytes after it as live PTY bytes
- rebuild terminal state from a fresh attach after disconnect instead of assuming transcript replay

## Attach Flow

The remote attach path is:

```text
client opens /api/sessions/:id/attach/ws
→ relay authenticates and checks that the session is online
→ relay allocates relay-scoped client_id
→ relay sends attach_open to the owning agent
→ agent terminal mirror atomically:
     - captures current cols / rows
     - serializes the current visible terminal state
     - registers the attached client for subsequent live bytes
→ relay sends attached { session_id, cols, rows }
→ relay forwards snapshot bytes as binary frames
→ relay sends snapshot_done
→ relay forwards subsequent live PTY bytes as binary frames
```

The critical invariant is gap-free handoff: there must be no byte gap between the snapshot point and the first later live bytes for that attached client.

## Terminal Mirror

The terminal mirror exists to make current-screen recovery precise without transcript replay.

- it is fed from the same PTY output stream seen by the local terminal
- it preserves the currently visible terminal state, not transcript history
- it is the source of snapshot bytes on attach
- it fans out subsequent live bytes to attached clients after the snapshot boundary
- it follows PTY resize updates owned by the local terminal session

The current implementation uses `github.com/gitpod-io/xterm-go`, an xterm-compatible headless engine with serialization support, so the snapshot path can restore alternate screen state, colors, cursor state, and other modern TUI behavior without a hand-written ANSI screen walker.

## Remote Input Flow

Remote input still flows through the relay, but translation into PTY bytes remains agent-owned.

```text
client input message
→ relay attach websocket
→ owning agent websocket
→ tunnel connector
→ structured input translation:
     - input_text { submit: false } -> UTF-8 text bytes
     - input_text { submit: true } -> UTF-8 text bytes, then trailing \r, as one serialized submit operation
     - supported input_key events -> PTY key bytes
→ PTY stdin
```

This keeps terminal behavior close to the PTY owner and avoids embedding terminal emulation inside the relay.

## Resize Flow

PTY size remains local-terminal-owned in this phase.

```text
local terminal resize
→ session hub updates cols / rows
→ local PTY resize
→ terminal mirror updates size
→ connector sends resize metadata to relay
→ relay forwards resize control message to each attached client
→ remote clients resize their terminal emulator
```

Remote clients follow the PTY size. They do not compete to become size authority in this revision.

## Startup And Relay Continuity

Relay availability has a bounded effect on startup, but not on the already-running local terminal session.

```text
tunnel launch
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

1. The agent registers over `/agent/ws`; the session becomes discoverable.
2. Clients may attach only while the session is online.
3. If the agent websocket drops, the relay closes active attaches and removes the session from `GET /api/sessions` immediately.
4. While the agent is offline, attaches and remote input are unavailable because the session is no longer discoverable.
5. If the same running agent reconnects with the same `session_id`, it re-registers and the session becomes discoverable again.

Closing the agent process ends the session. A later agent launch starts a different session with a different `session_id`.

## Package Map

- `cmd/agentunnel`: local `tunnel` entrypoint
- `session/`: PTY ownership, local terminal handling, hub fanout, resize state, and terminal mirror
- `connector/`: outbound relay connection, session registration, attach routing, and resize signaling
- `cmd/relay`: relay entrypoint
- `relay/`: live session registry, attach lifecycle, and HTTP / WebSocket handlers
- `protocol/`: shared attach-oriented wire types

## Related Documents

- [docs/protocol.md](./protocol.md)
- [docs/tui-attach-flow.md](./tui-attach-flow.md)
