# agent-tunnel

Launch a terminal agent locally and stream the live PTY session to a remote relay service.

`agentunnel` starts a real CLI such as `claude`, `codex`, or `gemini`, keeps the launching terminal interactive, and registers the session with a relay server. The relay is API-only: it exposes authenticated HTTP and WebSocket endpoints for external clients such as a mobile app to list live sessions, replay retained output, attach to a live stream, and send input.

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
- `GET /api/sessions/:id/history?after=0` to fetch the currently retained in-memory output buffer
- `GET /api/sessions/:id/ws?after=<seq>` to replay newer retained output and continue with the live stream
- `GET /api/session-events/ws` to stream session-level `action_required` transitions
- `POST /api/sessions/:id/read` to advance the shared read marker

Session history is intentionally live-only and in-memory. If the owning agent disconnects, the session disappears along with its retained history and unread state.

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

`agentunnel` resolves these executables from `PATH` and runs the real CLI unchanged, so approvals and terminal UX still come from the original tool.

## Development

```bash
make build             # builds bin/agentunnel and bin/relay
make test              # go test ./...
make test-real-hitl    # builds relay + agentunnel, then runs the real Codex approval smoke test
make agentunnel LAUNCHER=claude   # run agentunnel directly
make relay             # run relay server
```

The real human-in-the-loop smoke test uses a real `codex` session with `-a untrusted` to force an approval pause. It verifies that:

- relay session state flips to `action_required`
- `/api/session-events/ws` emits the transition outside terminal output
- `/api/sessions/:id/ws` still carries only terminal frames
- approving the request clears the session back to `normal`

It runs via [scripts/real_hitl_smoke.mjs](scripts/real_hitl_smoke.mjs) with [scripts/pty_driver.py](scripts/pty_driver.py) and requires a local `codex` install, an authenticated Codex environment, `node`, and `python3` on `PATH`.

## Protocol

See [docs/protocol.md](docs/protocol.md) for the full wire format specification.

JSON frames over WebSocket text messages:

| Type     | Direction        | Payload                        |
|----------|------------------|--------------------------------|
| `input`  | client -> relay -> agent | `data`: base64-encoded stdin |
| `output` | agent -> relay -> client | `seq`, `data`, `cols`, `rows`: base64-encoded PTY output plus terminal size |
| `resize` | agent -> relay | `cols`, `rows` as integers   |

The relay also exposes:
- `GET /api/sessions` for live session metadata, unread counts, preview payloads, and current session state
- `GET /api/sessions/:id/history` for `after`-based live-session history sync
- `GET /api/session-events/ws` for live session-state transitions outside terminal output
- `POST /api/sessions/:id/read` for advancing the shared per-session read marker
