# agent-tunnel

A terminal-over-WebSocket tool. The agent spawns a real PTY shell and exposes it over WebSocket on localhost. The client connects, puts your terminal in raw mode, and proxies all I/O — giving you a live shell session.

## Requirements

- Go 1.25+

## Run

### Shared live session mode

Use `agentunnel` to launch a supported terminal agent and mirror the same session into the browser.

```bash
make web-install
make agentunnel LAUNCHER=claude
```

Expected stderr output:

```text
▶ agentunnel — claude
  open http://127.0.0.1:43127
  local terminal and browser share the same live session
```

The local terminal remains interactive. Open the printed URL in a browser to view and type into the same live session.

### Legacy tools

The original `agent` and `client` commands remain available:

```bash
make agent
make client
```

## Build

```bash
make build             # builds bin/agent, bin/client, and bin/agentunnel
make agentunnel LAUNCHER=claude
make agent             # run agent (port 8585)
make client            # run CLI client (connects to localhost:8585)
make web               # run web client dev server (port 3000)
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
