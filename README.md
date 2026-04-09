# agent-tunnel

Launch a terminal agent locally and stream the live PTY session to a remote relay service.

`agentunnel` starts a real CLI such as `claude`, `codex`, or `gemini`, keeps the launching terminal interactive, and registers the session with a relay server. The relay is API-only: it authenticates clients, lists live sessions, fans out best-effort live output, and proxies session-history reads from the owning agent. It does not retain frame history itself.

On startup, `agentunnel` gives relay registration a short first chance to succeed. If that startup window expires, local terminal work still begins and `agentunnel` continues reconnecting to the relay in the background. Runtime relay outages do not interrupt the local terminal session.

On macOS, once startup succeeds, `agentunnel` also attempts default-on idle sleep prevention for the lifetime of the `agentunnel` process. If that helper cannot be started, the session still starts and the startup line reports the failure.

The relay is intentionally content-opaque. It forwards output bytes and proxies history reads, but it does not derive previews or other content semantics from terminal data.

Remote and mobile clients can observe and interact with live sessions, but the remote path remains intentionally lighter weight than the local terminal:

- `GET /api/updates/ws` is the best-effort live output channel
- `GET /api/sessions/:id/frames` is the recovery path for the current in-memory transcript owned by the running agent
- the local terminal remains the most complete view of session output

Client input uses structured events:

- `input_text` for normal typing, pasted text, IME-committed text, and explicit submit via `submit: true`
- `input_key` for special keys and supported key combinations

The relay forwards those events to the owning `agentunnel` session. `agentunnel` translates supported key events into PTY bytes locally, and it handles `input_text { submit: true }` as one serialized submit operation: write the provided text first, then write the same carriage return semantics used for `ENTER`, with no interleaving input for that session.

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

- `GET /api/sessions` to list live sessions, including `state` (`connected` or `reconnecting`) and the latest known agent-authored `latest_seq`
- `GET /api/updates/ws` to receive best-effort global live output updates for all sessions on one foreground socket
- `GET /api/sessions/:id/frames` to fetch the current session transcript from the connected owning agent
- `GET /api/sessions/:id/frames?from=101&to=120` to fetch an inclusive output sequence range

Each output frame, whether fetched through `/frames` or received live over websocket, includes:

- `seq`
- `data_b64`
- `cols` and `rows`
- agent-authored UTC `ts`

`seq`, `ts`, and `latest_seq` are authored by the running `agentunnel` process. They describe the current agent-owned transcript, not proof that a remote client has seen every byte the local PTY produced. After reconnecting to `GET /api/updates/ws`, clients should treat `GET /api/sessions/:id/frames` as the standard recovery path while the session is `connected`.

## Session History Model

The session transcript now lives on the agent side.

- `agentunnel` keeps a bounded in-memory history buffer for the lifetime of the running session
- every PTY output chunk is appended locally as a replay frame with agent-authored `seq`, `ts`, `cols`, and `rows`
- the relay stores session metadata such as `latest_seq`, `last_active_at`, state, and the current owner connection, but it does not store the frame array
- when a client calls `GET /api/sessions/:id/frames`, the relay sends a `history_request` over `/agent/ws`, the agent snapshots its local buffer, and the relay returns the agent's `history_response` frames to the client
- the history is a terminal output transcript only, not an exact input log; typed characters appear in history only when the terminal application echoes them
- the transcript is live-only and non-durable; if the agent process exits, that session and its in-memory history are gone

If the agent-relay link drops, the session stays discoverable as `reconnecting` for a short grace window, but `/frames` and remote input are unavailable until the agent reconnects. If the grace window expires, the relay removes the session.

Stronger delivery guarantees may be considered later, but the current contract is intentionally best-effort.

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
