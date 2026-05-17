# agent-tunnel

Launch a terminal agent locally and expose daemon-backed mobile connectivity without making Relay the terminal data plane.

Cross-repository protocol decisions live in [yuanbohan/agent-tunnel-protocols](https://github.com/yuanbohan/agent-tunnel-protocols). This repository keeps the Go implementation, daemon behavior, tests, and operational docs aligned with that protocol source of truth.

For local cross-repository work, keep these sibling checkouts together:

- `../agent-tunnel-protocols` - SSOT protocol docs for pairing, Relay
  control-plane behavior, daemon transport, and end-to-end mobile flows.
- `../agent-tunnel-android` - official Android companion implementation and
  mobile UX/docs.

When a change affects pairing, trusted-computer visibility, direct/relay
transport, daemon session frames, or mobile detail input, update
`agent-tunnel-protocols` first or in the same PR set. This repository should
link to SSOT docs instead of carrying a second detailed copy of shared protocol
rules.

Primary SSOT entry points:

- `../agent-tunnel-protocols/docs/end-to-end-flows.md` - trusted computer list, pairing, session list/preview, direct/relay, detail input, key storage.
- `../agent-tunnel-protocols/docs/draws/README.md` - Mermaid diagrams for the critical flows.
- `../agent-tunnel-protocols/docs/api.md` - public Relay API, auth, WebSocket, fallback tunnel, removed endpoints.
- `../agent-tunnel-protocols/docs/architecture.md` - cross-repository ownership boundaries.
- `../agent-tunnel-protocols/docs/pairing.md` - Ed25519 transcripts, SAS, trust persistence, revocation.
- `../agent-tunnel-protocols/docs/relay-control-plane.md` - Relay realtime, rendezvous, fallback token, opaque packet forwarding.
- `../agent-tunnel-protocols/docs/protocol.md` - daemon-to-mobile QUIC transport, frame registry, stream model, payloads.
- `../agent-tunnel-protocols/docs/security.md` - threat model and security gates.
- `../agent-tunnel-protocols/docs/status/implementation-matrix.md` - implementation readiness and known gaps.
- `../agent-tunnel-protocols/docs/legacy/README.md` - historical designs that are no longer current authority.

The remote contract has separate live-only surfaces:

- computer launch control plane: app clients discover currently online computers with `GET /api/computers`, then ask one computer daemon to launch `tunnel run <command>` with required `cwd`, optional `label`, and wait for `session_ready`
- mobile companion session transport: after launch, the mobile companion treats `session_ready.session_id` as a correlation key and waits for the daemon connectivity transport to report the session through `session_index` or `session_upsert`; session roster, preview, terminal snapshots/live bytes, input, resize, and mobile session detail do not come from Relay
- local CLI session management: `tunnel session list` and `tunnel session stop <session-id>` use this computer's daemon control socket and broker state

`tunnel` starts a real CLI command such as `claude`, `codex`, `gemini`, `qwen`, or `aider`, keeps the launching terminal interactive, and registers launch ownership with Relay. A local `tunnel run` requires a compatible background daemon and registers session metadata, a bounded latest preview, coalesced terminal snapshots, and live output bytes over a local-only broker socket for trusted connectivity transports before the user command starts; `tunnel run` remains the PTY and terminal-mirror owner. Relay authenticates app clients with user-scoped bearer tokens, can bind app sessions to client device fingerprints, authenticates agents with user-owned long-lived agent tokens, lists currently online computer daemons, forwards computer launch requests, carries connectivity pairing/visibility/rendezvous control messages, pairs with the same-image Binding-only STUN service for direct UDP discovery, and provides an opaque WebSocket QUIC fallback tunnel. Operator maintenance routes stay host-local outside the public `/api/` namespace. Relay does not retain transcript history, expose account-wide session discovery, expose session attach, or emulate the terminal.

On startup, `tunnel` must establish Relay registration and daemon broker registration during the startup wait window. If either registration does not succeed, startup exits before terminal setup and does not launch the local command.

After startup, if the relay socket drops, `tunnel` keeps retrying with backoff (3s → 5m, with pauses between attempts), while local terminal work continues unchanged.

The local terminal remains the primary view of the PTY session. Trusted mobile clients are intentionally narrower:

- they receive current daemon broker session state through daemon connectivity transport
- they can receive current terminal snapshots, latest previews, and live output through the daemon transport
- they do not get Relay-owned transcript replay, durable history recovery, account-wide session sharing, or terminal bytes from Relay

Daemon transport client input uses structured events:

- `input_text` for normal typing, pasted text, IME-committed text, and explicit submit via `submit: true`
- `input_key` for special keys only

The daemon broker routes those events to the owning `tunnel` session. `tunnel` translates supported key events into PTY bytes locally, and it handles `input_text { submit: true }` as one serialized submit operation: write the provided text first, then write the same carriage return semantics used for `ENTER`, with no interleaving input for that session.

## Cloud Principle: Multi-Tenant Session Isolation

For any hosted relay deployment, multi-tenant isolation is a hard product invariant.

- every agent token is owned by exactly one user account
- when `tunnel` connects to `/agent/ws`, the relay binds that live owner state to the owning user of that token
- app-facing computer presence and launch requests are user-scoped
- daemon connectivity visibility is scoped by account, trusted client fingerprint, and daemon-local trusted rosters
- one user's token must never reveal or control another user's computers, launch correlations, fallback tunnels, or connectivity peers

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
After a successful install, it prints non-blocking guidance when `tmux` is missing. Tunnel never auto-installs `tmux`; users install it manually when they want mobile-created workspace sessions.
Official release packages also publish a `checksums.txt` manifest used by native `tunnel update` and `tunnel rollback`.

After install, Tunnel has three binary lifecycle paths:

- `tunnel update` installs the latest official release in place
- `tunnel rollback` re-downloads the previous recorded official release after one successful official upgrade
- interactive `tunnel run ...` checks for updates at most once every 24 hours and may prompt before startup

The startup prompt is only shown for interactive `tunnel run` sessions. Non-interactive usage stays silent and never blocks on update interaction.

The `Release` workflow records source tag `tunnel-vX.Y.Z` but publishes the public Tunnel version as plain `vX.Y.Z`. It does not publish `relay` binaries.

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

If you use the hosted relay at `https://agentunnel.cn`, `--base-url` is optional because that is the default.

### 3. Start tunnel

Launch a session with the saved local login:

```bash
./bin/tunnel run claude
```

Or with a label:

```bash
./bin/tunnel run -l api-fix --base-url https://agentunnel.cn codex
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

### 2b. Manage the local daemon

`tunnel run` requires a same-base-URL and same-auth-context daemon. It starts the daemon when needed, waits for the local broker to accept the session id, and only then starts the user command. Broker reconnects re-check the daemon's Relay base URL and non-secret auth-context fingerprint before sending local session metadata, previews, snapshots, or live output. You can manage the daemon explicitly, and should start it before relying on mobile-created sessions:

```bash
./bin/tunnel auth login --base-url http://127.0.0.1:8586
./bin/tunnel daemon start
./bin/tunnel daemon status
./bin/tunnel daemon doctor
```

`tunnel daemon start` uses the same auth precedence as `tunnel run`: `TUNNEL_AUTH_TOKEN` first, then the saved local login in `~/.tunnel/auth.json`.

Pairing and trusted-client management are top-level commands:

```bash
./bin/tunnel pair
./bin/tunnel pair devices
./bin/tunnel pair revoke <fingerprint>
```

`tunnel pair` prints a terminal QR code, waits for the client response, and lets the user enter the 6-digit pairing code in the same command. `tunnel pair --json`, `tunnel pair devices --json`, and `tunnel pair revoke <fingerprint> --json` keep machine-readable automation paths. JSON-capable daemon and pair commands return a single `{"error":{"code":"...","message":"..."}}` envelope on command failures while preserving a non-zero exit status. Human `daemon start` output warns when launch readiness is degraded.

Workspace view commands are separate from session management:

```bash
./bin/tunnel workspace open
./bin/tunnel workspace close
```

Use `tunnel session list` to see live sessions on this computer and `tunnel session stop <session-id>` to stop one local session through the daemon broker. `tunnel session list --json` prints the same local-computer sessions in a machine-readable shape for automation. `workspace close` only detaches one local tmux workspace view; it does not stop the daemon or terminate sessions.

The daemon connectivity core can run without `tmux`. Missing `tmux` reports degraded launch readiness and prevents tmux-backed remote launch, but it does not prevent local broker registration or pairing/connectivity control paths.

The connectivity path is direct-first for the Go simulator and daemon-side implementation: peers exchange rendezvous hints over Relay realtime, attempt pinned QUIC/TLS over UDP, and fall back to the Relay tunnel if direct setup times out or fails. `daemon status` / `daemon doctor` expose only path/failure diagnostics such as `direct`, `relay`, or `direct_timeout`; they do not expose previews, snapshots, live bytes, or input text.

### 3. Connect a client

App clients authenticate with bearer tokens returned by `POST /api/auth/login`.

The mobile companion uses Relay for auth, account policy, pairing, computer presence, rendezvous, fallback tunnel setup, and `POST /api/computers/:computerID/sessions`; after `session_ready`, it waits for the daemon connectivity transport to report the launched session through `session_index` or `session_upsert`.

See `../agent-tunnel-protocols/docs/api.md` for the current public Relay API endpoint inventory, auth requirements, request/response examples, and error contracts.

Device daemons connect separately on `/device/ws`. Reverse proxies for hosted relay deployments must forward that path alongside `/api/` and `/agent/ws`.
## Session Transport Model

The current remote/mobile model is:

- `tunnel` owns the PTY and maintains the authoritative headless terminal mirror for that running session
- the daemon broker keeps live local session metadata, latest preview, latest coalesced terminal snapshot, and live output bytes for trusted daemon transports
- Relay stores only live owner/correlation state needed for `/agent/ws` registration, launch readiness, token/user cleanup, and online daemon routing
- `started_at` is a Unix timestamp encoded as a JSON integer in seconds
- mobile clients receive session rows and terminal streams from daemon connectivity transport, not Relay session APIs
- if the owning `tunnel run` process disconnects from the daemon broker, the daemon removes that local session from broker state
- if the Relay socket drops after startup, local terminal work and daemon-local session transport continue according to their own connectivity state

Stronger delivery guarantees, transcript history, and Relay-driven PTY sizing are out of scope for this protocol revision.

## Computer Launch Model

Remote launch is computer-daemon-backed and tmux-backed:

- `tunnel run` requires a matching daemon and broker registration before starting the user command; users can also run `tunnel daemon start` explicitly so the computer is discoverable before any local session exists
- the daemon stays online until `tunnel daemon stop`
- clients use `GET /api/computers` to discover only currently connected computers
- `POST /api/computers/:computerID/sessions` always returns a `request_id`; success is `status: "session_ready"` plus `session_id` after the launched agent has registered, joined the local daemon broker, started its PTY process, and sent `launch_ready`; failure is `status: "failed"` plus a structured `reason` such as `command_not_allowed`, `device_offline`, `busy`, `path_not_found`, or `launch_timeout`
- for the official mobile companion, `session_ready.session_id` is a control-plane launch result; the visible session row, preview, terminal snapshot/live bytes, input, resize, and session detail come from the daemon connectivity transport after that same session id appears in `session_index` or `session_upsert`
- a successful launch creates a new dedicated tmux session and runs `tunnel run <command>` there
- when that launched `tunnel run <command>` exits, the tmux session stays available and returns to an interactive shell prompt
- users can inspect or resume the local workspace from any terminal with `tunnel workspace open`, or detach one open workspace view with `tunnel workspace close`
- `tunnel workspace close` is the inverse of `open`: it closes a local tmux workspace view if one is open, but it does not stop the daemon or terminate any session
- local session shutdown uses `tunnel session stop <session-id>` through the local daemon broker; it is scoped to sessions on this computer and does not stop sessions on other computers through Relay
- the daemon owns local launch state such as allowlist, busy/not-busy, tmux workspace health, doctor output, and last failure
- the relay only keeps transient online routing for connected daemons; if a daemon disconnects, it disappears from `GET /api/computers` immediately

Current scope boundaries:

- only macOS and Linux are supported for the computer daemon; local `tmux` is required for remote launch and workspace commands, not for the daemon connectivity core or local broker
- command authorization is a first-token allowlist read from the daemon config
- device identity is stable per machine-local daemon state, while display metadata such as device name and platform are refreshed whenever the daemon registers with the relay
- connectivity pairing uses a separate daemon-local Ed25519 identity, pending SAS-confirmed pairing responses, and trusted client roster; Relay visibility is live-only, granted by `pair_completed`, and rebuilt from the daemon roster on reconnect
- connectivity session transport uses the same pinned QUIC/TLS protocol on direct UDP and Relay fallback paths; `hello.path_kind` and `path_state` provide advisory Direct/Relay badge data for clients

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
export TUNNEL_BASE_URL=https://agentunnel.cn
./bin/tunnel auth login
./bin/tunnel run --label "feature-branch" claude
```

Publish Relay images from the private repo Actions tab by running `Release`, selecting `relay`, and entering a plain version such as `v0.1.0`. The workflow resolves source tag `relay-v0.1.0`, builds and verifies one Relay/STUN image artifact, then creates or validates that source tag immediately before pushing both `ghcr.io/yuanbohan/agent-tunnel-relay:v0.1.0` and `ghcr.io/yuanbohan/agent-tunnel-stun:v0.1.0`. Compose uses the Relay image name for the `relay` HTTP/WebSocket service and the STUN image name for the Binding-only `stun` UDP service. Set `RELAY_IMAGE_TAG` and `STUN_IMAGE_TAG` in the remote `.env` to exact plain versions; do not deploy from a mutable `latest` tag. On the first split-service rollout, both tags should point at the first release that includes `relay stun serve`.

The GHCR packages are private. Set `relay_ghcr_token` in the environment's Ansible secrets file so `make compose-up-*` can log in to GHCR as `yuanbohan` before pulling.

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
make compose-up-relay-cn    # routine Relay-only update; leaves STUN untouched
make compose-up-stun-relay-cn # rare STUN-only update
make compose-up-stack-relay-cn # first rollout/full-stack update
make compose-stop-relay-cn  # stop relay-cn services without removing containers
make relay-cn-ops           # print the common relay-cn Docker operator commands
make relay-cn-invite-list   # run `relay invite list` inside the relay-cn relay container
make relay-cn-status        # check nginx routes, website, relay health, auth paths, Compose state, and public STUN
make deploy-website-relay-cn # relay-cn website bundle from ../agent-tunnel-website
make compose-sync-dev       # sync Compose assets to dev
make compose-up-dev         # pull images and start/update dev services
make deploy-website-dev     # dev website bundle from ../agent-tunnel-website
```

The Compose role syncs `deploy/compose/` and `deploy/postgres/latest.sql`, but it does not overwrite the real remote `.env`. It also does not sync `.env.example` to the server. Create `/opt/agentunnel/compose/.env` manually on the server and keep secrets there. The checked-in example intentionally leaves secrets blank so Compose fails until the values are filled in.

For Docker Compose operations, the remote `/opt/agentunnel/compose/.env` is authoritative for Relay and PostgreSQL runtime configuration. Do not maintain duplicate runtime secrets in local Ansible files.

The Compose file hardcodes the non-secret runtime defaults for production operations:

- Relay listens in-container on `0.0.0.0:8586`
- Docker publishes Relay to the host on `127.0.0.1:8586`
- Relay disables embedded STUN in Compose with `RELAY_STUN_LISTEN_ADDR=off`
- The separate `stun` service runs `relay stun serve` from `ghcr.io/yuanbohan/agent-tunnel-stun:${STUN_IMAGE_TAG}`
- Docker publishes STUN directly to the host on UDP `3478`; nginx does not proxy STUN
- PostgreSQL uses database `agent_tunnel`
- PostgreSQL uses role `relay_user`
- PostgreSQL stores data in Docker volume `relay-postgres-data`

DNS for production should point `agentunnel.cn` and `www.agentunnel.cn` at nginx for HTTP/WebSocket traffic, and `stun.agentunnel.cn` at the same VPS for direct UDP `3478` today. The separate STUN hostname lets operators move STUN later without changing the Relay hostname. The VPS cloud security group and any host firewall must allow inbound `3478/udp`.

PostgreSQL data lives in the fixed `relay-postgres-data` Docker named volume. Relay structured logs are appended inside the container to `/var/log/agentunnel/relay.log`, which Compose persists on the host at `/opt/agentunnel/logs/relay/relay.log`. STUN logs are written beside it as `/opt/agentunnel/logs/relay/stun.log`. `deploy/postgres/latest.sql` initializes only an empty volume. Updating an existing database schema is a manual operator step: update `deploy/postgres/latest.sql` in the same code change, then run the required SQL on the server before deploying a Relay image that depends on it.

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
make compose-up-dev       # routine Relay-only Compose update on dev
make compose-stop-dev     # stop relay Compose services on dev
make compose-sync-relay-cn  # sync relay Compose assets to relay-cn
make compose-up-relay-cn    # routine Relay-only Compose update on relay-cn
make compose-up-stun-relay-cn # rare STUN-only Compose update on relay-cn
make compose-up-stack-relay-cn # full-stack Compose update on relay-cn
make compose-stop-relay-cn  # stop relay Compose services on relay-cn
make relay-cn-status        # check relay-cn nginx routes, website, relay health, auth paths, Compose state, and public STUN
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

See [yuanbohan/agent-tunnel-protocols](https://github.com/yuanbohan/agent-tunnel-protocols) (local checkout: `../agent-tunnel-protocols`) for the cross-repository protocol source of truth.

Go implementation entry points:

- `internal/relay/handler/` - Gin router, auth middleware, public API handlers, WebSocket handlers.
- `internal/relay/device/` - `/device/ws` online computer routing and launch request correlation.
- `internal/relay/connectivity/` - app/daemon connectivity peers, pairing response correlation, visibility, rendezvous, fallback tunnel tokens.
- `internal/connectivity/` - pairing, frame/session protocol helpers, transport, interop.
- `internal/tunnel/daemon/` - local daemon lifecycle, broker, tmux workspace, pairing state, direct/fallback transport runtime.
- `docs/daemon.md` - daemon-local behavior, launch health, failure reasons, and operational constraints.
- `docs/connectivity/local-broker.md` - local machine broker protocol between daemon and `tunnel run`; not a cross-repository protocol spec.

See [docs/deployment.md](docs/deployment.md) for VPS deployment, nginx/TLS setup, and operations guide.
See [docs/operation.md](docs/operation.md) for day-to-day relay CLI usage and operator command examples.
See [docs/release-distribution.md](docs/release-distribution.md) for public `tunnel` release publishing and distribution-repo operations.
