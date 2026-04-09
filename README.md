# agent-tunnel

Launch a terminal agent locally and stream the live PTY session to a remote relay service.

`agentunnel` starts a real CLI such as `claude`, `codex`, or `gemini`, keeps the launching terminal interactive, and registers the session with a relay server. Every launcher follows the same direct PTY path. The relay is API-only: it exposes authenticated HTTP and WebSocket endpoints for external clients to list live sessions, replay bounded retained output, attach to a live stream, and send input.

On startup, `agentunnel` gives relay registration a short first chance to succeed. If that startup window expires, local terminal work still begins and `agentunnel` continues reconnecting to the relay in the background. Runtime relay outages do not interrupt the local terminal session.

On macOS, once startup succeeds, `agentunnel` also attempts default-on idle sleep prevention for the lifetime of the `agentunnel` process. If that helper cannot be started, the session still starts and the startup line reports the failure.

The relay is intentionally content-opaque. It forwards and retains output bytes for replay, but it does not derive previews or other content semantics from terminal data.

Remote/mobile clients can observe and interact with live sessions and are suitable for real remote work, but the remote output path is currently best-effort. The local terminal remains the most complete view of the session output in the current revision.

Client input uses structured events:

- `input_text` for normal typing, pasted text, IME-committed text, and explicit submit via `submit: true`
- `input_key` for special keys and supported key combinations

The relay forwards those events to the owning `agentunnel` session. `agentunnel` translates supported key events into PTY bytes locally, and it handles `input_text { submit: true }` as one serialized submit operation: write the provided text first, then write the same carriage return semantics used for `ENTER`, with no interleaving input for that session.

## Requirements

- Go 1.25+
- A supported launcher installed on `PATH`: `claude`, `codex`, or `gemini`

## Quick Start

### 1. Start Redis and the relay

The relay requires Redis for retained frame storage. For local development:

```bash
make redis-up
```

Then set auth and Redis environment variables:

```bash
export AGENTUNNEL_BASIC_USER=demo
export AGENTUNNEL_BASIC_PASSWORD=secret
export AGENTUNNEL_AGENT_TOKEN=agent-token
export AGENTUNNEL_REDIS_URL=redis://127.0.0.1:6379/0
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

Expected stderr output on macOS when relay is available during startup:

```text
▶ agentunnel claude — session <session-id>; relay connected (127.0.0.1:8586); sleep prevented
```

If relay startup registration does not succeed within the startup wait window, `agentunnel` still enters the local terminal session and shows this on macOS:

```text
▶ agentunnel claude — session <session-id>; relay reconnecting (127.0.0.1:8586); sleep prevented
```

While reconnecting, `agentunnel` keeps retrying in the background and shows a compact terminal status that local work continues.

Healthy startup banners are printed in green. Degraded startup banners, such as relay reconnecting or `sleep prevention failed`, are printed in red.

If macOS sleep prevention cannot be enabled, startup still continues and the startup line reports `sleep prevention failed` instead of `sleep prevented`.

On non-macOS platforms, startup still succeeds but the banner reports `sleep unsupported` because this phase only implements idle sleep prevention on macOS.

### 3. Connect a client

Point your client at the relay with HTTP Basic Auth and use the relay APIs:

- `GET /api/sessions` to list live sessions
- `GET /api/updates/ws` to receive best-effort global live output updates for all sessions on one foreground socket
- `GET /api/sessions/:id/frames` to fetch the currently retained output frames for a known `session_id` and recover recent relay-retained output after reconnect
- `GET /api/sessions/:id/frames?from=101&to=120` to fetch an inclusive output sequence range

Each output frame, whether fetched from retained history or received live over websocket, includes:

- `cols` and `rows`
- relay-assigned UTC `ts`

Relay `seq` values describe the order of frames the relay has recorded, not proof that a remote client has seen every byte the local PTY produced. After reconnecting to `GET /api/updates/ws`, clients should treat `GET /api/sessions/:id/frames` as the standard relay-side recovery path for recently retained output.

Retained output frames are intentionally bounded and incomplete. The relay currently keeps them in Redis with a 24 hour sliding TTL that refreshes when new output is recorded, and it trims oldest-first within the per-session byte budget. `/api/sessions` remains live-only, so an offline session disappears from the session list immediately, while `/api/sessions/:id/frames` may still replay retained output for that known `session_id` until the retained history expires.

Stronger delivery guarantees may be considered later, but the current contract is intentionally best-effort.

## VPS Deployment

On the remote host:

```bash
export AGENTUNNEL_BASIC_USER=ops
export AGENTUNNEL_BASIC_PASSWORD=strong-password
export AGENTUNNEL_AGENT_TOKEN=shared-agent-token
export AGENTUNNEL_REDIS_URL=redis://127.0.0.1:6379/0
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
make redis-up          # start local redis for relay retained history
make redis-logs        # follow local redis logs
make redis-down        # stop local redis
make agentunnel LAUNCHER=claude   # run agentunnel directly
make relay             # run relay server
```

## Protocol

See [docs/protocol.md](docs/protocol.md) for the full wire format specification.
