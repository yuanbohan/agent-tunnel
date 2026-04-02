# Agent Tunnel Vision

## Purpose

Control AI agents (Claude Code, Codex, Gemini) running on your local machine from a mobile device over the internet.

You start an agent session on your laptop terminal. From your phone -- over WiFi, 4G, or 5G -- you see everything the agent outputs, type commands, answer approval prompts (yes/no, option selection), and keep the agent working while you're away from your desk.

## Current State (v1 -- Localhost Shared Session)

What exists today:

```
┌─────────────────────────────────────────────────┐
│                Your Laptop                       │
│                                                  │
│   agentunnel claude                              │
│   ├── PTY (claude process)                       │
│   ├── Hub (fanout center)                        │
│   ├── Local Terminal Adapter (stdin/stdout)       │
│   └── Embedded HTTP/WS Server (127.0.0.1:PORT)   │
│        └── Browser connects here                 │
│                                                  │
└─────────────────────────────────────────────────┘
```

- `agentunnel` launches an agent in a PTY and keeps the local terminal interactive
- A localhost web server mirrors the same session to a browser via WebSocket
- Both local terminal and browser can send input
- The Hub fans out PTY output to all connected sinks
- Existing WebSocket protocol: JSON frames with base64-encoded data (`input`, `output`, `resize`)

This foundation is correct for the final goal. The Hub/sink architecture, the WebSocket protocol, and the xterm.js client are all reusable. The gap is network scope and authentication.

## Target Architecture (v2 -- Remote Access via Relay)

```
  Your Laptop                        Your VPS                       Your Phone
 ┌──────────────┐                  ┌──────────────┐               ┌───────────┐
 │ agentunnel   │   outbound WS   │ relay server │   WS (TLS)   │  mobile   │
 │              │─────────────────>│              │<─────────────│  web app  │
 │ session A ───│── yamux stream ──│  session     │── route A ───│           │
 │ (claude)     │                  │  registry    │              │  xterm.js │
 │              │                  │              │              │  terminal │
 │ session B ───│── yamux stream ──│  auth (JWT)  │── route B ───│           │
 │ (codex)      │                  │              │              │           │
 └──────────────┘                  └──────────────┘               └───────────┘
        │                                │                              │
   starts sessions                 routes + auth                  views + controls
   manually on laptop              multi-tenant                   any session
```

### How It Works

1. **Agent side (your laptop)**: Each `agentunnel` process opens an outbound WebSocket connection to the relay server on your VPS. It authenticates with an API key or JWT. The connection is wrapped with [yamux](https://github.com/hashicorp/yamux) for multiplexing -- multiple sessions share a single outbound connection, or each `agentunnel` process registers independently.

2. **Relay server (your VPS)**: A new `cmd/relay` binary. It accepts agent connections and mobile client connections, both over WebSocket with TLS. It maintains a session registry: `{user_id, session_id} -> yamux stream`. When a mobile client connects and requests a session, the relay bridges the two WebSocket streams.

3. **Mobile client (your phone)**: The same xterm.js web app, served by the relay or as a standalone PWA. It connects to the relay's public WebSocket endpoint, authenticates, lists available sessions, and attaches to one. The existing `input`/`output`/`resize` protocol works unchanged.

### Why Build the Relay, Not Wrap an Existing Tunnel Tool

We evaluated chisel, frp, rathole, bore, and pgrok. The conclusion: they solve "expose a local port to the internet," but we need multi-tenant auth, session routing, and a mobile-facing API. Wrapping any of these tools still requires building the auth layer, session registry, and routing logic -- which is most of the work. The tunnel layer itself is ~400-600 lines of Go using `gorilla/websocket` + `hashicorp/yamux`.

Building custom gives us:
- Full control over auth and session routing
- No subprocess management or sidecar deployment
- Same WebSocket protocol end-to-end
- Zero impedance mismatch with the existing codebase

### Why Outbound WebSocket from Laptop

Your laptop is behind NAT (home router, corporate firewall). Opening inbound ports is fragile and often impossible. Instead, `agentunnel` initiates the connection *outward* to the VPS. This works through any NAT, any firewall, any network -- the same way your browser connects to websites.

## Key Design Decisions

### Session creation is local only

You start sessions from your laptop terminal (`agentunnel claude`, `agentunnel codex`). The phone can only connect to sessions that are already running. There is no background daemon on the laptop listening for remote "start session" commands.

This keeps the architecture simple: each `agentunnel` process is self-contained, starts when you run it, and exits when the agent exits. A daemon mode can be added later if needed.

### Multi-tenant authentication

The relay server supports multiple users. Each user registers and gets credentials (API key or JWT). The relay isolates sessions by user -- you can only see and connect to your own sessions.

This enables a shared relay: you can run one relay on a VPS and let multiple people (teammates, friends) each connect their own agents and control them from their own phones.

### Multiple sessions per user

A user can have multiple `agentunnel` processes running simultaneously (e.g., Claude on project A, Codex on project B). Each registers as a separate session with the relay. The mobile app lists all active sessions and lets you switch between them.

### The existing WebSocket protocol is preserved

The `input`/`output`/`resize` JSON+base64 framing used between the local web server and the browser client today is the same protocol used end-to-end in the relay architecture. The relay does not interpret or transform these messages -- it just routes them.

## Evolution Path

```
v1 (done)     Localhost shared session
              └── Hub, fanout, local terminal, embedded web server

v2 (next)     Remote relay
              ├── cmd/relay binary (VPS deployment)
              ├── Agent-side relay connector (outbound WS + yamux)
              ├── JWT/API-key auth on relay
              ├── Session registry and routing
              └── Mobile-optimized web client

v3 (future)   Enhanced mobile experience
              ├── Daemon mode (start sessions from phone)
              ├── Push notifications (agent needs approval)
              ├── Session persistence (reconnect after disconnect)
              └── Observer mode (read-only access for sharing)
```

## Components to Build for v2

### 1. Relay Server (`cmd/relay`)

New binary deployed on the VPS.

Responsibilities:
- Accept agent WebSocket connections (authenticated)
- Accept mobile client WebSocket connections (authenticated)
- Maintain session registry: user -> list of active sessions
- Route mobile client to the correct agent session via yamux stream
- Serve the mobile web app (static assets)
- TLS termination (or sit behind a reverse proxy like Caddy/nginx)

### 2. Relay Connector (agent side)

New package `internal/relay` added to the agent.

Responsibilities:
- Open outbound WebSocket to relay server
- Authenticate with API key or JWT
- Register the current session (name, launcher type)
- Wrap the connection with yamux
- Accept inbound yamux streams (one per mobile client) and pipe them into the Hub as additional sinks
- Reconnect on disconnect with backoff

### 3. Auth System

Responsibilities:
- User registration and credential management
- JWT issuance and validation (or API key validation)
- Session isolation (users can only access their own sessions)

For v2, this can be simple: a config file or SQLite database on the relay with user records and API keys. Full registration UI is not required initially.

### 4. Mobile Web Client

The existing xterm.js client, adapted for mobile:
- Session list view (pick which agent to control)
- Touch-friendly input (on-screen keyboard works with xterm.js)
- Connection status and reconnect
- Served by the relay server or as a standalone page

### 5. Session List API

The relay exposes a simple REST or WebSocket API:
- List active sessions for the authenticated user
- Session metadata: name, launcher type, start time, terminal dimensions

## What Stays the Same

- PTY management (`internal/session/process.go`)
- Hub fanout (`internal/session/hub.go`)
- Local terminal adapter (`internal/session/local_terminal.go`)
- The `input`/`output`/`resize` WebSocket protocol (`internal/protocol/`)
- The xterm.js terminal component (`web/src/terminal.ts`)
- Launcher registry (`internal/launcher/`)
- The local embedded web server still works for localhost use
