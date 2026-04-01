# agent-tunnel

A terminal-over-WebSocket tool. The agent spawns a real PTY shell and exposes it over WebSocket on localhost. The client connects, puts your terminal in raw mode, and proxies all I/O — giving you a live shell session.

## Requirements

- Go 1.25+

## Run

Start the agent first — it's the same for both clients.

**Tab 1 — start the agent:**
```bash
make agent
# or: go run ./cmd/agent
# 2026/03/31 22:00:00 listening on :8585
```

### Option A: Web client (browser)

```bash
# First time only
make web-install

make web
# → open http://localhost:3000
```

The browser terminal connects to the agent automatically. A status bar shows connection state; click **Reconnect** if the agent isn't running yet.

### Option B: CLI client (Go)

```bash
make client
# or: go run ./cmd/client
```

You will see your zsh prompt. Type commands and see results — it is a real PTY session, so interactive programs (`vim`, `htop`, `top`) work too.

**To disconnect:** type `exit` or press Ctrl+D inside the session.

> **Note on Ctrl+C:** In raw mode, Ctrl+C is forwarded to the remote shell (interrupts whatever is running there), not to the client itself. Use `exit` or Ctrl+D to end the session.

## Build

```bash
make build        # builds bin/agent and bin/client
make agent        # run agent (port 8585)
make client       # run CLI client (connects to localhost:8585)
make web          # run web client dev server (port 3000)
```

Custom port / URL:
```bash
go run ./cmd/agent  -port 9000
go run ./cmd/client -url ws://localhost:9000/ws
```

## Protocol

JSON frames over WebSocket text messages.

| Type     | Direction        | Payload                        |
|----------|------------------|--------------------------------|
| `input`  | client → agent   | `data`: base64-encoded stdin   |
| `output` | agent → client   | `data`: base64-encoded stdout  |
| `resize` | client → agent   | `cols`, `rows` as integers     |

stderr is merged into `output` — the PTY has a single output stream, same as a real terminal.
