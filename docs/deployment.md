# Relay Deployment & Operations

This guide covers deploying the relay server behind nginx with TLS on an Ubuntu VPS, installing PostgreSQL for durable auth state, automating binary deploys from a dev machine, and day-to-day operations.

For the relay CLI command reference itself, see [operation.md](./operation.md).

## Prerequisites

- Ubuntu VPS with a public IP and ports 80/443 open
- A domain with DNS A-record pointing to the VPS IP
- SSH access from dev machine (passwordless recommended)
- Go toolchain on dev machine (for cross-compilation)
- PostgreSQL installed on the VPS

## Server Layout

```text
/usr/local/bin/relay                         # installed relay binary (used by systemd)
/usr/local/bin/agentunnel-relay-migrate     # installed migrator binary
~/relay                                      # uploaded staging binary from dev machine
~/agentunnel-relay-migrate                   # uploaded staging migrator binary
/etc/agentunnel/schema/relay/                # installed relay schema SQL files
/etc/systemd/system/agentunnel-relay.service # systemd unit
/etc/nginx/sites-available/<domain>          # nginx site config
/etc/nginx/conf.d/websocket_map.conf         # shared WebSocket header map
/etc/letsencrypt/live/<domain>/              # TLS cert (managed by certbot)
```

## Initial Setup

### 1. Install nginx

```bash
sudo apt update
sudo apt install -y nginx
```

Verify nginx is running:

```bash
sudo systemctl status nginx
curl -s -o /dev/null -w "%{http_code}" http://localhost
# Should return 200 (default welcome page)
```

### 1.5 Install PostgreSQL

```bash
sudo apt update
sudo apt install -y postgresql
sudo systemctl enable postgresql
sudo systemctl start postgresql

sudo -u postgres createdb agent_tunnel
```

### 2. Install certbot via snap

Snap is the recommended install method for certbot. It keeps certbot up to date automatically and includes a built-in renewal timer.

```bash
sudo snap install --classic certbot
sudo ln -s /snap/bin/certbot /usr/bin/certbot
certbot --version
```

The symlink ensures `certbot` is available system-wide. If `/usr/bin/certbot` already exists from a previous apt install, remove the old package first:

```bash
sudo apt remove -y certbot   # only if previously installed via apt
sudo snap install --classic certbot
sudo ln -sf /snap/bin/certbot /usr/bin/certbot
```

### 3. Build and upload the relay artifacts

From the project root on your dev machine:

```bash
make build-linux          # cross-compile for linux/amd64
scp bin/relay diarome:~/relay
scp bin/agentunnel-relay-migrate diarome:~/agentunnel-relay-migrate
ssh diarome 'rm -rf /tmp/agentunnel-relay-schema && mkdir -p /tmp/agentunnel-relay-schema'
scp schema/relay/*.sql diarome:/tmp/agentunnel-relay-schema/
```

`diarome` here is an SSH config host alias. Replace with `user@your-vps-ip` if you don't have one.

### 4. Install the binaries and schema files on the VPS

```bash
sudo install -m 0755 ~/relay /usr/local/bin/relay
sudo install -m 0755 ~/agentunnel-relay-migrate /usr/local/bin/agentunnel-relay-migrate
sudo install -d -m 0755 /etc/agentunnel/schema/relay
sudo install -m 0644 /tmp/agentunnel-relay-schema/*.sql /etc/agentunnel/schema/relay/
```

### 5. Create the systemd service

First, create a root-only env file at `/etc/agentunnel/relay.env`:

```bash
sudo install -d -m 0755 /etc/agentunnel
sudo tee /etc/agentunnel/relay.env >/dev/null <<'EOF'
RELAY_DATABASE_URL=postgres://localhost/agent_tunnel?sslmode=disable
RELAY_APP_SECRET=<long-random-secret>
RELAY_OPERATOR_TOKEN=<long-random-operator-token>
EOF
sudo chmod 600 /etc/agentunnel/relay.env
```

Create `/etc/systemd/system/agentunnel-relay.service`:

```ini
[Unit]
Description=Agent Tunnel Relay
After=network.target postgresql.service

[Service]
Type=simple
User=root
Group=root
ExecStart=/usr/local/bin/relay serve --listen-addr 127.0.0.1:8586
EnvironmentFile=/etc/agentunnel/relay.env
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

Enable and start:

```bash
sudo systemctl daemon-reload
sudo systemctl enable agentunnel-relay
sudo /bin/sh -lc 'set -a && . /etc/agentunnel/relay.env && set +a && /usr/local/bin/agentunnel-relay-migrate --schema-dir /etc/agentunnel/schema/relay'
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
- **`EnvironmentFile=/etc/agentunnel/relay.env`**: systemd and `make deploy` both read the same source of truth for relay environment variables.
- **Run `agentunnel-relay-migrate` before restart**: schema changes are explicit and are not applied automatically by `relay serve`.
- **`After=network.target`**: Ensures the relay starts only after the network is up.
- **`WantedBy=multi-user.target`**: The service starts automatically on boot.

#### Viewing relay logs

Relay logs go to journald (systemd's log system):

```bash
# Live tail
sudo journalctl -u agentunnel-relay -f

# Recent logs
sudo journalctl -u agentunnel-relay --since "1 hour ago"

# Filter by event with jq
sudo journalctl -u agentunnel-relay -o cat | jq 'select(.event == "relay_started")'
```

### 6. Configure nginx

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

### 7. Obtain TLS certificate

```bash
sudo certbot --nginx \
  -d example.com -d www.example.com \
  --non-interactive --agree-tos \
  -m your-email@example.com
```

Certbot will:
- Obtain the cert from Let's Encrypt
- Modify the nginx config to add SSL directives and HTTP-to-HTTPS redirect
- The snap-installed certbot includes a built-in renewal timer (`snap.certbot.renew.timer`)

Verify the cert was issued and HTTPS works:

```bash
# Should return 401 (bearer auth required) over HTTPS
curl -s -o /dev/null -w "%{http_code}" https://example.com/api/sessions

# Check cert details
sudo certbot certificates
```

### 8. Verify automatic certificate renewal

Snap-installed certbot ships with `snap.certbot.renew.timer`, a systemd timer that checks for renewal twice daily. No cron job is needed.

Verify the timer is active:

```bash
sudo systemctl list-timers | grep certbot
```

You should see `snap.certbot.renew.timer` listed with a next-run time.

How automatic renewal works:

- Let's Encrypt certificates expire after **90 days**
- Certbot renews when a cert has **fewer than 30 days remaining**, so renewal effectively happens every ~60 days
- The timer runs **twice daily** but only triggers actual renewal when needed
- On successful renewal, certbot reloads nginx automatically to pick up the new cert

Test that renewal would succeed (dry run, does not actually renew):

```bash
sudo certbot renew --dry-run
```

If the dry run fails, common causes are:

- Port 80 is blocked by a firewall (Let's Encrypt needs to reach it for HTTP-01 challenges)
- nginx is misconfigured or not running
- DNS no longer points to this server

#### Monitoring cert expiry

```bash
# Check all managed certs and their expiry dates
sudo certbot certificates

# One-liner to check days until expiry
sudo openssl x509 -in /etc/letsencrypt/live/<domain>/fullchain.pem -noout -dates
```

### 9. End-to-end verification

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

```bash
# On dev machine
make build-linux
scp bin/relay diarome:~/relay
scp bin/agentunnel-relay-migrate diarome:~/agentunnel-relay-migrate
ssh diarome 'rm -rf /tmp/agentunnel-relay-schema && mkdir -p /tmp/agentunnel-relay-schema'
scp schema/relay/*.sql diarome:/tmp/agentunnel-relay-schema/
ssh diarome 'sudo install -m 0755 ~/relay /usr/local/bin/relay'
ssh diarome 'sudo install -m 0755 ~/agentunnel-relay-migrate /usr/local/bin/agentunnel-relay-migrate'
ssh diarome 'sudo mkdir -p /etc/agentunnel/schema/relay && sudo install -m 0644 /tmp/agentunnel-relay-schema/*.sql /etc/agentunnel/schema/relay/'
ssh diarome 'sudo /bin/sh -lc '"'"'set -a && . /etc/agentunnel/relay.env && set +a && /usr/local/bin/agentunnel-relay-migrate --schema-dir /etc/agentunnel/schema/relay'"'"''
ssh diarome 'sudo systemctl restart agentunnel-relay'
```

### One-command deploy

The Makefile keeps the common path simple:

- `make deploy` builds, uploads, installs, and restarts the relay
- `make deploy-schema` uploads `schema/relay/*.sql` to the remote host
- `make deploy-migrate` runs `agentunnel-relay-migrate` separately when a release actually changes the PostgreSQL schema

```bash
make deploy
```

Override defaults for different environments:

```bash
make deploy DEPLOY_HOST=staging DEPLOY_RELAY_PATH=~/relay DEPLOY_SERVICE=agentunnel-relay
make deploy-migrate DEPLOY_HOST=staging DEPLOY_ENV_FILE=/etc/agentunnel/relay.env
```

When a release includes a migration, run the explicit sequence:

```bash
make deploy-install
make deploy-schema
make deploy-migrate
make deploy-restart
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
sudo /bin/sh -lc 'set -a && . /etc/agentunnel/relay.env && set +a && /usr/local/bin/agentunnel-relay-migrate --schema-dir /etc/agentunnel/schema/relay'
sudo /bin/sh -lc 'set -a && . /etc/agentunnel/relay.env && set +a && /usr/local/bin/agentunnel-relay-migrate --schema-dir /etc/agentunnel/schema/relay --baseline 0002_operator_audit.sql'
sudo /bin/sh -lc 'set -a && . /etc/agentunnel/relay.env && set +a && /usr/local/bin/relay invite create --count 5 --expires-in 7d'
sudo /bin/sh -lc 'set -a && . /etc/agentunnel/relay.env && set +a && /usr/local/bin/relay invite disable --code AB2C3D'
sudo /bin/sh -lc 'set -a && . /etc/agentunnel/relay.env && set +a && /usr/local/bin/relay user delete --username alice'

# Live logs
sudo journalctl -u agentunnel-relay -f

# Recent logs
sudo journalctl -u agentunnel-relay --since "1 hour ago"

# Check what is listening on the relay port
ss -lntp | grep 8586

# TLS cert status
sudo certbot certificates

# Test renewal (does not actually renew)
sudo certbot renew --dry-run

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

- **Cert renewal is automatic** but can fail silently if port 80 is blocked or nginx is misconfigured. Check `sudo certbot renew --dry-run` periodically, especially after nginx config changes.
- **Do not edit the `# managed by Certbot` lines** in the nginx config. Certbot needs them to locate its own directives during renewal.
- **If you change the domain**, you need a new cert: `sudo certbot --nginx -d new-domain.com`.

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
- **Is the cert valid?** `sudo certbot certificates` (check expiry date)
- **Can a client reach the relay?** `curl -s -o /dev/null -w "%{http_code}" https://your-domain/api/sessions` should return `401`

### Disk Space

- Relay logs accumulate in journald. If the VPS has limited disk, configure journal size limits in `/etc/systemd/journald.conf` (`SystemMaxUse=200M`).
- Certbot keeps old cert versions in `/etc/letsencrypt/archive/`. These are small but can be cleaned with `sudo certbot delete --cert-name <domain>` if you rotate domains.

## Connecting tunnel to the Deployed Relay

From your dev machine:

```bash
export AGENTUNNEL_RELAY_ADDR=diaro.me:443
export AGENTUNNEL_RELAY_TOKEN=<your-agent-token>
./bin/tunnel claude
```

Note: when connecting through nginx with TLS, use port `443` (the HTTPS port), not `8586` (the internal port). The relay address should be the domain name, not the VPS IP.
