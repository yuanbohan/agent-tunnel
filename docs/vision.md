# Agent Tunnel Vision

## Purpose

Control locally running terminal agents from a remote device without moving PTY ownership off the developer machine.

The laptop remains the place where the real agent process runs. A relay exposes that live session to authenticated external clients, with the initial focus on a mobile app that can:

- discover live sessions
- replay recent output
- attach to the live stream
- send explicit user input when needed

## Product Shape

The product now has two clear runtime roles:

- `agentunnel`
  - launches `claude`, `codex`, or `gemini` locally
  - owns the PTY
  - keeps the local terminal interactive
  - connects outward to the relay
- `relay`
  - authenticates agents and clients
  - tracks only live sessions
  - retains a short in-memory output buffer per live session
  - exposes list/history/read/live-attach APIs

The relay is not the system of record. External clients are expected to persist any history they need locally.

## Current Direction

The near-term product direction is:

- relay-first, not localhost-first
- API-first, not bundled-web-UI-first
- mobile-client-led, not browser-dashboard-led
- live-session-centric, not durable-server-storage-centric

That means:

- no embedded frontend in the relay
- no separate resize history stream for clients
- one `after` cursor for incremental output sync
- per-output `cols` and `rows` for faithful terminal replay

## Core User Experience

1. Start an agent locally with `agentunnel`.
2. The session registers with the relay.
3. The mobile client lists live sessions through `GET /api/sessions`.
4. On first open, the client calls `GET /api/sessions/:id/history?after=0`.
5. The client persists output locally and stores the highest `seq`.
6. The client attaches with `GET /api/sessions/:id/ws?after=<stored-seq>`.
7. The relay replays retained output newer than `after`, then streams live output.

This keeps the relay simple while still smoothing over reconnects and late attaches.

## Deliberate Constraints

These are intentional, not temporary accidents:

- PTY ownership stays local.
- Relay state is in-memory only.
- Session loss on relay restart or agent disconnect is acceptable.
- Clients never control terminal size.
- Clients own long-term history, replay, and presentation.

## Why This Shape

This architecture fits the actual failure boundaries:

- the laptop is the only trustworthy PTY owner
- the relay is good at fanout, auth, and short-lived buffering
- the mobile client is best placed to own durable local history and replay UX

It also keeps the protocol narrow:

- one retained message type: output
- one sync cursor: `seq`
- one history fetch shape: `after`

## Next Product Milestones

### 1. Solid mobile client integration

- local output persistence
- replay correctness using per-output `cols` / `rows`
- reliable live reconnect with `after`

### 2. Better operator workflow

- clearer session labels
- stronger session metadata in list responses
- better unread/read-state handling

### 3. Optional future durability

Only if product needs demand it:

- server-side persistence
- session resurrection across relay restarts
- multi-device sync of read state or cached history

Those are later expansions, not current assumptions.
