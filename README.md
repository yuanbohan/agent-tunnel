# agent-tunnel

Launch a terminal agent locally and expose the running PTY through a relay-backed session attach API.

The remote contract is attach-only: clients discover live sessions with `GET /api/sessions`, then attach to one session with `GET /api/sessions/:id/attach/ws`. On attach, the owning `tunnel` process sends a current-screen snapshot and then continues streaming live PTY bytes on that same websocket.

`tunnel` starts a real CLI such as `claude`, `codex`, or `gemini`, keeps the launching terminal interactive, and registers the session with a relay server. The relay is API-only: it authenticates clients and agents, lists live sessions, brokers session-scoped attaches, forwards structured input, and manages reconnect lifecycle. It does not retain transcript history and it does not emulate the terminal.

On startup, `tunnel` gives relay registration a short first chance to succeed. If that startup window expires, local terminal work still begins and `tunnel` continues reconnecting to the relay in the background. Runtime relay outages do not interrupt the local terminal session.

On macOS, once startup succeeds, `tunnel` also attempts default-on idle sleep prevention for the lifetime of the `tunnel` process. If that helper cannot be started, the session still starts and the startup line reports the failure.

The local terminal remains the primary view of the PTY session. Remote clients are intentionally narrower:

- they can recover the current screen state on a fresh attach
- they can continue receiving live terminal bytes after that snapshot
- they do not get transcript replay or history recovery in this protocol revision

Client input uses structured events:

- `input_text` for normal typing, pasted text, IME-committed text, and explicit submit via `submit: true`
- `input_key` for special keys only

The relay forwards those events to the owning `tunnel` session. `tunnel` translates supported key events into PTY bytes locally, and it handles `input_text { submit: true }` as one serialized submit operation: write the provided text first, then write the same carriage return semantics used for `ENTER`, with no interleaving input for that session.

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

### 2. Start tunnel

Build the local binaries, then point the agent at the relay and launch a session:

```bash
make build
export AGENTUNNEL_RELAY_ADDR=127.0.0.1:8586
export AGENTUNNEL_RELAY_TOKEN=agent-token
./bin/tunnel claude
```

Or with a label:

```bash
./bin/tunnel --label api-fix --relay-addr 127.0.0.1:9000 codex
```

Expected stderr output on macOS when relay is available during startup:

```text
▶ tunnel claude — session <session-id>; relay connected (127.0.0.1:8586); sleep prevented
```

If relay startup registration does not succeed within the startup wait window, `tunnel` still enters the local terminal session and shows this on macOS:

```text
▶ tunnel claude — session <session-id>; relay reconnecting (127.0.0.1:8586); sleep prevented
```

While reconnecting, `tunnel` keeps retrying in the background and shows a compact terminal status that local work continues.

Healthy startup banners are printed in green. Degraded startup banners, such as relay reconnecting or `sleep prevention failed`, are printed in red.

If macOS sleep prevention cannot be enabled, startup still continues and the startup line reports `sleep prevention failed` instead of `sleep prevented`.

On non-macOS platforms, startup still succeeds but the banner reports `sleep unsupported` because this phase only implements idle sleep prevention on macOS.

### 3. Connect a client

Point your client at the relay with HTTP Basic Auth and use the relay APIs:

- `GET /api/sessions` to list live sessions and their `state` (`connected` or `reconnecting`)
- `GET /api/sessions/:id/attach/ws` to attach to one connected session

Browser attach clients must be same-origin with the relay. Native clients that do not send an `Origin` header remain supported.

The attach websocket is session-scoped:

- the first JSON control message is `attached` with `session_id`, `cols`, and `rows`
- the next binary frames are snapshot bytes for the current visible terminal state
- a `snapshot_done` control message marks the boundary after which binary frames are live PTY bytes
- later `resize` control messages tell the client to resize its terminal emulator
- client input goes back on the same websocket as JSON `input_text` and `input_key`

If the attach drops, the client should create a fresh terminal emulator state and open a fresh attach. Recovery in this protocol revision is current-screen recovery only, not transcript replay.

## Session Attach Model

The current remote model is:

- `tunnel` owns the PTY and maintains the authoritative headless terminal mirror for that running session
- the relay stores live session metadata such as `state`, `started_at`, and `last_active_at`
- `started_at` and `last_active_at` are Unix timestamps encoded as JSON integers in seconds
- a remote attach asks the agent for the current visible screen, not for old output history
- after the initial snapshot, the same attach continues as an ordered live byte stream for that client
- if the owning agent disconnects, the relay closes active attaches and the session becomes `reconnecting` briefly

Stronger delivery guarantees, transcript history, and remote-driven PTY sizing are out of scope for this protocol revision.

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
./bin/tunnel --label "feature-branch" claude
```

Then connect your mobile or other external client to `relay.example.com:8586`.

## Supported Launchers

- `claude`
- `codex`
- `gemini`

`tunnel` resolves these executables from `PATH` and runs the real CLI locally.

## Development

```bash
make build             # builds bin/tunnel and bin/relay
make install           # installs tunnel and relay to ~/.local/bin
make test              # go test ./...
make test-relay        # focused relay/protocol contract tests
make tunnel LAUNCHER=claude       # run tunnel directly
make relay             # run relay server
```

## Protocol

See [docs/protocol.md](docs/protocol.md) for the full wire format specification.
