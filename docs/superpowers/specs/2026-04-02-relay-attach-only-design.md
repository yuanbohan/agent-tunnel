# Relay Attach-Only Design

## Summary

Extend `agentunnel` from a localhost-only shared session into a remote, mobile-friendly relay system.

The first remote release remains intentionally narrow:

- `attach-only`: sessions are started locally on the Mac, never remotely from the phone
- one live local `agentunnel` process equals one live relay session
- each local session connects directly to the relay; there is no local daemon or coordinator
- single-user only
- browser access is protected by Basic Auth
- agent-to-relay registration is protected by a static agent token
- relay state is in-memory only; there is no durable session history

The product goal is specific: when away from the desk, the user can open a mobile web UI, see which locally-running agent sessions are active, inspect what each one is doing, and optionally type into a chosen session after explicitly enabling input in the browser.

## Goals

- Let multiple local `agentunnel` sessions appear in one remote dashboard
- Keep the local terminal as the primary interactive surface on the Mac
- Allow the phone browser to inspect live session output over the internet
- Allow the phone browser to send input to a live session after an explicit per-browser toggle
- Make the mobile dashboard good enough to distinguish sessions quickly
- Reuse the existing PTY hub, terminal protocol, and web terminal work where possible

## Non-Goals

- Multi-user or tenant isolation
- User registration, JWTs, cookies, or account management
- Starting, stopping, or restarting sessions from the phone
- A long-running local control daemon
- Offline session history or persistent session recovery
- Input ownership, locking, or exclusive control semantics
- Semantic summaries derived from Claude/Codex/Gemini-specific output parsing
- Desktop-first dashboard design

## User Experience

### Local startup

The user still starts sessions on the Mac terminal:

```bash
agentunnel codex --label api-fix
agentunnel gemini --label docs
```

Each process:

1. launches the requested CLI in a PTY
2. keeps the local terminal fully interactive
3. opens an outbound WebSocket to the relay
4. registers the session metadata with the relay
5. streams PTY output to the relay while accepting remote input and resize messages

The local terminal remains authoritative for the "native tool UX" requirement. The relay path adds remote visibility and optional input, not a replacement control plane.

### Mobile dashboard

The browser opens the relay root page after passing Basic Auth. The homepage shows only live sessions.

Each session card emphasizes the fields needed to identify it quickly on a phone:

- launcher icon
- optional label, shown prominently when present
- launcher name
- command preview
- working-directory clue
- most recent text preview extracted from raw terminal output
- recent activity time

The dashboard is mobile-first, compact, and information-dense. It should avoid decorative whitespace and avoid pushing secondary metadata ahead of the session-identifying fields.

### Session detail

Selecting a card opens a terminal-first mobile detail page.

Requirements:

- the terminal viewport is the dominant part of the screen
- the header is intentionally thin
- metadata such as command and working directory is secondary and may be collapsed
- browser input is disabled by default
- the input control is a compact state chip, not a wide verb button

The state chip toggles between:

- `Read-only`
- `Input on`

This is only a browser-side input gate to prevent accidental typing on mobile. It is not a lock and does not disable the local terminal.

## Chosen Architecture

The chosen approach is a thin relay with an in-memory live session registry.

### High-level topology

```
 Your Mac                           VPS Relay                       Phone Browser
┌────────────────┐                ┌────────────────┐               ┌──────────────┐
│ agentunnel A   │── outbound WS ─> session registry│── HTTP/API ─> dashboard    │
│ codex + PTY    │                │ terminal router │── WS attach ─> session page │
└────────────────┘                │ preview cache   │               └──────────────┘
                                  │ Basic Auth gate │
┌────────────────┐                └────────────────┘
│ agentunnel B   │── outbound WS ────────────────────────────────────────────────>
│ gemini + PTY   │
└────────────────┘
```

### Why this shape

This design matches the product scope without introducing the wrong complexity:

- no local daemon
- no remote process creation
- no persistent control plane
- no auth system beyond minimum viable protection

Each live `agentunnel` process already owns a PTY-backed session. The relay simply exposes those sessions remotely, adds a live registry for discovery, and routes remote browser attaches.

## Components

### 1. Local `agentunnel`

`agentunnel` remains the owner of:

- the launcher process
- the PTY
- the local terminal adapter
- the session hub

For remote mode, it gains a relay connector that:

- authenticates with a static agent token
- registers session metadata
- forwards PTY output to the relay
- accepts relay-delivered `input` and `resize` messages
- reconnects when the relay disappears temporarily

The local terminal never depends on relay availability.

### 2. Relay server

A new relay binary serves three roles:

- HTTP server for the mobile web UI
- authenticated API and WebSocket endpoint for browsers
- authenticated registration and data endpoint for local sessions

Responsibilities:

- enforce browser Basic Auth
- authenticate local sessions with a static agent token
- maintain an in-memory registry of live sessions
- maintain per-session browser sinks
- route browser input to the correct local session
- route local PTY output to attached browsers
- derive a rolling preview and last-active timestamp from raw output

Responsibilities intentionally excluded from v1:

- persistence
- offline history
- remote launch or orchestration
- exclusive control

### 3. In-memory live session registry

The registry stores only the live shape needed by the dashboard and attach flow.

Each record contains:

- `session_id`
- `launcher`
- `label`
- `cwd`
- `command_preview`
- `started_at`
- `last_preview`
- `last_active_at`
- current local-session connection handle
- current browser sinks

If the relay restarts, the registry is rebuilt entirely from reconnecting live sessions.

### 4. Mobile-first web UI

The relay-served web UI has two primary views:

- live session dashboard
- session detail terminal page

The layout constraints are product requirements, not implementation suggestions:

- mobile-first
- compact
- minimal whitespace
- terminal-first on detail pages
- session-identification-first on dashboard cards

## Session Metadata Model

The local `agentunnel` process collects metadata at startup and sends it during registration.

### Required metadata

- `session_id`: generated once per local process lifetime
- `launcher`: one of the supported launchers
- `label`: optional user-provided label
- `cwd`: current working directory or a display-safe preview of it
- `command_preview`: a compact preview of the invoked command
- `started_at`: registration time or process start time

### Session identity rules

- each new local process generates a new random `session_id`
- reconnects from the same process reuse the original `session_id`
- the relay treats `session_id` as the stable identity for that running local session

The dashboard display priority should be:

1. label, when present
2. launcher icon and launcher name
3. command preview
4. working-directory clue
5. recent preview and activity time

## Preview Extraction

The dashboard preview comes from raw terminal output, not from semantic agent integration.

Relay preview extraction should:

1. strip ANSI escape sequences
2. ignore empty or obviously non-text fragments when possible
3. keep the most recent text-like line
4. truncate to a compact dashboard-friendly length

If no good preview can be extracted, the session card still remains valid and useful through:

- label
- launcher
- command preview
- working directory
- last-active time

The system must not fabricate semantic summaries.

## Auth Model

The browser and local session should not share one auth path.

### Browser auth

- transport: HTTPS
- scheme: HTTP Basic Auth
- scope: relay web UI, dashboard API, browser attach WebSocket

This is sufficient for a single-user first release and keeps the browser flow simple.

### Local session auth

- transport: outbound WebSocket over TLS
- scheme: static agent token
- source: environment variable or simple config

The local session presents the token during relay connection setup, for example via `Authorization: Bearer <token>`.

This keeps the agent connection simple and avoids awkward Basic Auth challenge handling in the local client.

## Protocol and API Surface

The relay interface should stay thin and close to the existing protocol model.

### HTTP routes

- `GET /`
  - serves the mobile web UI
- `GET /api/sessions`
  - returns live sessions only
- `GET /api/sessions/:id/ws`
  - browser terminal attach for a specific session
- `GET /healthz`
  - health endpoint

No dedicated `GET /api/sessions/:id` endpoint is required in v1 because the list payload already contains the metadata needed by the detail page header.

### Agent connection

The local session connects to:

```text
GET /agent/ws
```

After authentication, the first control message is:

- `register`

The `register` payload contains:

- `session_id`
- `launcher`
- `label`
- `cwd`
- `command_preview`
- `started_at`

After registration, the same WebSocket remains the single data channel for that session:

- agent -> relay: `output`
- relay -> agent: `input`
- relay -> agent: `resize`

This avoids splitting session ownership across multiple connections.

The relay and local session should also use WebSocket ping/pong or equivalent read deadlines so that dead connections are detected promptly. The live registry should not wait on long TCP timeouts before evicting an unreachable session.

### Browser attach connection

The browser terminal connects to:

```text
GET /api/sessions/:id/ws
```

This WebSocket reuses the existing terminal protocol:

- browser -> relay: `input`
- browser -> relay: `resize`
- relay -> browser: `output`

The browser UI itself enforces the `Read-only` / `Input on` state chip semantics. The relay does not track ownership or exclusivity.

## Data Flow

### Registration flow

1. user runs `agentunnel codex --label api-fix`
2. local process launches the PTY-backed agent
3. local process establishes relay connection
4. local process sends `register` with session metadata
5. relay inserts the session into the live registry
6. dashboard can now render the session card

### Output flow

1. local PTY emits bytes
2. local session hub sends those bytes to the relay
3. relay forwards them to attached browsers
4. relay updates `last_active_at`
5. relay derives and stores `last_preview` when possible

### Browser input flow

1. browser opens the detail page and attaches in read-only mode
2. user toggles the state chip from `Read-only` to `Input on`
3. browser begins sending `input` frames
4. relay forwards `input` frames to the local session
5. local session writes bytes into the PTY

### Resize flow

1. browser terminal resizes
2. browser sends `resize`
3. relay forwards `resize` to the local session
4. local session applies the PTY resize

## Failure Modes and Edge Cases

### Relay restart

- live registry is lost
- browser attaches disconnect
- local sessions reconnect automatically
- reconnecting sessions re-register with the same `session_id`
- dashboard entries reappear after reconnect

### Temporary network loss

- local PTY and local terminal continue normally
- only the remote relay path is degraded
- local session reconnects with backoff
- relay evicts the session after connection close or heartbeat timeout

### Local process exit

- relay removes the session from the live list immediately
- attached browsers are disconnected or redirected back to dashboard

### Multiple browsers on one session

- allowed
- all browsers receive the same output
- each browser independently chooses whether it is `Read-only` or `Input on`
- concurrent browser input is allowed in v1

### Local plus remote concurrent input

- allowed by design
- local terminal always remains active
- the mobile state chip prevents accidental browser input but does not provide ownership

### Duplicate `session_id`

If the relay sees two competing local connections for the same `session_id`, it should keep only one live owner. The simplest rule is last-writer-wins:

- accept the newer authenticated connection
- drop the older connection
- preserve one authoritative registry entry

### Preview extraction failure

Some terminal traffic will not yield a useful preview:

- ANSI-heavy redraws
- spinners
- fragmented cursor-motion output
- binary-like garbage

In those cases, the session still remains visible and attachable without a preview.

## Testing Strategy

### Go tests

- relay registry insert, update, remove
- reconnect and replace-same-session-id behavior
- preview extraction behavior
- browser attach and detach behavior
- auth guard behavior

### Web tests

- dashboard rendering from session-list payloads
- compact mobile session-card rendering
- terminal detail state chip behavior
- read-only versus input-enabled browser behavior

### Integration tests

- local `agentunnel` registers successfully with relay
- dashboard lists the live session
- browser attach receives terminal output
- browser input reaches the PTY only after the UI toggles to `Input on`
- relay restart triggers re-registration and session reappearance

### Manual validation

Manual checks should prioritize mobile usability rather than only protocol correctness:

- live dashboard is readable on a phone
- sessions are distinguishable quickly
- detail page gives most of the vertical space to the terminal
- compact metadata presentation does not bury the important fields

## Rollout Scope

### In scope for v1

- relay binary
- relay connector in local `agentunnel`
- live in-memory session registry
- mobile-first live dashboard
- terminal detail page
- browser Basic Auth
- static token agent auth
- raw-text rolling preview extraction

### Explicitly out of scope for v1

- multi-user support
- persistent storage
- offline history
- remote launch/stop/restart
- local daemon/coordinator
- exclusive control or locking
- semantic session summaries
- push notifications
- sharing or observer roles

## Future Extensions

This design intentionally leaves room for later expansion without contaminating v1:

- multi-user session isolation
- persistent history and offline records
- remote session creation through a local daemon
- richer activity summaries
- explicit control ownership and handoff

Those extensions should remain future work. They are not required to deliver the core product value of "see and optionally type into my own live local sessions from my phone."
