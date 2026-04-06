# agent-tunnel

Launch a terminal agent locally and stream the live PTY session to a remote relay service.

`agentunnel` starts a real CLI such as `claude`, `codex`, or `gemini`, keeps the launching terminal interactive, and registers the session with a relay server. Every launcher follows the same direct PTY path. The relay is API-only: it exposes authenticated HTTP and WebSocket endpoints for external clients to list live sessions, replay retained output, attach to a live stream, and send input.

The relay is intentionally content-opaque. It forwards and retains output bytes for replay, but it does not derive previews or other content semantics from terminal data.

## Requirements

- Go 1.25+
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

### 3. Connect a client

Point your client at the relay with HTTP Basic Auth and use the relay APIs:

- `GET /api/sessions` to list live sessions
- `GET /api/updates/ws` to receive global live output updates for all sessions on one foreground socket
- `GET /api/sessions/:id/frames` to fetch the currently retained in-memory output frames

Session history is intentionally live-only and in-memory. If the owning agent disconnects, the session disappears along with its retained history.

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

Then connect your mobile or other external client to `relay.example.com:8586`.

## Supported Launchers

- `claude`
- `codex`
- `gemini`

`agentunnel` resolves these executables from `PATH` and runs the real CLI locally.

## Development

```bash
make build             # builds bin/agentunnel and bin/relay
make test              # go test ./...
make test-relay        # focused relay/protocol contract tests
make agentunnel LAUNCHER=claude   # run agentunnel directly
make relay             # run relay server
```

## Protocol

See [docs/protocol.md](docs/protocol.md) for the full wire format specification.
