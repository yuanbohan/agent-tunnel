# Relay Deployment & Operations

This guide covers deploying the relay server behind nginx with TLS on an Ubuntu VPS, automating binary deploys from a dev machine, and day-to-day operations.

## Prerequisites

- Ubuntu VPS with a public IP and ports 80/443 open
- A domain with DNS A-record pointing to the VPS IP
- SSH access from dev machine (passwordless recommended)
- nginx and certbot installed on VPS
- Go toolchain on dev machine (for cross-compilation)

## Server Layout

```text
/root/relay                                  # relay binary
/etc/systemd/system/agentunnel-relay.service # systemd unit
/etc/nginx/sites-available/<domain>          # nginx site config
/etc/letsencrypt/live/<domain>/              # TLS cert (managed by certbot)
```

## Initial Setup

### 1. Build and upload the binary

From the project root on your dev machine:

```bash
make build-linux          # cross-compile for linux/amd64
scp bin/relay relay:~/relay
```

`relay` here is an SSH config host alias. Replace with `user@your-vps-ip` if you don't have one.

### 2. Create the systemd service

On the VPS, create `/etc/systemd/system/agentunnel-relay.service`:

First, create a dedicated system user and install the binary:

```bash
sudo useradd --system --no-create-home --shell /usr/sbin/nologin agentunnel || true
sudo install -m 0755 ~/relay /usr/local/bin/relay
```

Then create `/etc/systemd/system/agentunnel-relay.service`:

```ini
[Unit]
Description=Agent Tunnel Relay
After=network.target

[Service]
Type=simple
User=agentunnel
Group=agentunnel
ExecStart=/usr/local/bin/relay --port 8586
Environment=AGENTUNNEL_BASIC_USER=<user>
Environment=AGENTUNNEL_BASIC_PASSWORD=<password>
Environment=AGENTUNNEL_AGENT_TOKEN=<token>
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

Then enable and start:

```bash
sudo systemctl daemon-reload
sudo systemctl enable agentunnel-relay
sudo systemctl start agentunnel-relay
```

### 3. Configure nginx

Reference configs are checked into `deploy/nginx/`. Copy them to the VPS:

```bash
scp deploy/nginx/websocket_map.conf relay:/tmp/
scp deploy/nginx/diaro.dev relay:/tmp/
ssh relay 'sudo mv /tmp/websocket_map.conf /etc/nginx/conf.d/ && sudo mv /tmp/diaro.dev /etc/nginx/sites-available/'
```

Or create them manually. First, add the WebSocket map in `/etc/nginx/conf.d/websocket_map.conf`:

```nginx
map $http_upgrade $connection_upgrade {
    default upgrade;
    ''      close;
}
```

This conditionally sets the `Connection` header: `upgrade` when the client sends an `Upgrade` header (WebSocket), `close` otherwise (plain HTTP). Without the map, you would have to hardcode `Connection "upgrade"` on every request, which works but is technically incorrect for non-WebSocket routes.

Then create `/etc/nginx/sites-available/<domain>` with an HTTP-only config (certbot will add TLS):

```nginx
server {
    listen 80;
    listen [::]:80;
    server_name example.com www.example.com;

    location / {
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
}
```

Enable the site and reload:

```bash
sudo ln -sf /etc/nginx/sites-available/<domain> /etc/nginx/sites-enabled/<domain>
sudo nginx -t && sudo systemctl reload nginx
```

Key nginx settings to pay attention to:

- **`proxy_read_timeout 86400s`** and **`proxy_send_timeout 86400s`**: WebSocket connections (agent `/agent/ws` and client `/api/sessions/:id/attach/ws`) are long-lived. The default 60s timeout will kill them. Set these to at least 24 hours.
- **`Upgrade` and `Connection` headers with `map`**: The `$connection_upgrade` variable (from `websocket_map.conf`) ensures `Connection: upgrade` is only sent for actual WebSocket requests. Without the map and these headers, WebSocket connections fail silently.
- **`proxy_http_version 1.1`**: WebSocket requires HTTP/1.1. nginx defaults to 1.0 for upstream connections.

### 4. Obtain TLS certificate

```bash
sudo certbot --nginx \
  -d example.com -d www.example.com \
  --non-interactive --agree-tos \
  -m your-email@example.com
```

Certbot will:
- Obtain the cert from Let's Encrypt
- Modify the nginx config to add SSL directives and HTTP-to-HTTPS redirect
- Install a systemd renewal timer (commonly `certbot.timer` or `snap.certbot.renew.timer`, depending on install method) that checks for renewal twice daily

Certs expire after 90 days. Certbot renews them when they have fewer than 30 days remaining, so renewal effectively happens every ~60 days with no manual intervention.

### 5. Verify

```bash
# Should return 401 (auth required)
curl -s -o /dev/null -w "%{http_code}" https://example.com/api/sessions

# Check cert expiry
sudo certbot certificates

# Check renewal timer is active
sudo systemctl list-timers | grep certbot
```

## Deploying Updates

### Manual deploy

```bash
# On dev machine
make build-linux
scp bin/relay relay:~/relay
ssh relay 'sudo install -m 0755 ~/relay /usr/local/bin/relay'
ssh relay 'sudo systemctl restart agentunnel-relay'
```

### One-command deploy

The Makefile includes a `deploy` target with configurable variables:

```bash
make deploy
```

Override defaults for different environments:

```bash
make deploy DEPLOY_HOST=staging DEPLOY_RELAY_PATH=~/relay DEPLOY_SERVICE=agentunnel-relay
```

### What happens during restart

The relay is stateless and in-memory. A restart means:

- All active agent sessions are dropped
- All attached clients are disconnected
- Agents will automatically reconnect and re-register their sessions (the connector has built-in retry with backoff)
- Clients need to re-attach after the agent reconnects

This is a brief interruption (typically under 5 seconds), not data loss. There is no persistent state to migrate.

## Operations

### Common commands

```bash
# Service lifecycle
sudo systemctl status agentunnel-relay
sudo systemctl restart agentunnel-relay
sudo systemctl stop agentunnel-relay

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

### Log format

The relay emits structured JSON logs to stderr (captured by journald):

```json
{"ts":"...","level":"INFO","event":"relay_started","listen_addr":"127.0.0.1:8586"}
```

Filter by event with `jq`:

```bash
sudo journalctl -u agentunnel-relay -o cat | jq 'select(.event == "relay_started")'
```

## Things to Pay Attention To

### Security

- **Credentials in the systemd unit are visible** to anyone who can read the service file or run `systemctl show`. For production use, consider using `EnvironmentFile=/etc/agentunnel/env` with restricted permissions (`chmod 600`) instead of inline `Environment=` directives.
- **The relay listens on `127.0.0.1` only.** All external access goes through nginx. Do not change this to `0.0.0.0` unless you have a specific reason and a firewall in place.
- **Use strong credentials.** `AGENTUNNEL_BASIC_USER`, `AGENTUNNEL_BASIC_PASSWORD`, and `AGENTUNNEL_AGENT_TOKEN` should be long random strings in production.
- **The agent token is shared** between all `tunnel` instances that connect to this relay. Anyone with the token can register sessions.

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
export AGENTUNNEL_RELAY_ADDR=diaro.dev:443
export AGENTUNNEL_RELAY_TOKEN=<your-agent-token>
./bin/tunnel claude
```

Note: when connecting through nginx with TLS, use port `443` (the HTTPS port), not `8586` (the internal port). The relay address should be the domain name, not the VPS IP.
