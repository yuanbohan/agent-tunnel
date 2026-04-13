# Relay Deployment & Operations

This guide covers one-time host bootstrap on an Ubuntu VPS, automated relay deploys and schema migrations from a dev machine, static website deploys from the sibling website checkout, and day-to-day operations.

The repo now owns the relay-facing host setup as well as the binary deploy. `make install-dev` bootstraps `nginx` and PostgreSQL for the dev VPS, syncs the repo-controlled HTTP nginx site, and restarts nginx. `make install-prod` does the same for production, plus `certbot`, certificate issuance, a renewal hook, and the repo-controlled TLS nginx site. Those nginx templates now serve the public website at `/` and keep `/api/` plus `/agent/ws` pointed at the relay. Schema migrations remain wired into `make deploy`, so every normal relay deploy keeps the database in sync. Website bundle deploys stay separate under `make deploy-website-dev` and `make deploy-website-prod`.

For the relay CLI command reference itself, see [operation.md](./operation.md).

## Hosted Relay Invariant

For any cloud or VPS-hosted relay, multi-tenant session isolation is non-negotiable:

- agent tokens are user-owned credentials, not one relay-wide shared secret
- each live session registered on `/agent/ws` is bound to that token's owning user
- `GET /api/sessions` must return only that user's sessions
- `GET /api/sessions/:id/attach/ws` must return `404 session_not_found` for another user's session
- one tenant must never learn that another tenant's session exists from discovery or attach behavior

## Prerequisites

- Ubuntu VPS with a public IP (port 80 open for dev; ports 80/443 open for production TLS)
- SSH access from dev machine, with passwordless `sudo` on the VPS because the install script uses `sudo -n`
- Go toolchain on dev machine (for cross-compilation)
- Node.js and npm on the dev machine when you run `make deploy-website-dev` or `make deploy-website-prod`
- A PostgreSQL role and database that match the DSN in your env file (for example role `relay_user`, database `agent_tunnel`). The `postgresql` package itself can be installed by `make install-dev` or `make install-prod`.

## Server Layout

```text
/usr/local/bin/relay                         # installed relay binary (used by systemd)
/usr/local/bin/relay-migrate     # installed migrator binary
~/relay                                      # uploaded staging binary from dev machine
~/relay-migrate                   # uploaded staging migrator binary
/etc/agentunnel/schema/                      # installed relay schema SQL files
/etc/systemd/system/agentunnel-relay.service # systemd unit
/etc/systemd/system/nginx.service.d/agentunnel-restart.conf # nginx auto-restart override
/etc/nginx/sites-available/<domain>          # nginx site config
/etc/nginx/conf.d/websocket_map.conf         # shared WebSocket header map
/var/www/agentunnel-website/current          # nginx docroot symlink served at /
/var/www/agentunnel-website/releases/        # versioned website releases
/var/www/certbot/                            # ACME HTTP-01 webroot used by certbot
/etc/letsencrypt/live/<domain>/              # TLS cert (managed by certbot)
/etc/letsencrypt/renewal-hooks/deploy/reload-nginx.sh # reload nginx after cert renewal
```

## Initial Setup

### 1. Bootstrap the host once

From the project root on your dev machine:

```bash
make install-dev
make install-prod
make install-prod INSTALL_CERTBOT_EMAIL=ops@example.com
```

Useful overrides:

- `INSTALL_HOST=user@1.2.3.4` to override the SSH destination for a single run
- `INSTALL_DEV_SERVER_NAMES="dev.example.com www.dev.example.com"` to replace the dev default `server_name`
- `INSTALL_PROD_SERVER_NAMES="example.com www.example.com"` and `INSTALL_PROD_PRIMARY_DOMAIN=example.com` to point production at a different domain set
- `INSTALL_NGINX_SITE_NAME=my-site` if you do not want the default site filename (`agentunnel-dev` on dev, the primary prod domain on prod)
- `INSTALL_WEBSITE_ROOT=/var/www/custom-site` to serve the public website from a different release root
- `INSTALL_CERTBOT_EMAIL=ops@example.com` to provide the required certbot email non-interactively
- `INSTALL_DRY_RUN=1` or `INSTALL_VERBOSE=1` for preview/debug output

What the install targets do:

- `make install-dev` installs `nginx` and PostgreSQL if they are missing, syncs `deploy/nginx/websocket_map.conf`, renders `deploy/nginx/agentunnel-http.conf.template`, installs `deploy/systemd/nginx-restart.conf`, enables PostgreSQL, and restarts nginx. The rendered site serves `INSTALL_WEBSITE_ROOT/current` at `/` and proxies `/api/` and `/agent/ws` to the relay.
- `make install-prod` first requires a certbot email. If `INSTALL_CERTBOT_EMAIL` is missing, it stops and prompts on stdin until you enter one. Then it performs the same base work, installs `certbot`, issues or refreshes the certificate with `certbot certonly --webroot`, installs `deploy/certbot/reload-nginx.sh`, enables `certbot.timer`, renders `deploy/nginx/agentunnel-tls.conf.template`, and restarts nginx again with TLS enabled. The TLS site serves `INSTALL_WEBSITE_ROOT/current` at `/` and keeps the relay proxy routes intact.

If a host was already bootstrapped before these nginx template changes landed, rerun `make install-dev` or `make install-prod` once so nginx starts serving the website root before you rely on `make deploy-website-dev` or `make deploy-website-prod`.

### 2. Build and upload the relay artifacts

From the project root on your dev machine:

```bash
make build-linux          # cross-compile for linux/amd64
scp bin/relay diarome:~/relay
scp bin/relay-migrate diarome:~/relay-migrate
ssh diarome 'rm -rf /tmp/agentunnel-relay-schema && mkdir -p /tmp/agentunnel-relay-schema'
scp schema/*.sql diarome:/tmp/agentunnel-relay-schema/
```

`diarome` here is an SSH config host alias. Replace with `user@your-vps-ip` if you don't have one.

### 3. Install the binaries and schema files on the VPS

```bash
sudo install -m 0755 ~/relay /usr/local/bin/relay
sudo install -m 0755 ~/relay-migrate /usr/local/bin/relay-migrate
sudo install -d -m 0755 /etc/agentunnel/schema
sudo install -m 0644 /tmp/agentunnel-relay-schema/*.sql /etc/agentunnel/schema/
```

### 4. Create the systemd service

The canonical unit file lives in the repo at `deploy/systemd/agentunnel-relay.service`. It reads `RELAY_LOG_FILE` from `/etc/agentunnel/relay.env`, and uses `LogsDirectory=agentunnel` so systemd creates `/var/log/agentunnel` with the right ownership. `make deploy-env` always writes `RELAY_LOG_FILE=/var/log/agentunnel/relay.log` into that env file. systemd unit-state messages and anything that still writes directly to stdout/stderr continue to flow through `journalctl`.

First, create a root-only env file at `/etc/agentunnel/relay.env`:

```bash
sudo install -d -m 0755 /etc/agentunnel
sudo tee /etc/agentunnel/relay.env >/dev/null <<'EOF'
RELAY_DATABASE_URL=postgres://relay_user:change-me-db-password@localhost/agent_tunnel?sslmode=disable
RELAY_APP_SECRET=<long-random-secret>
RELAY_OPERATOR_TOKEN=<long-random-operator-token>
EOF
sudo chmod 600 /etc/agentunnel/relay.env
```

Copy the repo's unit file to the VPS (one-time, or whenever the repo unit changes):

```bash
scp deploy/systemd/agentunnel-relay.service diarome:/tmp/
ssh diarome 'sudo install -m 0644 /tmp/agentunnel-relay.service /etc/systemd/system/agentunnel-relay.service && \
  sudo systemctl daemon-reload && sudo systemctl enable agentunnel-relay && rm /tmp/agentunnel-relay.service'
```

`make deploy` does not install the unit file; it is a rarely-changing piece of host config. Log rotation is not set up: at current traffic, `/var/log/agentunnel/relay.log` is expected to grow slowly. When it eventually becomes a concern, add a `/etc/logrotate.d/agentunnel-relay` config (weekly, 8 rotations, `copytruncate`) manually.

Enable and start:

```bash
sudo systemctl daemon-reload
sudo systemctl enable agentunnel-relay
sudo /usr/local/bin/relay-migrate --env-file /etc/agentunnel/relay.env --schema-dir /etc/agentunnel/schema
sudo systemctl start agentunnel-relay
```

Verify:

```bash
sudo systemctl status agentunnel-relay
# Should show active (running)

ss -lntp | grep 8586
# Should show relay listening on 127.0.0.1:8586
```

#### Systemd key settings explained

- **`Restart=always`** and **`RestartSec=5`**: If the relay crashes, systemd restarts it after 5 seconds. This keeps the relay self-healing without manual intervention.
- **`EnvironmentFile=/etc/agentunnel/relay.env`**: systemd reads this file at service start. `make deploy` uploads the selected local env file (`.env.prod` or `.env.dev`) to this same path and pins `RELAY_LOG_FILE=/var/log/agentunnel/relay.log`.
- **Run `relay-migrate` before restart**: schema changes are explicit and are not applied automatically by `relay serve`.
- **`After=network.target`**: Ensures the relay starts only after the network is up.
- **`WantedBy=multi-user.target`**: The service starts automatically on boot.

#### Viewing relay logs

Structured relay logs written through `logx` are appended to `/var/log/agentunnel/relay.log` when `RELAY_LOG_FILE` is set. systemd unit-state messages and any direct stdout/stderr output still go to journald.

```bash
# Live tail of the application log
sudo tail -f /var/log/agentunnel/relay.log

# Filter by structured event with jq
sudo tail -n 1000 /var/log/agentunnel/relay.log | jq 'select(.event == "relay_started")'

# systemd-level events (restarts, failures, etc.)
sudo journalctl -u agentunnel-relay -f
```

### 5. Nginx and TLS configuration

`make install-dev` and `make install-prod` automate the host bootstrap below. Keep the manual equivalent here for inspection and emergency repair.

Repo-controlled files:

- `deploy/nginx/websocket_map.conf` -> `/etc/nginx/conf.d/websocket_map.conf`
- `deploy/nginx/agentunnel-http.conf.template` -> rendered HTTP site on dev, and the production HTTP bootstrap site used for ACME challenges
- `deploy/nginx/agentunnel-tls.conf.template` -> rendered final production TLS site
- `deploy/systemd/nginx-restart.conf` -> `/etc/systemd/system/nginx.service.d/agentunnel-restart.conf`
- `deploy/certbot/reload-nginx.sh` -> `/etc/letsencrypt/renewal-hooks/deploy/reload-nginx.sh`

Important defaults:

- Dev uses `server_name _;` unless `INSTALL_DEV_SERVER_NAMES` overrides it. If the SSH host resolves to an IPv4 address, the script also adds that IP to the dev `server_name` list.
- Prod defaults to `INSTALL_PROD_SERVER_NAMES="diaro.me www.diaro.me"` and `INSTALL_PROD_PRIMARY_DOMAIN=diaro.me`.
- The nginx site serves the static website from `INSTALL_WEBSITE_ROOT/current` at `/` and proxies `/api/` plus `/agent/ws`. Operator control routes stay host-local.
- `proxy_read_timeout` and `proxy_send_timeout` are pinned to `86400s` for long-lived WebSocket sessions.

For a manual production certificate bootstrap, the repo now uses a webroot flow instead of the nginx plugin so the tracked nginx templates stay authoritative:

```bash
sudo install -d -m 0755 /var/www/certbot
sudo certbot certonly --webroot -w /var/www/certbot \
  --non-interactive --agree-tos --keep-until-expiring \
  -m your-email@example.com \
  -d example.com -d www.example.com
sudo install -m 0755 deploy/certbot/reload-nginx.sh /etc/letsencrypt/renewal-hooks/deploy/reload-nginx.sh
sudo systemctl enable --now certbot.timer
```

### 6. End-to-end verification

```bash
# Prod: should return 401 (bearer auth required)
curl -s -o /dev/null -w "%{http_code}" https://example.com/api/sessions

# Dev: same check over plain HTTP
curl -s -o /dev/null -w "%{http_code}" http://dev-host-or-ip/api/sessions

# Prod only: check cert expiry
sudo certbot certificates

# Prod only: check renewal timer is active
sudo systemctl list-timers | grep certbot

# Check relay is listening
ss -lntp | grep 8586

# Check relay service health
sudo systemctl is-active agentunnel-relay
```

## Deploying Updates

### Manual deploy

The manual flow below is a close approximation of `make deploy-prod`. Prefer the Make targets; keep this as a reference.

```bash
# On dev machine
make build-linux
scp bin/relay diarome:~/relay
scp bin/relay-migrate diarome:~/relay-migrate
ssh diarome 'rm -rf /tmp/agentunnel-relay-schema && mkdir -p /tmp/agentunnel-relay-schema'
scp schema/*.sql diarome:/tmp/agentunnel-relay-schema/
ssh diarome 'sudo install -m 0755 ~/relay-migrate /usr/local/bin/relay-migrate'
tmp_env="$(mktemp)"
trap 'rm -f "$tmp_env"' EXIT
grep -v '^RELAY_LOG_FILE=' .env.prod >"$tmp_env"
printf '\nRELAY_LOG_FILE=/var/log/agentunnel/relay.log\n' >>"$tmp_env"
scp "$tmp_env" diarome:/tmp/agentunnel-relay.env
ssh diarome 'sudo rm -rf /etc/agentunnel/schema && sudo install -d -m 0755 /etc/agentunnel/schema && sudo install -m 0644 /tmp/agentunnel-relay-schema/*.sql /etc/agentunnel/schema/'
ssh diarome 'sudo /usr/local/bin/relay-migrate --env-file /tmp/agentunnel-relay.env --schema-dir /etc/agentunnel/schema'
ssh diarome 'sudo install -m 0755 ~/relay /usr/local/bin/relay'
ssh diarome 'sudo install -d -m 0755 /etc/agentunnel && sudo install -m 0600 /tmp/agentunnel-relay.env /etc/agentunnel/relay.env && rm -f /tmp/agentunnel-relay.env'
ssh diarome 'sudo systemctl restart agentunnel-relay'
rm -f "$tmp_env"
trap - EXIT
```

### One-command deploy

The Makefile keeps the common path simple and supports a prod env and a dev env side-by-side via two separate env files (`.env.prod` and `.env.dev`). Both are gitignored. Each file carries its own `DEPLOY_HOST`, and the remote install targets default `INSTALL_HOST` to that same value.

`make deploy` now always reruns the migrator before it installs the new relay binary and env file. That is the safer default for this repo: the migrator records applied versions in `schema_migrations`, takes a PostgreSQL advisory lock to avoid concurrent runners, and executes each migration inside its own transaction. If the database is already current, rerunning the migrator is a no-op.

Relay deploy is intentionally limited in scope. It manages relay artifacts only:

- `relay` and `relay-migrate` binaries
- `schema/` synced to `/etc/agentunnel/schema`
- `/etc/agentunnel/relay.env`
- restart of `agentunnel-relay`

Relay deploy does not install or reconfigure `nginx`, `certbot`, or `postgresql`, and it does not rewrite those host-level config files. Keep those changes in `make install-dev`, `make install-prod`, or manual host administration.

Core targets:

- `make install-dev` installs `nginx` and PostgreSQL if they are missing, syncs the HTTP nginx site for the dev VPS, and restarts nginx.
- `make install-prod` does the same for production, plus `certbot`, certificate issuance/refresh, the renewal hook, and the final TLS nginx site.
- `make deploy` builds, syncs schema, safely reruns migrations, installs the selected relay binary and env file, and restarts the relay. `ENV_FILE` defaults to `.env.prod`.
- `make deploy-website` runs `npm ci`, builds `../agent-tunnel-website`, rejects bundle symlinks, uploads a release under `DEPLOY_WEBSITE_ROOT/releases`, and atomically repoints `DEPLOY_WEBSITE_ROOT/current`. `DEPLOY_WEBSITE_HOST` defaults to `DEPLOY_HOST`.
- `make deploy-install` only uploads and installs binaries whose sha256 differs from the copy already at the installed path on the remote, so repeat runs on unchanged builds are cheap no-ops.
- `make deploy-env` uploads the selected local env file to `/etc/agentunnel/relay.env` and pins `RELAY_LOG_FILE=/var/log/agentunnel/relay.log`.
- `make deploy-schema` mirrors the local `schema/` directory to `/etc/agentunnel/schema` on the remote host so removed SQL files do not linger forever.
- `make deploy-migrate` streams the selected local `$(ENV_FILE)` to the remote host temporarily, runs `relay-migrate`, and cleans up the temp file before returning.
- `make deploy-schema-migrate` is the combined “sync schema + rerun migrator” step.
- `make deploy-restart` restarts the `agentunnel-relay` systemd unit.

Convenience targets:

- `make deploy-dev` → `make deploy ENV_FILE=.env.dev`
- `make deploy-prod` → `make deploy ENV_FILE=.env.prod`
- `make deploy-website-dev` → `make deploy-website ENV_FILE=.env.dev`
- `make deploy-website-prod` → `make deploy-website ENV_FILE=.env.prod`

Typical flows:

```bash
make install-prod                                         # one-time prod bootstrap; prompts for certbot email
make install-prod INSTALL_CERTBOT_EMAIL=ops@example.com   # same bootstrap with the email provided up front
make install-dev                                          # one-time dev bootstrap
make deploy-prod        # prod deploy
make deploy-dev         # dev deploy
make deploy-website-prod # prod website deploy
make deploy-website-dev  # dev website deploy
make install-dev INSTALL_DRY_RUN=1   # preview dev bootstrap without changing the VPS
make deploy-dev DEPLOY_DRY_RUN=1    # structured preview without making changes
make deploy-website-dev DEPLOY_DRY_RUN=1  # preview website deploy without making changes
make deploy-dev DEPLOY_VERBOSE=1    # include deploy debug details
make deploy-install     # just refresh relay + migrator binaries if needed
```

Notes:

- `make install-prod` always requires a certbot email before it continues. Pass `INSTALL_CERTBOT_EMAIL=<ops@example.com>` to avoid the interactive prompt.
- For a readable preview of host bootstrap, prefer `make install-dev INSTALL_DRY_RUN=1` or `make install-prod INSTALL_DRY_RUN=1`.
- `make -n deploy-dev` is still a Make-level dry-run, but it now only prints the deploy script entrypoint.
- For a readable preview of the actual relay or website deploy plan, prefer `make deploy-dev DEPLOY_DRY_RUN=1`, `make deploy-prod DEPLOY_DRY_RUN=1`, or `make deploy-website-dev DEPLOY_DRY_RUN=1`.
- GNU Make does not support a custom `--verbose` flag for project-defined behavior. For verbose deploy logs, use `DEPLOY_VERBOSE=1` on the same `make deploy-dev`, `make deploy-prod`, or `make deploy-website-dev` command you would normally run.

You can still override individual variables or run steps one at a time:

```bash
make deploy ENV_FILE=.env.dev DEPLOY_HOST=user@1.2.3.4
make deploy-migrate ENV_FILE=.env.dev
```

### What happens during restart

The relay is stateless and in-memory. A restart means:

- All active agent sessions are dropped
- All attached clients are disconnected
- Agents will automatically reconnect and re-register their sessions (the connector has built-in retry with backoff)
- Clients need to re-attach after the agent reconnects

This is a brief interruption (typically under 5 seconds), not account-data loss. Auth state lives in PostgreSQL; only the live in-memory session graph is rebuilt after restart.

## Operations

### Common commands

```bash
# Service lifecycle
sudo systemctl status agentunnel-relay
sudo systemctl restart agentunnel-relay
sudo systemctl stop agentunnel-relay

# Relay migrations and operator workflows (run on the relay host)
sudo /usr/local/bin/relay-migrate --env-file /etc/agentunnel/relay.env --schema-dir /etc/agentunnel/schema
sudo /usr/local/bin/relay-migrate --env-file /etc/agentunnel/relay.env --schema-dir /etc/agentunnel/schema --baseline 0002_operator_audit.sql
sudo /bin/sh -lc 'set -a && . /etc/agentunnel/relay.env && set +a && /usr/local/bin/relay invite create --count 5 --expires-in 7d'
sudo /bin/sh -lc 'set -a && . /etc/agentunnel/relay.env && set +a && /usr/local/bin/relay invite disable --code AB2C3D'
sudo /bin/sh -lc 'set -a && . /etc/agentunnel/relay.env && set +a && /usr/local/bin/relay user delete --username alice'

# Live logs
sudo journalctl -u agentunnel-relay -f

# Recent logs
sudo journalctl -u agentunnel-relay --since "1 hour ago"

# Check what is listening on the relay port
ss -lntp | grep 8586

# nginx config test
sudo nginx -t

# Reload nginx (after config changes)
sudo systemctl reload nginx
```

## Things to Pay Attention To

### Security

- **The relay listens on `127.0.0.1` only.** All external access goes through nginx. Do not change this to `0.0.0.0` unless you have a specific reason and a firewall in place.
- **Keep operator routes off the public proxy surface.** nginx should serve the website on `/`, proxy only `/api/` and `/agent/ws` into the relay, and leave the operator control paths outside the public proxy surface.
- **Use a strong app secret.** `RELAY_APP_SECRET` should be a long random string in production.
- **Use a strong operator token.** `RELAY_OPERATOR_TOKEN` protects the local-only operator control path that `relay invite ...` and `relay user delete` call.
- **Agent tokens are user-owned.** Users create long-lived agent tokens through the app APIs; operators do not preload one shared relay-wide token anymore.

### TLS / Certificates

Production TLS termination now lives inside the install flow. `make install-prod` installs `certbot` if needed, obtains or refreshes the certificate with the webroot challenge, enables `certbot.timer`, and installs the nginx reload hook used after renewal. When you add a new relay domain, update `INSTALL_PROD_SERVER_NAMES` and `INSTALL_PROD_PRIMARY_DOMAIN`, rerun `make install-prod INSTALL_CERTBOT_EMAIL=...`, and then continue with the normal deploy targets.

### WebSocket Stability

- **nginx proxy timeouts** directly control how long idle WebSocket connections survive. The current config uses 24-hour timeouts. If you see agent or client disconnects after periods of inactivity, check these values first.
- **Some cloud providers or firewalls** have their own idle connection timeouts (often 5-10 minutes). If you see regular disconnects at a fixed interval, investigate upstream network infrastructure, not just nginx.

### Relay Restarts

- The relay holds no persistent state. Restarting it is safe but causes a brief disruption.
- Agents reconnect automatically. Clients need to re-attach.
- If you need zero-downtime deploys in the future, you would need a process manager that supports socket handoff, which is out of scope for the current architecture.

### Monitoring

Minimal monitoring checklist:

- **Is the relay process running?** `systemctl is-active agentunnel-relay`
- **Is nginx healthy?** `systemctl is-active nginx`
- **Can a client reach the relay?** `curl -s -o /dev/null -w "%{http_code}" https://your-domain/api/sessions` should return `401`

### Disk Space

- Application logs accumulate in `/var/log/agentunnel/relay.log`. Add logrotate when that file starts to matter.
- systemd unit-state logs still accumulate in journald. If the VPS has limited disk, configure journal size limits in `/etc/systemd/journald.conf` (`SystemMaxUse=200M`).
- Certbot keeps old cert versions in `/etc/letsencrypt/archive/`. These are small but can be cleaned with `sudo certbot delete --cert-name <domain>` if you rotate domains.

## Connecting tunnel to the Deployed Relay

From your dev machine:

```bash
export TUNNEL_BASE_URL=https://diaro.me
export TUNNEL_AUTH_TOKEN=<your-agent-token>
./bin/tunnel claude
```

`TUNNEL_BASE_URL` is optional when you use `https://diaro.me`, because that is the built-in default. For local or staging relays, point it at the full `http://` or `https://` base URL.
