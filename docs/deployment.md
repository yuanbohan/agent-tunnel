# Relay Deployment & Operations

This repository now manages relay installation and deploys through Ansible only.
The source of truth is:

- `ansible/inventories/dev.yml`
- `ansible/inventories/prod.yml`
- `ansible/group_vars/all/relay-secrets.yml`

The old `.env.dev` / `.env.prod` deploy flow is gone.

## Prerequisites

- Ubuntu hosts reachable over SSH
- Passwordless `sudo` for the Ansible SSH user
- Go toolchain on the local machine for `make build-linux`
- Node.js and npm on the local machine for website deploys

## Secrets

Create the shared secrets file once:

```bash
cp ansible/group_vars/all/relay-secrets.example.yml ansible/group_vars/all/relay-secrets.yml
```

Set at least:

- `relay_database_password`
- `relay_app_secret`
- `relay_operator_token`
- `relay_certbot_email` for production TLS

## Inventories

`ansible/inventories/dev.yml` and `ansible/inventories/prod.yml` define:

- target host and SSH user
- nginx site names and domains
- TLS enablement
- PostgreSQL database name and username
- relay install paths
- website release paths

Per-environment differences belong in inventory. Shared secrets belong in `group_vars`.

## Server Layout

```text
/usr/local/bin/relay
/usr/local/bin/relay-migrate
/etc/agentunnel/relay.env
/etc/agentunnel/schema/
/etc/systemd/system/agentunnel-relay.service
/etc/systemd/system/nginx.service.d/agentunnel-restart.conf
/etc/nginx/conf.d/websocket_map.conf
/etc/nginx/sites-available/<site>
/etc/nginx/sites-enabled/<site>
/var/www/agentunnel-website/current
/var/www/agentunnel-website/releases/
/var/www/certbot/
/var/log/agentunnel/relay.log
```

## Make Targets

Host bootstrap:

- `make install-dev`
- `make install-prod`

Targeted host changes:

- `make deploy-deps`
- `make deploy-certbot`
- `make deploy-nginx`
- `make deploy-postgres`

Relay deploy:

- `make deploy-schema`
- `make deploy-env`
- `make deploy-migrate`
- `make deploy-relay`
- `make deploy-dev`
- `make deploy-prod`

Website deploy:

- `make deploy-website-dev`
- `make deploy-website-prod`

Local preview:

- `ANSIBLE_DRY_RUN=1 make install-dev`
- `ANSIBLE_DRY_RUN=1 make deploy-prod`

## Typical Flows

Dev bootstrap:

```bash
make install-dev
make deploy-postgres ANSIBLE_INVENTORY=ansible/inventories/dev.yml
make deploy-dev
make deploy-website-dev
```

Prod bootstrap:

```bash
make install-prod
make deploy-postgres ANSIBLE_INVENTORY=ansible/inventories/prod.yml
make deploy-prod
make deploy-website-prod
```

Routine relay update:

```bash
make deploy-dev
make deploy-prod
```

Routine website update:

```bash
make deploy-website-dev
make deploy-website-prod
```

Lower-risk rollout by slice:

```bash
make deploy-nginx ANSIBLE_INVENTORY=ansible/inventories/prod.yml
make deploy-relay ANSIBLE_INVENTORY=ansible/inventories/prod.yml
make deploy-website-prod
```

## What Each Slice Owns

- `install-dev`: package install plus dev nginx config
- `install-prod`: package install plus certbot and prod nginx config
- `deploy-postgres`: ensure PostgreSQL service is enabled and the relay database/user exist
- `deploy-dev` / `deploy-prod`: build Linux binaries, sync schema, rerun migrations, render relay env, render systemd unit, restart relay
- `deploy-website-*`: build website locally, upload release tarball, switch `current`, reload nginx

## Relay Restart Behavior

The relay is live-state only. A restart:

- disconnects active agents and attach clients
- causes agents to reconnect and re-register
- does not lose PostgreSQL-backed auth data

## Manual Checks

On the target host:

```bash
sudo systemctl status agentunnel-relay
sudo systemctl status nginx
sudo nginx -t
ss -lntp | grep 8586
sudo journalctl -u agentunnel-relay --since "1 hour ago"
sudo tail -n 200 /var/log/agentunnel/relay.log
```

Network checks:

```bash
curl -s -o /dev/null -w "%{http_code}" http://1.12.249.160/api/sessions
curl -s -o /dev/null -w "%{http_code}" https://diaro.me/api/sessions
```

Production TLS checks:

```bash
sudo certbot certificates
sudo systemctl list-timers | grep certbot
```

## Notes

- Keep the relay listening on `127.0.0.1`; nginx is the public entrypoint.
- Keep operator routes off the public nginx surface.
- Put inventory-specific behavior in inventory, not in Make variables.
- Put long-lived secrets in `ansible/group_vars/all/relay-secrets.yml`, not in ad hoc local env files.
