# agent-tunnel

Launch a terminal agent locally and stream the live PTY session to a remote relay dashboard.

`agentunnel` starts a real CLI such as `claude`, `codex`, or `gemini`, keeps the launching terminal interactive, and registers the session with a relay server. The relay serves a browser dashboard where authenticated users can list live sessions and attach to any one of them in real time.

## Requirements

- Go 1.25+
- Node.js and npm for the bundled web UI (`make build` depends on `web-build`)
- A supported launcher installed on `PATH`: `claude`, `codex`, or `gemini`

## Quick Start

### 1. Start the relay

The relay requires three environment variables for auth:

```bash
export AGENTUNNEL_BASIC_USER=demo
export AGENTUNNEL_BASIC_PASSWORD=secret
export AGENTUNNEL_AGENT_TOKEN=agent-token
make relay
```

The relay listens on `0.0.0.0:8586` by default. Override the port with `--port`:

```bash
go run ./cmd/relay --port 9000
```

### 2. Start agentunnel

Point the agent at the relay and launch a session:

```bash
export AGENTUNNEL_RELAY_ADDR=127.0.0.1:8586
export AGENTUNNEL_RELAY_TOKEN=agent-token
go run ./cmd/agentunnel claude
```

Or with a label:

```bash
go run ./cmd/agentunnel --label api-fix --relay-addr 127.0.0.1:9000 codex
```

Expected stderr output:

```text
▶ agentunnel — claude
  relay: 127.0.0.1:8586
  local terminal is interactive
```

### 3. Open the dashboard

Open `http://localhost:8586/` in a browser, authenticate with the Basic Auth credentials, and choose a live session from the dashboard.

## VPS Deployment

On the remote host:

```bash
export AGENTUNNEL_BASIC_USER=ops
export AGENTUNNEL_BASIC_PASSWORD=strong-password
export AGENTUNNEL_AGENT_TOKEN=shared-agent-token
./bin/relay --port 8586
```

On each developer machine:

```bash
export AGENTUNNEL_RELAY_ADDR=relay.example.com:8586
export AGENTUNNEL_RELAY_TOKEN=shared-agent-token
./bin/agentunnel --label "feature-branch" claude
```

Then open `http://relay.example.com:8586/` in any browser.

## Supported Launchers

- `claude`
- `codex`
- `gemini`

`agentunnel` resolves these executables from `PATH` and runs the real CLI unchanged, so approvals and terminal UX still come from the original tool.

## Development

On a fresh machine, run `make web-install` before `make build` or `make test`.

```bash
make web-install       # install web dependencies
make build             # builds bin/agentunnel and bin/relay
make test              # go test + web tests
make web-build         # rebuild embedded web assets in webui/dist
make web               # run web dev server
make agentunnel LAUNCHER=claude   # run agentunnel directly
make relay             # run relay server
```

If you change files under `web/`, rebuild the embedded assets before committing:

```bash
make web-build
```

## Protocol

See [docs/protocol.md](docs/protocol.md) for the full wire format specification.

JSON frames over WebSocket text messages:

| Type     | Direction        | Payload                        |
|----------|------------------|--------------------------------|
| `input`  | browser -> relay -> agent | `data`: base64-encoded stdin |
| `output` | agent -> relay -> browser | `data`: base64-encoded stdout |
| `resize` | browser -> relay -> agent | `cols`, `rows` as integers   |
