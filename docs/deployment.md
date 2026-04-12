# Relay Deployment & Operations

This guide covers deploying the relay server on an Ubuntu VPS, automating binary deploys and schema migrations from a dev machine, and day-to-day operations.

nginx, certbot, and PostgreSQL are treated as pre-installed infrastructure; installing and configuring them is out of scope for this guide. Only the relay-specific nginx site config, the relay systemd unit, and the schema migration step are documented below. Schema migrations are wired into `make deploy`, so every normal deploy keeps the database in sync.

For the relay CLI command reference itself, see [operation.md](./operation.md).

## Prerequisites

- Ubuntu VPS with a public IP (ports 80/443 open if you intend to serve the relay over HTTPS)
- SSH access from dev machine (passwordless recommended)
- Go toolchain on dev machine (for cross-compilation)
- nginx already installed and running on the VPS
- certbot already installed on the VPS if you plan to terminate TLS there
- PostgreSQL already installed and running on the VPS, with a role and database that match the DSN in your env file (e.g. role `relay_user`, database `agent_tunnel`)

## Server Layout

```text
/usr/local/bin/relay                         # installed relay binary (used by systemd)
/usr/local/bin/relay-migrate     # installed migrator binary
~/relay                                      # uploaded staging binary from dev machine
~/relay-migrate                   # uploaded staging migrator binary
/etc/agentunnel/schema/                      # installed relay schema SQL files
/etc/systemd/system/agentunnel-relay.service # systemd unit
/etc/nginx/sites-available/<domain>          # nginx site config
/etc/nginx/conf.d/websocket_map.conf         # shared WebSocket header map
/etc/letsencrypt/live/<domain>/              # TLS cert (managed by certbot)
```

## Initial Setup

### 1. Build and upload the relay artifacts

From the project root on your dev machine:

```bash
make build-linux          # cross-compile for linux/amd64
scp bin/relay diarome:~/relay
scp bin/relay-migrate diarome:~/relay-migrate
ssh diarome 'rm -rf /tmp/agentunnel-relay-schema && mkdir -p /tmp/agentunnel-relay-schema'
scp schema/*.sql diarome:/tmp/agentunnel-relay-schema/
```

`diarome` here is an SSH config host alias. Replace with `user@your-vps-ip` if you don't have one.

### 2. Install the binaries and schema files on the VPS

```bash
sudo install -m 0755 ~/relay /usr/local/bin/relay
sudo install -m 0755 ~/relay-migrate /usr/local/bin/relay-migrate
sudo install -d -m 0755 /etc/agentunnel/schema
sudo install -m 0644 /tmp/agentunnel-relay-schema/*.sql /etc/agentunnel/schema/
```

### 3. Create the systemd service

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

### 4. Configure the nginx site for the relay

nginx itself is assumed to be installed and running. The steps below add only the relay-specific site config.

Reference configs are checked into `deploy/nginx/`. Copy them to the VPS:

```bash
scp deploy/nginx/websocket_map.conf diarome:/tmp/
scp deploy/nginx/diaro.me diarome:/tmp/
ssh diarome 'sudo mv /tmp/websocket_map.conf /etc/nginx/conf.d/ && sudo mv /tmp/diaro.me /etc/nginx/sites-available/'
```

Or create them manually. First, add the WebSocket map in `/etc/nginx/conf.d/websocket_map.conf`:

```nginx
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}
```

This conditionally sets the `Connection` header: `upgrade` when the client sends an `Upgrade` header (WebSocket), `close` otherwise (plain HTTP). Without the map, you would have to hardcode `Connection "upgrade"` on every request, which works but is technically incorrect for non-WebSocket routes.

Then create `/etc/nginx/sites-available/<domain>` with an HTTP-only config (certbot will add TLS in the next step):

```nginx
server {
    listen 80;
    listen [::]:80;
    server_name example.com www.example.com;

    location = /agent/ws {
        proxy_pass http://127.0.0.1:8586;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 86400s;
        proxy_send_timeout 86400s;
    }

    location /api/ {
        proxy_pass http://127.0.0.1:8586;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 86400s;
        proxy_send_timeout 86400s;
    }

    location / {
        return 404;
    }
}
```

Enable the site and reload:

```bash
sudo ln -sf /etc/nginx/sites-available/<domain> /etc/nginx/sites-enabled/<domain>
sudo rm -f /etc/nginx/sites-enabled/default   # remove default site if present
sudo nginx -t && sudo systemctl reload nginx
```

Key nginx settings to pay attention to:

- **`proxy_read_timeout 86400s`** and **`proxy_send_timeout 86400s`**: WebSocket connections (agent `/agent/ws` and client `/api/sessions/:id/attach/ws`) are long-lived. The default 60s timeout will kill them. Set these to at least 24 hours.
- **Only proxy `/api/` and `/agent/ws`**: operator control routes live outside `/api/` and must stay host-local. Returning `404` for everything else prevents accidental public exposure.
- **`Upgrade` and `Connection` headers with `map`**: The `$connection_upgrade` variable (from `websocket_map.conf`) ensures `Connection: upgrade` is only sent for actual WebSocket requests. Without the map and these headers, WebSocket connections fail silently.
- **`proxy_http_version 1.1`**: WebSocket requires HTTP/1.1. nginx defaults to 1.0 for upstream connections.

### 5. Issue a TLS cert for the relay domain (optional)

certbot itself is assumed to be installed. If this host needs a new cert for the relay domain, issue it with the nginx plugin:

```bash
sudo certbot --nginx \
  -d example.com -d www.example.com \
  --non-interactive --agree-tos \
  -m your-email@example.com
```

If certbot already manages this domain, skip this step. Cert renewal is handled by the existing certbot installation.

### 6. End-to-end verification

```bash
# Should return 401 (bearer auth required)
curl -s -o /dev/null -w "%{http_code}" https://example.com/api/sessions

# Check cert expiry
sudo certbot certificates

# Check renewal timer is active
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

The Makefile keeps the common path simple and supports a prod env and a dev env side-by-side via two separate env files (`.env.prod` and `.env.dev`). Both are gitignored. The env file also carries `DEPLOY_HOST` for that environment, so the `make` targets below pick up the right host automatically.

`make deploy` now always reruns the migrator before it installs the new relay binary and env file. That is the safer default for this repo: the migrator records applied versions in `schema_migrations`, takes a PostgreSQL advisory lock to avoid concurrent runners, and executes each migration inside its own transaction. If the database is already current, rerunning the migrator is a no-op.

Core targets:

- `make deploy` builds, syncs schema, safely reruns migrations, installs the selected relay binary and env file, and restarts the relay. `ENV_FILE` defaults to `.env.prod`.
- `make deploy-install` only uploads and installs binaries whose sha256 differs from the copy already at the installed path on the remote, so repeat runs on unchanged builds are cheap no-ops.
- `make deploy-env` uploads the selected local env file to `/etc/agentunnel/relay.env` and pins `RELAY_LOG_FILE=/var/log/agentunnel/relay.log`.
- `make deploy-schema` mirrors the local `schema/` directory to `/etc/agentunnel/schema` on the remote host so removed SQL files do not linger forever.
- `make deploy-migrate` streams the selected local `$(ENV_FILE)` to the remote host temporarily, runs `relay-migrate`, and cleans up the temp file before returning.
- `make deploy-schema-migrate` is the combined “sync schema + rerun migrator” step.
- `make deploy-restart` restarts the `agentunnel-relay` systemd unit.

Convenience targets:

- `make deploy-dev` → `make deploy ENV_FILE=.env.dev`
- `make deploy-prod` → `make deploy ENV_FILE=.env.prod`

Typical flows:

```bash
make deploy-prod        # prod deploy
make deploy-dev         # dev deploy
make deploy-dev DEPLOY_DRY_RUN=1    # structured preview without making changes
make deploy-dev DEPLOY_VERBOSE=1    # include deploy debug details
make deploy-install     # just refresh relay + migrator binaries if needed
```

Notes:

- `make -n deploy-dev` is still a Make-level dry-run, but it now only prints the deploy script entrypoint.
- For a readable preview of the actual deploy plan, prefer `make deploy-dev DEPLOY_DRY_RUN=1` or `make deploy-prod DEPLOY_DRY_RUN=1`.
- GNU Make does not support a custom `--verbose` flag for project-defined behavior. For verbose deploy logs, use `DEPLOY_VERBOSE=1` on the same `make deploy-dev` or `make deploy-prod` command you would normally run.

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
- **Keep operator routes off the public proxy surface.** nginx should proxy only `/api/` and `/agent/ws`. The operator control paths stay outside `/api/`, and relay also rejects proxied operator requests.
- **Use a strong app secret.** `RELAY_APP_SECRET` should be a long random string in production.
- **Use a strong operator token.** `RELAY_OPERATOR_TOKEN` protects the local-only operator control path that `relay invite ...` and `relay user delete` call.
- **Agent tokens are user-owned.** Users create long-lived agent tokens through the app APIs; operators do not preload one shared relay-wide token anymore.

### TLS / Certificates

TLS termination uses the existing nginx + certbot install on the VPS; cert issuance and renewal are managed outside this deployment flow. When adding a new relay domain, issue a cert with `sudo certbot --nginx -d new-domain.com` as a one-off step and then run the normal deploy targets.

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
