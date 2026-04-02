# agent-tunnel

Launch a terminal agent locally and mirror the same PTY session into a localhost web client.

`agentunnel` starts a real CLI such as `claude`, `codex`, or `gemini`, keeps the launching terminal interactive, and serves a browser client that shares the exact same input and output stream. The original `agent` and `client` commands also remain available as the legacy shell-over-WebSocket mode.

## Requirements

- Go 1.25+
- Node.js and npm for the bundled web UI (`make build` and `make agentunnel` both depend on `web-build`)
- A supported launcher installed on `PATH`: `claude`, `codex`, or `gemini`

## Quick Start

On a fresh machine, install web dependencies first:

```bash
make web-install
make agentunnel LAUNCHER=claude
```

Equivalent direct command:

```bash
go run ./cmd/agentunnel claude
```

Expected stderr output:

```text
▶ agentunnel — claude
  open http://127.0.0.1:43127
  local terminal and browser share the same live session
```

Open the printed URL in a browser. The local terminal and browser can both read from and write to the same live session.

## Supported Launchers

- `claude`
- `codex`
- `gemini`

`agentunnel` resolves these executables from `PATH` and runs the real CLI unchanged, so approvals and terminal UX still come from the original tool.

## Legacy Mode

The original `agent` and `client` commands remain available:

```bash
make agent
make client
```

This path starts a shell PTY on localhost and connects a terminal client to it. It is separate from `agentunnel`.

## Development

On a fresh machine, run `make web-install` before `make build`, `make test`, or `make agentunnel`.

```bash
make agentunnel LAUNCHER=claude
make build
make test
make web
```

Command reference:

```bash
make build             # builds bin/agent, bin/client, and bin/agentunnel
make web               # run web client dev server
make web-build         # rebuild embedded web assets in internal/webui/dist
make agent             # run legacy PTY shell server on localhost:8585
make client            # run legacy CLI client against ws://localhost:8585/ws
```

If you change files under `web/`, rebuild the embedded assets before committing:

```bash
make web-build
```

## Protocol

JSON frames over WebSocket text messages.

| Type     | Direction        | Payload                        |
|----------|------------------|--------------------------------|
| `input`  | client → agent   | `data`: base64-encoded stdin   |
| `output` | agent → client   | `data`: base64-encoded stdout  |
| `resize` | client → agent   | `cols`, `rows` as integers     |

`stderr` is merged into `output` because the PTY exposes a single terminal stream.
