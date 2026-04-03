# Agent Tunnel Relay Protocol

This document specifies the WebSocket protocol used between agent-tunnel components. It is intended for developers building native clients (iOS, Android, or other platforms) that connect to the relay server.

## Overview

The relay server is a WebSocket broker with two roles:

- **Agent** — a local `agentunnel` process that owns a PTY session. It connects to the relay, registers the session, and streams terminal output and resize events. It receives input from attached browsers.
- **Browser** — a web or native client that lists live sessions and attaches to one for viewing and optional interaction. Browsers never control the terminal size; resize flows one-way from agent to browser.

All WebSocket communication uses JSON text frames. Binary data (terminal I/O) is base64-encoded within JSON fields.

## Endpoints

| Endpoint | Role | Auth | Protocol |
|----------|------|------|----------|
| `GET /api/sessions` | Browser | Basic Auth | HTTP JSON |
| `GET /api/sessions/:id/history` | Browser | Basic Auth | HTTP JSON |
| `POST /api/sessions/:id/read` | Browser | Basic Auth | HTTP JSON |
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
    "last_active_at": "2026-04-03T10:05:30Z",
    "latest_seq": 42,
    "last_read_seq": 37,
    "unread_count": 5,
    "preview_seq": 42,
    "preview_b64": "SGVsbG8gZnJvbSB0aGUgbGF0ZXN0IGNo..."
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
| `latest_seq` | integer | yes | Latest retained output-frame sequence number for this live session |
| `last_read_seq` | integer | yes | Shared read marker for this live session |
| `unread_count` | integer | yes | `latest_seq - last_read_seq` |
| `preview_seq` | integer | yes | Sequence number of the latest preview frame |
| `preview_b64` | string | no | Latest raw output frame for dashboard mini-terminal rendering |

Notes:
- The dashboard currently renders `codex` sessions with OpenAI branding, but the wire-level launcher value remains `codex`.
- `last_preview` remains available as a compatibility field, but the browser UI now prefers `preview_b64`.

### Fetch Session History

```
GET /api/sessions/:id/history?before=<seq>&after=<seq>&limit=<n>&max_bytes=<n>
Authorization: Basic base64(username:password)
```

Returns one page of retained live-session history:

```json
{
  "messages": [
    { "seq": 38, "data_b64": "..." },
    { "seq": 39, "data_b64": "..." },
    { "seq": 40, "data_b64": "..." }
  ],
  "has_more": true,
  "latest_seq": 42,
  "last_read_seq": 37
}
```

Query parameters:

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `before` | integer | no | Return frames with `seq < before` |
| `after` | integer | no | Return frames with `seq > after` |
| `limit` | integer | no | Target maximum number of frames in the page |
| `max_bytes` | integer | no | Target maximum total raw byte size in the page |

Response fields:

| Field | Type | Description |
|-------|------|-------------|
| `messages` | array | History frames in chronological order |
| `messages[].seq` | integer | Output-frame sequence number |
| `messages[].data_b64` | string | Raw PTY bytes for that frame |
| `has_more` | boolean | Whether more frames are available in the requested direction/window |
| `latest_seq` | integer | Latest live output sequence known to the relay |
| `last_read_seq` | integer | Current shared read marker |

Semantics:
- Omitting both `before` and `after` returns the newest page.
- `before` is used for backward paging in the session detail view.
- `after` is used to bridge frames emitted between initial history fetch and live WebSocket attach.
- If the session does not exist, the relay returns `404 Not Found`.

### Mark Session Read

```
POST /api/sessions/:id/read
Authorization: Basic base64(username:password)
Content-Type: application/json
```

Request body:

```json
{ "seq": 42 }
```

Response:

```json
{
  "session_id": "1743667200000000000",
  "latest_seq": 42,
  "last_read_seq": 42,
  "unread_count": 0
}
```

Semantics:
- The relay clamps the submitted `seq` to the current `latest_seq`.
- Read state is monotonic. Posting a lower `seq` does not move `last_read_seq` backward.

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
  "seq": 42,
  "data": "SGVsbG8gV29ybGQ="
}
```

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | Always `"output"` |
| `seq` | integer | Monotonic output-frame sequence number assigned by the relay |
| `data` | string | Standard base64-encoded raw PTY bytes |

**Resize frame** — PTY dimensions changed on the agent side:

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
| `cols` | integer | Current terminal width in columns |
| `rows` | integer | Current terminal height in rows |

The browser should use this to update its terminal emulator dimensions (e.g., in "scroll" display mode). The browser never sends resize frames — terminal size is owned exclusively by the agent's local terminal.

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

**Resize frame** — PTY dimensions changed (local terminal resized):

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

The relay broadcasts resize frames to all attached browser sinks.

### Frames: Relay → Agent

**Input frame** — keystrokes forwarded from an attached browser:

```json
{
  "type": "input",
  "data": "bHMK"
}
```

This is the only frame type the relay sends to the agent. Resize is one-way: agent → relay → browsers.

## Connection Lifecycle

### Agent Lifecycle

```
1. Open WebSocket to /agent/ws
   Headers: Authorization: Bearer <token>

2. Send register frame with session metadata
   ← Relay adds session to live registry

3. Stream output frames as PTY produces bytes
   ← Relay broadcasts to attached browsers
   ← Relay appends the frame to live in-memory history
   ← Relay updates unread metadata and latest dashboard preview

4. Send resize frames when local terminal resizes
   ← Relay broadcasts to attached browsers

5. Receive input frames from relay
   → Forward decoded bytes to PTY stdin

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
   ← Receive list of live sessions, unread counters, and preview frames

2. GET /api/sessions/:id/history
   ← Receive newest retained history page

3. Open WebSocket to /api/sessions/:id/ws
   Headers: Authorization: Basic base64(user:pass)

4. Receive output frames
   → Decode base64 → write raw bytes to terminal emulator

5. Receive resize frames
   → Update terminal emulator dimensions to match the agent's PTY

6. POST /api/sessions/:id/read
   → Advance the shared read marker after history replay + live attach are active

7. Optionally send input frames
   → Encode keystrokes as base64 → send as input frame

8. On WebSocket close
   → Return to session list or show disconnected state
```

> **Note:** Browsers never send resize frames. Terminal dimensions are owned by the agent's local terminal. The browser can choose how to display the content — either "wrap" mode (fit to browser window) or "scroll" mode (match PTY dimensions exactly).

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

The relay maintains only live, in-memory state. If the agent disconnects, the session disappears from the registry immediately. There is no persistence across relay restarts or disconnected sessions, but live sessions do expose a rolling retained history buffer while they remain online.

Mobile clients should:
- Handle WebSocket `onclose` gracefully
- Return to the session list when a session WebSocket closes
- Re-fetch `/api/sessions` to get updated session list
- Use `/api/sessions/:id/history` for initial replay before live attach if they want parity with the web UI
- Not attempt automatic reconnection to a specific session if the relay reports `404` (the session may no longer exist)

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
