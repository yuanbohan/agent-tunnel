# agent-tunnel

Launch a terminal agent locally and expose the running PTY through relay-backed session attach and device-launch APIs.

The remote contract now has two live-only surfaces:

- session attach: clients discover live sessions with `GET /api/sessions`, then attach to one session with `GET /api/sessions/:id/attach/ws`
- device launch: clients discover currently online devices with `GET /api/devices`, then ask one device daemon to launch `tunnel run <command>` with required `cwd`, optional `label`, and wait for `session_ready`; any live session can later be stopped through `POST /api/sessions/:id/stop`

On attach, the owning `tunnel` process sends a fresh terminal-state snapshot, which may include up to 10,000 lines of bounded agent-local normal-buffer scrollback, and then continues streaming live PTY bytes on that same websocket. The `snapshot_done` control message may also include bounded agent-local submit anchors for jump-dot navigation, and already attached clients may receive live `submit_anchor` controls for newly recorded submit Enter events.

`tunnel` starts a real CLI command such as `claude`, `codex`, `gemini`, `qwen`, or `aider`, keeps the launching terminal interactive, and registers the session with a relay server. The relay is API-only: it authenticates app clients with user-scoped bearer tokens, can bind app sessions to Android device fingerprints, authenticates agents with user-owned long-lived agent tokens, lists live sessions, lists currently online daemons, brokers session-scoped attaches, forwards structured input, forwards session stop control, forwards device launch requests, and carries Step 2 connectivity pairing/visibility control messages. Session discovery now includes best-effort Git branch metadata for the startup directory, optional local daemon identity through `device_id`, `launch_source` (`local` or `mobile`), and best-effort machine identity metadata from the registering agent, including platform family, platform id, and normalized computer name. Operator maintenance routes stay host-local outside the public `/api/` namespace. It does not retain transcript history and it does not emulate the terminal.

On startup, `tunnel` must establish relay registration during the startup wait window. If registration does not succeed, startup exits with a relay connection error and does not launch the local terminal session.

After startup, if the relay socket drops, `tunnel` keeps retrying with backoff (3s → 5m, with pauses between attempts), while local terminal work continues unchanged.

The local terminal remains the primary view of the PTY session. Remote clients are intentionally narrower:

- they can recover the current screen state on a fresh attach
- they can recover bounded recent normal-buffer scrollback, currently up to 10,000 lines, when the agent mirror still has it
- they can recover bounded submit anchors when those anchors still map into the fresh snapshot context
- they can continue receiving live terminal bytes and live submit-anchor controls after that snapshot
- they do not get full transcript replay, durable history recovery, or exact missed-byte recovery in this protocol revision

Client input uses structured events:

- `input_text` for normal typing, pasted text, IME-committed text, and explicit submit via `submit: true`
- `input_key` for special keys only

The relay forwards those events to the owning `tunnel` session. `tunnel` translates supported key events into PTY bytes locally, and it handles `input_text { submit: true }` as one serialized submit operation: write the provided text first, then write the same carriage return semantics used for `ENTER`, with no interleaving input for that session. Any local or remote input write that sends the `ENTER` carriage return outside a bracketed-paste region may create a bounded agent-local submit anchor for snapshots and live attached clients; anchors are navigation hints, not transcript records or exact Codex-rendered message markers.

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
Official release packages also publish a `checksums.txt` manifest used by native `tunnel update` and `tunnel rollback`.

After install, Tunnel has three binary lifecycle paths:

- `tunnel update` installs the latest official release in place
- `tunnel rollback` re-downloads the previous recorded official release after one successful official upgrade
- interactive `tunnel run ...` checks for updates at most once every 24 hours and may prompt before startup

The startup prompt is only shown for interactive `tunnel run` sessions. Non-interactive usage stays silent and never blocks on update interaction.

Tunnel and Relay are guaranteed compatible within the same compatibility line:

- for `v1+`, the compatibility line is the major version
- for pre-`v1`, the compatibility line is `0.minor`, so `v0.1.x` and `v0.2.x` are different lines

The `Release` workflow enforces that a published `tunnel` version stays within the current repo relay compatibility line when `tunnel` is selected. It records source tag `tunnel-vX.Y.Z` but publishes the public Tunnel version as plain `vX.Y.Z`. It does not publish `relay` binaries.

Verify the installed version with `tunnel --version`.
For CLI usage, flags, and examples, run `tunnel --help`. Local command launch now lives under `tunnel run <command>`. `tunnel run` supports `-v` and `-l` as short forms for `--verbose` and `--label`. `--base-url` remains long-form only.

### Local state

Tunnel keeps persistent local CLI state under `~/.tunnel/`:

- `auth.json`: saved local fallback auth created by `tunnel auth login`
- `settings.json`: user-editable settings, currently used for `env` overrides such as `TUNNEL_UPDATE_DISABLED`
- `updater.json`: internal updater cadence and rollback bookkeeping

Real environment variables override matching keys from `~/.tunnel/settings.json`.

To disable the automatic startup update check for `tunnel run`, either export:

```sh
export TUNNEL_UPDATE_DISABLED=1
```

or add it to `~/.tunnel/settings.json`:

```json
{
  "env": {
    "TUNNEL_UPDATE_DISABLED": "1"
  }
}
```

## Quick Start

### 1. Start the relay

The relay now requires PostgreSQL, an application secret used for credential digests, and a fixed operator token for local maintenance commands:

```bash
cp deploy/compose/.env.example deploy/compose/.env
$EDITOR deploy/compose/.env
docker compose --env-file deploy/compose/.env -f deploy/compose/compose.yaml up -d
curl -fsS http://127.0.0.1:8586/healthz
```

Create one or more invite codes on the relay host:

```bash
docker compose --env-file deploy/compose/.env -f deploy/compose/compose.yaml exec relay relay invite create --count 3 --expires-in 7d
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

Before an interactive `tunnel run`, Tunnel now performs one update check at most once per 24-hour interval. If a newer official release is available, Tunnel shows this startup prompt before the relay registration and PTY startup path begins:

```text
A new Tunnel version is available

Current: v0.1.7
Latest:  v0.1.9

? Update Tunnel now?
> Update now
  Skip and continue
```

If you choose `Update now`, Tunnel updates itself to the latest official release and restarts the same `tunnel run` command under the new binary. If the download or replacement step fails before restart, Tunnel reports the failure and continues the current `tunnel run`. If the binary replacement succeeds but the automatic restart fails, Tunnel stops and prints a recovery path instead of silently continuing.

You can always manage the binary explicitly:

```sh
tunnel update
tunnel rollback
```

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
- the next binary frames are snapshot bytes for the current terminal state, including up to 10,000 lines of bounded agent-local normal-buffer scrollback when available
- a `snapshot_done` control message marks the boundary after which binary frames are live PTY bytes, and may include bounded submit anchors
- later `submit_anchor` control messages may add live submit anchors for already attached clients
- later `resize` control messages tell the client to resize its terminal emulator
- client input goes back on the same websocket as JSON `input_text` and `input_key`

If the attach drops, the client should create a fresh terminal emulator state and open a fresh attach. Recovery in this protocol revision is fresh snapshot recovery, not transcript replay. A fresh snapshot may include up to 10,000 lines of bounded in-memory scrollback, but it is not a replay of every missed PTY byte.

## Session Attach Model

The current remote model is:

- `tunnel` owns the PTY and maintains the authoritative headless terminal mirror for that running session
- the relay stores live session metadata such as `started_at`
- the relay also stores session metadata such as `git_branch`, optional local daemon `device_id`, `platform_family`, `platform_id`, and normalized `computer_name`
- `started_at` is a Unix timestamp encoded as a JSON integer in seconds
- a remote attach asks the agent for the current terminal state, plus up to 10,000 lines of bounded in-memory normal-buffer scrollback when available, not for relay-owned or durable old output history
- a remote attach may also receive up to 256 bounded submit anchors that map to rows in the restored snapshot buffer; these anchors expire with agent-local retained context
- after the initial snapshot, the same attach continues as an ordered live byte stream for that client and may receive live `submit_anchor` controls for newly recorded submit Enter events
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
- users can inspect or resume the local workspace from any terminal with `tunnel daemon open`, detach one open workspace view with `tunnel daemon close`, or list sessions with `tunnel daemon sessions`
- `tunnel daemon close` is the inverse of `open`: it closes a local tmux workspace view if one is open, but it does not stop the daemon or terminate any session
- mobile/API session shutdown uses `POST /api/sessions/:sessionID/stop`; it sends `stop_session` to the owning `tunnel run` process, so local-launched and mobile-launched sessions use the same shutdown path
- the daemon owns local launch state such as allowlist, busy/not-busy, tmux workspace health, doctor output, and last failure
- the relay only keeps transient online routing for connected daemons; if a daemon disconnects, it disappears from `GET /api/devices` immediately

Current scope boundaries:

- only macOS and Linux environments with local `tmux` are supported
- command authorization is a first-token allowlist read from the daemon config
- device identity is stable per machine-local daemon state, while display metadata such as device name and platform are refreshed whenever the daemon registers with the relay
- connectivity pairing uses a separate daemon-local Ed25519 identity, pending SAS-confirmed pairing responses, and trusted Android roster; Relay visibility is live-only, granted by `pair_completed`, and rebuilt from the daemon roster on reconnect

See `docs/daemon.md` for the implementation contract that constrains daemon lifecycle, tmux workspace ownership, launch correlation, health reporting, and failure reasons.

## VPS Deployment

See [docs/docker-operation.md](docs/docker-operation.md) for the full Docker Compose deployment and operation guide covering GitHub/GHCR setup, remote paths, environment files, Ansible commands, logs, and PostgreSQL schema handling. [docs/deployment.md](docs/deployment.md) redirects to the current deployment guide.

Quick start on the remote host:

```bash
sudo install -d -m 0755 /opt/agentunnel
sudo cp -R deploy/compose deploy/postgres /opt/agentunnel/
cd /opt/agentunnel/compose
sudo cp .env.example .env
sudo chmod 600 .env
sudoedit .env
sudo docker compose --env-file .env pull
sudo docker compose --env-file .env up -d
curl -fsS http://127.0.0.1:8586/healthz

# in another shell on the same host
cd /opt/agentunnel/compose
sudo docker compose --env-file .env exec relay relay invite create --count 3 --expires-in 7d
```

After the user registers in the app, on each developer machine:

```bash
export TUNNEL_BASE_URL=https://diaro.me
./bin/tunnel auth login
./bin/tunnel run --label "feature-branch" claude
```

Publish Relay images from the private repo Actions tab by running `Release`, selecting `relay`, and entering a plain version such as `v0.1.0`. The workflow resolves source tag `relay-v0.1.0`, builds and verifies the image, then creates or validates that source tag immediately before pushing `ghcr.io/yuanbohan/agent-tunnel-relay:v0.1.0`. Set `RELAY_IMAGE_TAG` in the remote `.env` to the exact plain version you want to run; do not deploy from a mutable `latest` tag.

The GHCR package is private. Set `relay_ghcr_token` in the environment's Ansible secrets file so `make compose-up-*` can log in to GHCR as `yuanbohan` before pulling.

Keep host/bootstrap config in Ansible:

- `ansible/inventories/dev.yml` and `ansible/inventories/relay-cn.yml` define hosts, domains, and non-secret defaults
- `ansible/host_vars/dev/relay-secrets.yml` and `ansible/host_vars/relay-cn/relay-secrets.yml` hold deploy-only secrets such as `relay_ghcr_token` and, for `relay-cn` TLS bootstrapping, `relay_certbot_email`

Bootstrap each host once:

```bash
cp ansible/host_vars/dev/relay-secrets.example.yml ansible/host_vars/dev/relay-secrets.yml
cp ansible/host_vars/relay-cn/relay-secrets.example.yml ansible/host_vars/relay-cn/relay-secrets.yml
make init-dev          # dev: install nginx and render HTTP nginx
make init-relay-cn     # relay-cn: install nginx+certbot, render HTTP nginx, issue TLS cert, switch nginx to TLS
make compose-sync-relay-cn # sync Compose assets before creating /opt/agentunnel/compose/.env
```

Before running `make init-relay-cn`, point the production DNS records at the target host and set `relay_certbot_email` in `ansible/host_vars/relay-cn/relay-secrets.yml`; Let's Encrypt validates over HTTP-01 against the new host.

Deploy or update the Compose stack:

```bash
make compose-sync-relay-cn  # sync Compose assets to relay-cn
make compose-up-relay-cn    # pull images and start/update relay-cn services
make compose-stop-relay-cn  # stop relay-cn services without removing containers
make relay-cn-ops           # print the common relay-cn Docker operator commands
make relay-cn-invite-list   # run `relay invite list` inside the relay-cn relay container
make relay-cn-status        # check relay-cn website, relay health, API auth paths, websocket auth paths, and Compose state
make deploy-website-relay-cn # relay-cn website bundle from ../agent-tunnel-website
make compose-sync-dev       # sync Compose assets to dev
make compose-up-dev         # pull images and start/update dev services
make deploy-website-dev     # dev website bundle from ../agent-tunnel-website
```

The Compose role syncs `deploy/compose/` and `deploy/postgres/latest.sql`, but it does not overwrite the real remote `.env`. It also does not sync `.env.example` to the server. Create `/opt/agentunnel/compose/.env` manually on the server and keep secrets there. The checked-in example intentionally leaves secrets blank so Compose fails until the values are filled in.

For Docker Compose operations, the remote `/opt/agentunnel/compose/.env` is the runtime source of truth for Relay and PostgreSQL configuration. Do not maintain duplicate runtime secrets in local Ansible files.

The Compose file hardcodes the non-secret runtime defaults for production operations:

- Relay listens in-container on `0.0.0.0:8586`
- Docker publishes Relay to the host on `127.0.0.1:8586`
- PostgreSQL uses database `agent_tunnel`
- PostgreSQL uses role `relay_user`
- PostgreSQL stores data in Docker volume `relay-postgres-data`

PostgreSQL data lives in the fixed `relay-postgres-data` Docker named volume. Relay structured logs are appended inside the container to `/var/log/agentunnel/relay.log`, which Compose persists on the host at `/opt/agentunnel/logs/relay/relay.log`. `deploy/postgres/latest.sql` initializes only an empty volume. Updating an existing database schema is a manual operator step: update `deploy/postgres/latest.sql` in the same code change, then run the required SQL on the server before deploying a Relay image that depends on it.

Relay production operations are Docker Compose only: update `/opt/agentunnel/compose/.env`, run `docker compose`, and execute operator commands inside the `relay` container. Legacy binary/systemd paths may remain in the repository during the transition, but they are not part of the current production operating model. Website deploy stays separate: `make deploy-website-*` runs `npm ci`, builds `../agent-tunnel-website`, rejects bundle symlinks, uploads a release under `/var/www/agentunnel-website/releases`, and atomically repoints `/var/www/agentunnel-website/current`.

Use `ANSIBLE_DRY_RUN=1` for a check-mode preview and `ANSIBLE_EXTRA_VARS_FILE=<path>` if you want to layer extra vars on top of the checked-in inventories.

## Launchers

`tunnel` does not maintain a launcher allowlist. It resolves the user-provided command from `PATH`, starts it locally, and records that same command in session metadata and startup output.

## Development

```bash
make build             # builds bin/tunnel, bin/relay, and bin/relay-migrate
make install           # installs local development builds to ~/.local/bin without release versioning, tagging, or pushing
make install-dev       # installs packages and syncs the dev nginx config
make install-relay-cn  # installs packages, certbot, and syncs the relay-cn nginx config
make docker-relay-image-test # build the Relay Docker image and verify embedded version metadata
make compose-sync-dev     # sync relay Compose assets to dev
make compose-up-dev       # pull and start/update relay Compose services on dev
make compose-stop-dev     # stop relay Compose services on dev
make compose-sync-relay-cn  # sync relay Compose assets to relay-cn
make compose-up-relay-cn    # pull and start/update relay Compose services on relay-cn
make compose-stop-relay-cn  # stop relay Compose services on relay-cn
make relay-cn-status        # check relay-cn website, relay health, API auth paths, websocket auth paths, and Compose state
make deploy-website-dev   # build ../agent-tunnel-website and publish it to the dev host
make deploy-website-relay-cn  # build ../agent-tunnel-website and publish it to the relay-cn host
make test              # go test ./...
make test-relay        # focused relay/protocol contract tests
make local-e2e-db-up   # start fixed-version Docker PostgreSQL for local E2E
make test-local-e2e    # run local E2E against AGENTUNNEL_TEST_DATABASE_URL
make test-local-e2e-docker # start fixed-version Docker PostgreSQL and run local E2E
make test-local-e2e-clean  # reset DB, run local E2E, save output to tmp/local-e2e/latest.log, and fail on test or cleanup errors
make tunnel LAUNCHER=claude       # run tunnel directly
go run ./cmd/relay serve          # run relay server
make migrate           # legacy/local: run relay schema migrations using the current shell environment
```

See [docs/local-e2e.md](docs/local-e2e.md) for the Docker-backed local E2E workflow, manual acceptance checklist, and database inspection queries.

## Protocol

See [docs/api.md](docs/api.md) for the current public app-facing relay API reference.
See [docs/protocol.md](docs/protocol.md) for the full wire format specification.
See [docs/tui-attach-flow.md](docs/tui-attach-flow.md) for the end-to-end snapshot, live-byte, relay, and client reconnect flow.
See [docs/deployment.md](docs/deployment.md) for VPS deployment, nginx/TLS setup, and operations guide.
See [docs/operation.md](docs/operation.md) for day-to-day relay CLI usage and operator command examples.
See [docs/release-distribution.md](docs/release-distribution.md) for public `tunnel` release publishing and distribution-repo operations.
