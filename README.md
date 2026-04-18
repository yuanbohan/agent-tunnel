# agent-tunnel

Launch a terminal agent locally and expose the running PTY through relay-backed session attach and device-launch APIs.

The remote contract now has two live-only surfaces:

- session attach: clients discover live sessions with `GET /api/sessions`, then attach to one session with `GET /api/sessions/:id/attach/ws`
- device launch: clients discover currently online devices with `GET /api/devices`, then ask one device daemon to launch `tunnel run <command>` with required `cwd`, optional `label`, and wait for `session_ready`

On attach, the owning `tunnel` process sends a fresh terminal-state snapshot, which may include bounded agent-local normal-buffer scrollback, and then continues streaming live PTY bytes on that same websocket.

`tunnel` starts a real CLI command such as `claude`, `codex`, `gemini`, `qwen`, or `aider`, keeps the launching terminal interactive, and registers the session with a relay server. The relay is API-only: it authenticates app clients with user-scoped bearer tokens, authenticates agents with user-owned long-lived agent tokens, lists live sessions, lists currently online daemons, brokers session-scoped attaches, forwards structured input, and forwards device launch requests. Session discovery now includes best-effort machine identity metadata from the registering agent, including platform family, platform id, and normalized computer name. Operator maintenance routes stay host-local outside the public `/api/` namespace. It does not retain transcript history and it does not emulate the terminal.

On startup, `tunnel` must establish relay registration during the startup wait window. If registration does not succeed, startup exits with a relay connection error and does not launch the local terminal session.

After startup, if the relay socket drops, `tunnel` keeps retrying with backoff (3s → 5m, with pauses between attempts), while local terminal work continues unchanged.

The local terminal remains the primary view of the PTY session. Remote clients are intentionally narrower:

- they can recover the current screen state on a fresh attach
- they can recover bounded recent normal-buffer scrollback when the agent mirror still has it
- they can continue receiving live terminal bytes after that snapshot
- they do not get full transcript replay, durable history recovery, or exact missed-byte recovery in this protocol revision

Client input uses structured events:

- `input_text` for normal typing, pasted text, IME-committed text, and explicit submit via `submit: true`
- `input_key` for special keys only

The relay forwards those events to the owning `tunnel` session. `tunnel` translates supported key events into PTY bytes locally, and it handles `input_text { submit: true }` as one serialized submit operation: write the provided text first, then write the same carriage return semantics used for `ENTER`, with no interleaving input for that session.

## Cloud Principle: Multi-Tenant Session Isolation

For any hosted relay deployment, multi-tenant isolation is a hard product invariant.

- every agent token is owned by exactly one user account
- when `tunnel` connects to `/agent/ws`, the relay binds that live session to the owning user of that token
- `GET /api/sessions` returns only the authenticated user's live sessions
- `GET /api/sessions/:id/attach/ws` treats another user's session as not found
- one user's token must never list, reveal, or attach to another user's session, even when both users are online at the same time

This is one of the most important cloud guarantees in Agent Tunnel.

## Requirements

- Go 1.25+
- A launcher executable installed on `PATH`

## Install Tunnel

The public distribution repo is [yuanbohan/tunnel](https://github.com/yuanbohan/tunnel). Install the latest release with:

```sh
curl -fsSL https://raw.githubusercontent.com/yuanbohan/tunnel/main/install.sh | sh
```

Pin a specific release with:

```sh
curl -fsSL https://raw.githubusercontent.com/yuanbohan/tunnel/main/install.sh | VERSION=v0.1.2 sh
```

The installer writes `tunnel` to `~/.local/bin/tunnel` and supports `darwin/arm64`, `darwin/amd64`, `linux/amd64`, and `linux/arm64`.

Tunnel and Relay are guaranteed compatible within the same compatibility line:

- for `v1+`, the compatibility line is the major version
- for pre-`v1`, the compatibility line is `0.minor`, so `v0.1.x` and `v0.2.x` are different lines

The `Release Tunnel` workflow enforces that a published `tunnel` version stays within the current repo relay compatibility line. It does not publish `relay` binaries.

Verify the installed version with `tunnel --version`.
For CLI usage, flags, and examples, run `tunnel --help`. Local command launch now lives under `tunnel run <command>`. `tunnel run` supports `-v` and `-l` as short forms for `--verbose` and `--label`. `--base-url` remains long-form only.

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

### 2. Authenticate tunnel

After a user has registered, sign `tunnel` in once on that machine:

```bash
make build
./bin/tunnel auth login --base-url http://127.0.0.1:8586
```

This stores one local fallback token in `~/.tunnel/auth.json`. `TUNNEL_AUTH_TOKEN` still has higher priority when you need to override the saved login for CI, scripts, or one-off operator work.

If you use the hosted relay at `https://diaro.me`, `--base-url` is optional because that is the default.

### 3. Start tunnel

Launch a session with the saved local login:

```bash
./bin/tunnel run claude
```

Or with a label:

```bash
./bin/tunnel run -l api-fix --base-url https://diaro.me codex
```

Expected stderr output when relay is available during startup:

```text
▶ tunnel claude — session <session-id>; relay server connected
```

Startup banners are rendered inline without consuming an extra terminal row.
Healthy startup banners are printed in bright green.

### 2b. Start the device daemon

If you want mobile clients to create new sessions remotely on this machine, start the daemon explicitly:

```bash
./bin/tunnel auth login --base-url http://127.0.0.1:8586
./bin/tunnel daemon start
./bin/tunnel daemon status
./bin/tunnel daemon doctor
```

`tunnel daemon start` uses the same auth precedence as `tunnel run`: `TUNNEL_AUTH_TOKEN` first, then the saved local login in `~/.tunnel/auth.json`.

### 3. Connect a client

App clients authenticate with bearer tokens returned by `POST /api/auth/login` and use the relay APIs:

- `GET /api/sessions` to list sessions whose owning agent is currently online
- `GET /api/sessions/:id/attach/ws` to attach to one online session

See [docs/api.md](docs/api.md) for the current public app-facing endpoint inventory, auth requirements, request and response examples, and error contracts.

Browser attach clients must be same-origin with the relay. Native clients that do not send an `Origin` header remain supported.

Device daemons connect separately on `/device/ws`. Reverse proxies for hosted relay deployments must forward that path alongside `/api/` and `/agent/ws`.

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
- the relay also stores agent-supplied session device identity metadata such as `platform_family`, `platform_id`, and normalized `computer_name`
- `started_at` is a Unix timestamp encoded as a JSON integer in seconds
- a remote attach asks the agent for the current terminal state, plus bounded in-memory normal-buffer scrollback when available, not for relay-owned or durable old output history
- after the initial snapshot, the same attach continues as an ordered live byte stream for that client
- if the owning agent disconnects, the relay closes active attaches and removes the session from discovery immediately
- if an app session logs out or all app sessions are revoked by password change, the relay closes the affected app-side attaches but leaves the owning agent session online
- if the terminal is currently on the alternate screen, the snapshot restores that current alt-screen state; any preserved history comes from the underlying normal buffer, not from alt-screen replay

Stronger delivery guarantees, transcript history, and remote-driven PTY sizing are out of scope for this protocol revision.

## Device Launch Model

Remote launch is explicit and tmux-backed:

- users opt in per machine with `tunnel daemon start`
- the daemon stays online until `tunnel daemon stop`
- mobile clients use `GET /api/devices` to discover only currently connected devices
- `POST /api/devices/:deviceID/launch` always returns a `request_id`; success is `status: "session_ready"` plus `session_id`, and failure is `status: "failed"` plus a structured `reason` such as `command_not_allowed`, `device_offline`, `busy`, `path_not_found`, or `launch_timeout`
- a successful launch creates a new dedicated tmux session and runs `tunnel run <command>` there
- when that launched `tunnel run <command>` exits, the tmux session stays available and returns to an interactive shell prompt
- users can inspect or resume the local workspace from any terminal with `tunnel daemon open` or list sessions with `tunnel daemon sessions`
- the daemon owns local launch state such as allowlist, busy/not-busy, tmux workspace health, doctor output, and last failure
- the relay only keeps transient online routing for connected daemons; if a daemon disconnects, it disappears from `GET /api/devices` immediately

Current scope boundaries:

- only macOS and Linux environments with local `tmux` are supported
- command authorization is a first-token allowlist read from the daemon config
- device identity is stable per machine-local daemon state, while display metadata such as device name and platform are refreshed whenever the daemon registers with the relay

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

After the user registers in the app, on each developer machine:

```bash
export TUNNEL_BASE_URL=https://diaro.me
./bin/tunnel auth login
./bin/tunnel run --label "feature-branch" claude
```

Keep deployment config in Ansible:

- `ansible/inventories/dev.yml` and `ansible/inventories/prod.yml` define hosts, domains, and non-secret defaults
- `ansible/host_vars/dev/relay-secrets.yml` and `ansible/host_vars/prod/relay-secrets.yml` hold per-environment secrets

Bootstrap each host once:

```bash
cp ansible/host_vars/dev/relay-secrets.example.yml ansible/host_vars/dev/relay-secrets.yml
cp ansible/host_vars/prod/relay-secrets.example.yml ansible/host_vars/prod/relay-secrets.yml
make init-dev          # dev: install nginx+postgresql, create relay DB, render HTTP nginx
make init-prod         # prod: install nginx+postgresql+certbot, create relay DB, render HTTP nginx, issue TLS cert, switch nginx to TLS
```

Before running `make init-prod`, point the prod DNS records at the target host and set `relay_certbot_email` and `relay_database_password` in `ansible/host_vars/prod/relay-secrets.yml`; Let's Encrypt validates over HTTP-01 against the new host.

Deploy:

```bash
make deploy-prod            # prod relay
make deploy-dev             # dev relay
make deploy-website-prod    # prod website bundle from ../agent-tunnel-website
make deploy-website-dev     # dev website bundle from ../agent-tunnel-website
```

`make init-prod` and `make install-prod` both read `relay_certbot_email` from `ansible/host_vars/prod/relay-secrets.yml`. `make init-dev` and `make install-dev` skip `certbot` and keep the dev relay on plain HTTP port 80. The nginx config they render serves `/` from the static website at `/var/www/agentunnel-website/current` and proxies `/api/`, `/agent/ws`, and `/device/ws` to the relay. None of them build or publish the website bundle. `make init-*` is the idempotent bootstrap target used on a fresh host: it installs packages, creates the relay PostgreSQL user and database, and (on prod) runs the nginx render both before and after certificate issuance so nginx ends up on TLS. `make install-*` is the narrower slice that only covers package installation, certbot wiring, and nginx rendering. `make install` remains the local binary install alias.

Every relay deploy builds Linux binaries, syncs `schema/`, reruns the migrator, renders `/etc/agentunnel/relay.env` from Ansible variables, updates the systemd unit, and restarts the relay. Website deploy stays separate: `make deploy-website-*` runs `npm ci`, builds `../agent-tunnel-website`, rejects bundle symlinks, uploads a release under `/var/www/agentunnel-website/releases`, and atomically repoints `/var/www/agentunnel-website/current`.

For targeted relay maintenance, use the sliced deploy targets: `make migrator-dev` / `make migrator-prod` install only `relay-migrate`, `make relay-bin-dev` / `make relay-bin-prod` install only `relay`, `make migrate-dev` / `make migrate-prod` sync `schema/` and run migrations using the already-installed remote migrator, and `make relay-dev` / `make relay-prod` render relay env and systemd config, then restart the service using the already-installed remote relay binary.

Deploy is intentionally narrower than install: it does not install packages, request certificates, or change PostgreSQL users and databases unless you run the dedicated Ansible-tagged targets for those steps.

Use `ANSIBLE_DRY_RUN=1` for a check-mode preview and `ANSIBLE_EXTRA_VARS_FILE=<path>` if you want to layer extra vars on top of the checked-in inventories.

## Launchers

`tunnel` does not maintain a launcher allowlist. It resolves the user-provided command from `PATH`, starts it locally, and records that same command in session metadata and startup output.

## Development

```bash
make build             # builds bin/tunnel, bin/relay, and bin/relay-migrate
make install           # installs tunnel, relay, and relay-migrate to ~/.local/bin
make install-dev       # installs packages and syncs the dev nginx config
make install-prod      # installs packages, certbot, and syncs the prod nginx config
make deploy-website-dev   # build ../agent-tunnel-website and publish it to the dev host
make deploy-website-prod  # build ../agent-tunnel-website and publish it to the prod host
make test              # go test ./...
make test-relay        # focused relay/protocol contract tests
make local-e2e-db-up   # start fixed-version Docker PostgreSQL for local E2E
make test-local-e2e    # run local E2E against AGENTUNNEL_TEST_DATABASE_URL
make test-local-e2e-docker # start fixed-version Docker PostgreSQL and run local E2E
make test-local-e2e-clean  # reset DB, run local E2E, save output to tmp/local-e2e/latest.log, and fail on test or cleanup errors
make tunnel LAUNCHER=claude       # run tunnel directly
go run ./cmd/relay serve          # run relay server
make migrate           # run relay schema migrations using the current shell environment
```

See [docs/local-e2e.md](docs/local-e2e.md) for the Docker-backed local E2E workflow, manual acceptance checklist, and database inspection queries.

## Protocol

See [docs/api.md](docs/api.md) for the current public app-facing relay API reference.
See [docs/protocol.md](docs/protocol.md) for the full wire format specification.
See [docs/tui-attach-flow.md](docs/tui-attach-flow.md) for the end-to-end snapshot, live-byte, relay, and client reconnect flow.
See [docs/deployment.md](docs/deployment.md) for VPS deployment, nginx/TLS setup, and operations guide.
See [docs/operation.md](docs/operation.md) for day-to-day relay CLI usage and operator command examples.
See [docs/release-distribution.md](docs/release-distribution.md) for public `tunnel` release publishing and distribution-repo operations.
