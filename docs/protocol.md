# Agent Tunnel Relay Protocol

This document specifies the WebSocket protocol used between agent-tunnel components. It is intended for developers building native clients (iOS, Android, or other platforms) that connect to the relay server.

## Overview

The relay server is a WebSocket broker with two roles:

- **Agent** — a local `agentunnel` process that owns a PTY session. It connects to the relay, registers the session, and streams terminal output. It receives input and resize commands from attached browsers.
- **Browser** — a web or native client that lists live sessions and attaches to one for viewing and optional interaction.

All WebSocket communication uses JSON text frames. Binary data (terminal I/O) is base64-encoded within JSON fields.

## Endpoints

| Endpoint | Role | Auth | Protocol |
|----------|------|------|----------|
| `GET /api/sessions` | Browser | Basic Auth | HTTP JSON |
| `GET /api/sessions/:id/ws` | Browser | Basic Auth | WebSocket |
| `GET /agent/ws` | Agent | Bearer token | WebSocket |
| `GET /healthz` | Any | None | HTTP text |

## Authentication

### Browser — Basic Auth

All browser-facing endpoints require HTTP Basic Authentication.

```
Authorization: Basic base64(username:password)
```

The credentials are configured on the relay server via `AGENTUNNEL_BASIC_USER` and `AGENTUNNEL_BASIC_PASSWORD` environment variables.

### Agent — Bearer Token

The agent WebSocket endpoint requires a bearer token in the `Authorization` header.

```
Authorization: Bearer <token>
```

The token is configured on the relay server via `AGENTUNNEL_AGENT_TOKEN` and on the agent via `AGENTUNNEL_RELAY_TOKEN`.

## REST API

### List Live Sessions

```
GET /api/sessions
Authorization: Basic base64(username:password)
```

Returns a JSON array of `SessionInfo` objects, sorted by most recently active:

```json
[
  {
    "session_id": "1743667200000000000",
    "launcher": "claude",
    "label": "api-fix",
    "cwd": "/home/user/project",
    "command_preview": "claude --resume",
    "started_at": "2026-04-03T10:00:00Z",
    "last_preview": "Running tests...",
    "last_active_at": "2026-04-03T10:05:30Z"
  }
]
```

**SessionInfo fields:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `session_id` | string | yes | Unique session identifier (nanosecond timestamp) |
| `launcher` | string | yes | Agent CLI name: `claude`, `codex`, or `gemini` |
| `label` | string | no | Optional human-readable label set by the operator |
| `cwd` | string | yes | Working directory where the agent was launched |
| `command_preview` | string | yes | Command line preview (e.g., `claude --resume`) |
| `started_at` | string | yes | ISO 8601 UTC timestamp of session start |
| `last_preview` | string | no | Rolling text preview of recent terminal output (ANSI stripped) |
| `last_active_at` | string | no | ISO 8601 UTC timestamp of last output activity |

### Health Check

```
GET /healthz
```

Returns `200 OK` with body `ok`. No authentication required.

## Browser WebSocket

### Connect

```
GET /api/sessions/:id/ws
Authorization: Basic base64(username:password)
```

Upgrades to a WebSocket connection attached to the specified session. The relay enforces a same-origin check on the WebSocket upgrade request.

If the session does not exist, the server returns `404 Not Found` before upgrade.

### Frames: Server → Browser

**Output frame** — terminal bytes from the agent's PTY:

```json
{
  "type": "output",
  "data": "SGVsbG8gV29ybGQ="
}
```

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | Always `"output"` |
| `data` | string | Standard base64-encoded raw PTY bytes |

### Frames: Browser → Server

**Input frame** — keystrokes to forward to the agent's PTY:

```json
{
  "type": "input",
  "data": "bHMK"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | Always `"input"` |
| `data` | string | Standard base64-encoded bytes (user keystrokes) |

**Resize frame** — terminal dimension change:

```json
{
  "type": "resize",
  "cols": 120,
  "rows": 40
}
```

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | Always `"resize"` |
| `cols` | integer | New terminal width in columns |
| `rows` | integer | New terminal height in rows |

## Agent WebSocket

### Connect

```
GET /agent/ws
Authorization: Bearer <token>
```

Upgrades to a WebSocket connection. The agent must send a `register` frame as its first message.

The relay sets a read limit of 1 MB per message and a read deadline of 30 seconds. The relay sends WebSocket ping frames every 10 seconds; the agent must respond with pong frames (handled automatically by most WebSocket libraries). Each pong resets the read deadline.

### Frames: Agent → Relay

**Register frame** — must be the first message after connection:

```json
{
  "type": "register",
  "session": {
    "session_id": "1743667200000000000",
    "launcher": "claude",
    "label": "api-fix",
    "cwd": "/home/user/project",
    "command_preview": "claude --resume",
    "started_at": "2026-04-03T10:00:00Z"
  }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | Always `"register"` |
| `session` | SessionInfo | Session metadata (see SessionInfo fields above) |

The `session` object must include at minimum: `session_id`, `launcher`, `cwd`, `command_preview`, and `started_at`. The `label` field is optional.

**Output frame** — terminal bytes from the PTY:

```json
{
  "type": "output",
  "data": "SGVsbG8gV29ybGQ="
}
```

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | Always `"output"` |
| `data` | string | Standard base64-encoded raw PTY bytes |

### Frames: Relay → Agent

**Input frame** — keystrokes forwarded from an attached browser:

```json
{
  "type": "input",
  "data": "bHMK"
}
```

**Resize frame** — terminal resize from an attached browser:

```json
{
  "type": "resize",
  "cols": 120,
  "rows": 40
}
```

These frames use the same schema as the browser frames described above.

## Connection Lifecycle

### Agent Lifecycle

```
1. Open WebSocket to /agent/ws
   Headers: Authorization: Bearer <token>

2. Send register frame with session metadata
   ← Relay adds session to live registry

3. Stream output frames as PTY produces bytes
   ← Relay broadcasts to attached browsers
   ← Relay extracts rolling preview for dashboard

4. Receive input frames from relay
   → Forward decoded bytes to PTY stdin

5. Receive resize frames from relay
   → Apply to PTY dimensions

6. Relay sends WebSocket pings every 10s
   → Agent responds with pong (automatic in most libraries)
   → Each pong resets the 30s read deadline

7. On disconnect (network drop, agent exit, etc.)
   ← Relay removes session from registry
   ← Attached browsers stop receiving output
```

### Browser Lifecycle

```
1. GET /api/sessions with Basic Auth
   ← Receive list of live sessions

2. Open WebSocket to /api/sessions/:id/ws
   Headers: Authorization: Basic base64(user:pass)

3. Receive output frames
   → Decode base64 → write raw bytes to terminal emulator

4. Optionally send input frames
   → Encode keystrokes as base64 → send as input frame

5. Send resize frame when terminal dimensions change

6. On WebSocket close
   → Return to session list or show disconnected state
```

## Data Encoding

All `data` fields use standard base64 encoding (RFC 4648, alphabet `A-Z a-z 0-9 + /`, padding with `=`). This is **not** base64url.

To send the string `ls\n` as input:
1. UTF-8 encode: `[0x6C, 0x73, 0x0A]`
2. Base64 encode: `bHMK`
3. Send: `{"type":"input","data":"bHMK"}`

To process received output:
1. Parse JSON, extract `data` field
2. Base64 decode to raw bytes
3. Write bytes directly to terminal emulator

## Mobile Implementation Notes

### Terminal Rendering

Recommended terminal emulator libraries for native platforms:

- **iOS**: [SwiftTerm](https://github.com/migueldeicaza/SwiftTerm) — a mature xterm-compatible terminal emulator for Swift
- **Android**: [TerminalView](https://github.com/niclas-pfeifer/TerminalView) or the terminal emulator component from [Termux](https://github.com/termux/termux-app)

These libraries accept raw byte streams, which is exactly what the base64-decoded `output` data provides.

### Read-Only by Default

The relay UI is designed primarily for monitoring. Implement an explicit toggle (similar to the web UI's "Read-only" / "Input on" chip) before forwarding input frames. This prevents accidental keystrokes from reaching the agent.

### Reconnection

The relay maintains only live, in-memory state. If the agent disconnects, the session disappears from the registry immediately. There is no session persistence or replay.

Mobile clients should:
- Handle WebSocket `onclose` gracefully
- Return to the session list when a session WebSocket closes
- Re-fetch `/api/sessions` to get updated session list
- Not attempt automatic reconnection to a specific session (the session may no longer exist)

### Input Filtering

Browser terminal emulators (like xterm.js) can generate automatic response sequences (cursor position reports, device attribute responses) that should NOT be forwarded to the PTY. If your terminal emulator generates these, filter them before sending input frames. Common patterns to filter:

| Sequence | Pattern | Description |
|----------|---------|-------------|
| CPR | `\x1b[<digits>;<digits>R` | Cursor Position Report |
| DECXCPR | `\x1b[?<digits>;<digits>R` | Extended Cursor Position Report |
| DA1 | `\x1b[?<digits>(;<digits>)*c` | Primary Device Attributes |
| DA2 | `\x1b[><digits>(;<digits>)*c` | Secondary Device Attributes |
| OSC color | `\x1b]1<0|1>;rgb:...<ST>` | OSC color query response |

### WebSocket Libraries

Any standard WebSocket library works. The protocol uses only text frames with JSON payloads. Recommended:

- **iOS**: `URLSessionWebSocketTask` (built-in) or [Starscream](https://github.com/niclas-pfeifer/Starscream)
- **Android**: [OkHttp WebSocket](https://square.github.io/okhttp/) (built-in support)
- **Cross-platform**: Any library that supports text frames, custom headers (for auth), and automatic pong responses
