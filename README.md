# agent-tunnel

Launch a terminal agent locally and expose the running PTY through a relay-backed session attach API.

The remote contract is attach-only: clients discover live sessions with `GET /api/sessions`, then attach to one session with `GET /api/sessions/:id/attach/ws`. On attach, the owning `tunnel` process sends a fresh terminal-state snapshot, which may include bounded agent-local normal-buffer scrollback, and then continues streaming live PTY bytes on that same websocket.

`tunnel` starts a real CLI command such as `claude`, `codex`, `gemini`, `qwen`, or `aider`, keeps the launching terminal interactive, and registers the session with a relay server. The relay is API-only: it authenticates app clients with user-scoped bearer tokens, authenticates agents with user-owned long-lived agent tokens, lists live sessions, brokers session-scoped attaches, and forwards structured input. Operator maintenance routes stay host-local outside the public `/api/` namespace. It does not retain transcript history and it does not emulate the terminal.

On startup, `tunnel` gives relay registration a short first chance to succeed. If that startup window expires, local terminal work still begins and `tunnel` continues reconnecting to the relay in the background. Runtime relay outages do not interrupt the local terminal session.

The local terminal remains the primary view of the PTY session. Remote clients are intentionally narrower:

- they can recover the current screen state on a fresh attach
- they can recover bounded recent normal-buffer scrollback when the agent mirror still has it
- they can continue receiving live terminal bytes after that snapshot
- they do not get full transcript replay, durable history recovery, or exact missed-byte recovery in this protocol revision

Client input uses structured events:

- `input_text` for normal typing, pasted text, IME-committed text, and explicit submit via `submit: true`
- `input_key` for special keys only

The relay forwards those events to the owning `tunnel` session. `tunnel` translates supported key events into PTY bytes locally, and it handles `input_text { submit: true }` as one serialized submit operation: write the provided text first, then write the same carriage return semantics used for `ENTER`, with no interleaving input for that session.

## Requirements

- Go 1.25+
- A launcher executable installed on `PATH`

## Quick Start

### 1. Start the relay

The relay now requires PostgreSQL, an application secret used for credential digests, and a fixed operator token for local maintenance commands:

```bash
export RELAY_DATABASE_URL=postgres://relay_user:change-me-db-password@localhost/agent_tunnel?sslmode=disable
export RELAY_APP_SECRET=change-me
export RELAY_OPERATOR_TOKEN=change-me-operator-token
go run ./cmd/migrate --schema-dir ./schema
go run ./cmd/relay serve --listen-addr 127.0.0.1:8586
```

Create one or more invite codes on the relay host:

```bash
go run ./cmd/relay invite create --count 3 --expires-in 7d
```

### 2. Start tunnel

After a user has registered, logged in, and created an agent token, point `tunnel` at the relay and launch a session:

```bash
make build
export TUNNEL_BASE_URL=http://127.0.0.1:8586
export TUNNEL_AUTH_TOKEN=<user-owned-agent-token>
./bin/tunnel claude
```

If you use the hosted relay at `https://diaro.me`, `TUNNEL_BASE_URL` is optional because that is the default.

Or with a label:

```bash
./bin/tunnel --label api-fix --base-url https://diaro.me codex
```

Expected stderr output when relay is available during startup:

```text
▶ tunnel claude — session <session-id>; relay connected (http://127.0.0.1:8586)
```

If relay startup registration does not succeed within the startup wait window, `tunnel` still enters the local terminal session and shows:

```text
▶ tunnel claude — session <session-id>; relay reconnecting (http://127.0.0.1:8586)
```

While reconnecting, `tunnel` keeps retrying in the background and shows a compact terminal status that local work continues.

Startup banners are rendered inline without consuming an extra terminal row.

Healthy startup banners are printed in bright green. Degraded startup banners, such as relay reconnecting, are printed in red.

### 3. Connect a client

App clients authenticate with bearer tokens returned by `POST /api/auth/login` and use the relay APIs:

- `GET /api/sessions` to list sessions whose owning agent is currently online
- `GET /api/sessions/:id/attach/ws` to attach to one online session

See [docs/api.md](docs/api.md) for the current public app-facing endpoint inventory, auth requirements, request and response examples, and error contracts.

Browser attach clients must be same-origin with the relay. Native clients that do not send an `Origin` header remain supported.

The attach websocket is session-scoped:

- the first JSON control message is `attached` with `session_id`, `cols`, and `rows`
- the next binary frames are snapshot bytes for the current terminal state, including bounded agent-local normal-buffer scrollback when available
- a `snapshot_done` control message marks the boundary after which binary frames are live PTY bytes
- later `resize` control messages tell the client to resize its terminal emulator
- client input goes back on the same websocket as JSON `input_text` and `input_key`

If the attach drops, the client should create a fresh terminal emulator state and open a fresh attach. Recovery in this protocol revision is fresh snapshot recovery, not transcript replay. A fresh snapshot may include bounded in-memory scrollback, but it is not a replay of every missed PTY byte.

## Session Attach Model

The current remote model is:

- `tunnel` owns the PTY and maintains the authoritative headless terminal mirror for that running session
- the relay stores live session metadata such as `started_at`
- `started_at` is a Unix timestamp encoded as a JSON integer in seconds
- a remote attach asks the agent for the current terminal state, plus bounded in-memory normal-buffer scrollback when available, not for relay-owned or durable old output history
- after the initial snapshot, the same attach continues as an ordered live byte stream for that client
- if the owning agent disconnects, the relay closes active attaches and removes the session from discovery immediately
- if an app session logs out or all app sessions are revoked by password change, the relay closes the affected app-side attaches but leaves the owning agent session online
- if the terminal is currently on the alternate screen, the snapshot restores that current alt-screen state; any preserved history comes from the underlying normal buffer, not from alt-screen replay

Stronger delivery guarantees, transcript history, and remote-driven PTY sizing are out of scope for this protocol revision.

## VPS Deployment

See [docs/deployment.md](docs/deployment.md) for the full deployment guide covering one-time host bootstrap (`nginx`, PostgreSQL, and optional `certbot`), systemd, the relay-specific nginx site config, automated deploys, and the operational runbook.

Quick start on the remote host:

```bash
sudo install -d -m 0755 /etc/agentunnel
sudo tee /etc/agentunnel/relay.env >/dev/null <<'EOF'
RELAY_DATABASE_URL=postgres://relay_user:change-me-db-password@localhost/agent_tunnel?sslmode=disable
RELAY_APP_SECRET=<long-random-secret>
RELAY_OPERATOR_TOKEN=<long-random-operator-token>
EOF
sudo chmod 600 /etc/agentunnel/relay.env
sudo ./bin/relay-migrate --env-file /etc/agentunnel/relay.env --schema-dir ./schema
sudo /bin/sh -lc 'set -a && . /etc/agentunnel/relay.env && set +a && ./bin/relay serve --listen-addr 127.0.0.1:8586'

# in another shell on the same host
sudo /bin/sh -lc 'set -a && . /etc/agentunnel/relay.env && set +a && ./bin/relay invite create --count 3 --expires-in 7d'
```

After the user registers in the app and creates an agent token, on each developer machine:

```bash
export TUNNEL_BASE_URL=https://diaro.me
export TUNNEL_AUTH_TOKEN=<user-owned-agent-token>
./bin/tunnel --label "feature-branch" claude
```

Keep two gitignored env files at the repo root: `.env.prod` for production and `.env.dev` for the dev VPS. Each file carries its own `DEPLOY_HOST`, and the remote install targets default `INSTALL_HOST` to that same value.

Bootstrap each host once:

```bash
make install-prod                                          # prod: prompts for certbot email, then installs nginx + PostgreSQL + certbot + TLS nginx site
make install-prod INSTALL_CERTBOT_EMAIL=ops@example.com   # prod: same flow, with the email provided up front
make install-dev                                          # dev: nginx + PostgreSQL + HTTP nginx site
```

Deploy:

```bash
make deploy-prod    # prod
make deploy-dev     # dev
```

`make install-prod` always requires a certbot email before it does anything else. If `INSTALL_CERTBOT_EMAIL` is missing, it stops and prompts in the terminal until you enter one. Then it installs `certbot`, issues or refreshes the TLS certificate with a webroot challenge, enables `certbot.timer`, and installs a renewal hook that reloads nginx after renewal. `make install-dev` skips `certbot` entirely and keeps the dev relay on plain HTTP port 80. `make install` remains the local binary install alias.

Every deploy syncs `schema/` to the remote host and reruns the migrator before the new relay binary and env file are activated. That is safe to repeat: the migrator records applied versions in `schema_migrations`, holds a PostgreSQL advisory lock while it runs, and applies each migration in its own transaction. `make deploy-install` skips binary installs when the remote copy already has the same sha256, so re-running a deploy on an unchanged build is still cheap. `make deploy` defaults to `ENV_FILE=.env.prod`; override with `make deploy ENV_FILE=.env.dev` or with individual flags when needed.

Deploy is intentionally narrower than install: it does not install or reconfigure `nginx`, `certbot`, or `postgresql`, and it does not rewrite those host-level config files. Those changes stay in the install phase so later manual host edits are not overwritten by routine relay deploys.

For install output, `make install-dev INSTALL_DRY_RUN=1` gives a structured preview and `make install-dev INSTALL_VERBOSE=1` prints debug details. Deploy output follows the same pattern with `DEPLOY_DRY_RUN=1` and `DEPLOY_VERBOSE=1`.

## Launchers

`tunnel` does not maintain a launcher allowlist. It resolves the user-provided command from `PATH`, starts it locally, and records that same command in session metadata and startup output.

## Development

```bash
make build             # builds bin/tunnel, bin/relay, and bin/relay-migrate
make install           # installs tunnel, relay, and relay-migrate to ~/.local/bin
make install-dev       # installs nginx + PostgreSQL on the dev VPS if needed and syncs the HTTP nginx site
make install-prod    # prompts for certbot email, then installs prod nginx + PostgreSQL + certbot and syncs the TLS nginx site
make test              # go test ./...
make test-relay        # focused relay/protocol contract tests
make local-e2e-db-up   # start fixed-version Docker PostgreSQL for local E2E
make test-local-e2e    # run local E2E against AGENTUNNEL_TEST_DATABASE_URL
make test-local-e2e-docker # start fixed-version Docker PostgreSQL and run local E2E
make test-local-e2e-clean  # reset DB, run local E2E, save output to tmp/local-e2e/latest.log, and fail on test or cleanup errors
make tunnel LAUNCHER=claude       # run tunnel directly
go run ./cmd/relay serve          # run relay server
make migrate           # run relay schema migrations using $(ENV_FILE) (default .env.prod) or the shell environment
```

See [docs/local-e2e.md](docs/local-e2e.md) for the Docker-backed local E2E workflow, manual acceptance checklist, and database inspection queries.

## Protocol

See [docs/api.md](docs/api.md) for the current public app-facing relay API reference.
See [docs/protocol.md](docs/protocol.md) for the full wire format specification.
See [docs/tui-attach-flow.md](docs/tui-attach-flow.md) for the end-to-end snapshot, live-byte, relay, and client reconnect flow.
See [docs/deployment.md](docs/deployment.md) for VPS deployment, nginx/TLS setup, and operations guide.
See [docs/operation.md](docs/operation.md) for day-to-day relay CLI usage and operator command examples.
