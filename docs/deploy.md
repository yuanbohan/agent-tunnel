# Relay Deploy

This guide is for deploys started from your local checkout with `make` plus Ansible.
For commands run after SSHing into the VPS, use [operation.md](./operation.md).

## Files That Matter

- `ansible/inventories/dev.yml`
- `ansible/inventories/prod.yml`
- `ansible/host_vars/dev/relay-secrets.yml`
- `ansible/host_vars/prod/relay-secrets.yml`

## Important Commands

| Command | What it does |
| --- | --- |
| `make init-dev` | Bootstrap the dev host: nginx, postgresql, relay DB user/database, HTTP nginx site |
| `make init-prod` | Bootstrap the prod host: nginx, postgresql, certbot, relay DB user/database, HTTP nginx site, issue Let's Encrypt cert, then switch nginx to TLS site |
| `make install-dev` | Install only OS packages and render the dev HTTP nginx site |
| `make install-prod` | Install only OS packages, certbot wiring, and the prod HTTPS nginx site (assumes cert already exists) |
| `make postgres-dev` | Ensure the dev PostgreSQL user and database exist |
| `make postgres-prod` | Ensure the prod PostgreSQL user and database exist |
| `make migrator-dev` | Build and install `relay-migrate` on dev |
| `make migrator-prod` | Build and install `relay-migrate` on prod |
| `make relay-bin-dev` | Build and install `relay` on dev |
| `make relay-bin-prod` | Build and install `relay` on prod |
| `make migrate-dev` | Sync `schema/` and run DB migrations on dev |
| `make migrate-prod` | Sync `schema/` and run DB migrations on prod |
| `make relay-dev` | Render relay env and systemd config on dev, then restart relay |
| `make relay-prod` | Render relay env and systemd config on prod, then restart relay |
| `make deploy-dev` | Full dev relay deploy |
| `make deploy-prod` | Full prod relay deploy |
| `make deploy-website-dev` | Deploy the website bundle to dev |
| `make deploy-website-prod` | Deploy the website bundle to prod |

## Common Flows

### First Dev Bootstrap

```bash
make init-dev
make migrator-dev
make relay-bin-dev
make migrate-dev
make relay-dev
```

### First Prod Bootstrap

Requires `relay_certbot_email` and `relay_database_password` set in `ansible/host_vars/prod/relay-secrets.yml`, and the prod DNS records pointing at the target host before `make init-prod` runs (Let's Encrypt issues the cert over HTTP-01).

```bash
make init-prod
make migrator-prod
make relay-bin-prod
make migrate-prod
make relay-prod
```

`make init-prod` runs the ansible playbook twice: the first pass installs packages, creates the PostgreSQL user and database, renders the HTTP nginx site, and issues the Let's Encrypt cert over the webroot challenge; the second pass re-renders nginx with the TLS site now that the cert exists and reloads the service.

### Schema Change

```bash
make migrator-dev   # only if cmd/migrate or migration runner code changed
make migrate-dev
```

```bash
make migrator-prod  # only if cmd/migrate or migration runner code changed
make migrate-prod
```

### Relay Code Change

```bash
make relay-bin-dev
make relay-dev
```

```bash
make relay-bin-prod
make relay-prod
```

### One-Shot Deploy

```bash
make deploy-dev
make deploy-prod
```

`make deploy-*` installs both remote binaries, syncs `schema/`, runs DB migrations, renders relay env/systemd, and restarts the relay.

## Verify Relay After `make relay-dev` or `make relay-prod`

Run these on the target VPS:

```bash
curl -fsS http://127.0.0.1:8586/healthz
sudo systemctl status agentunnel-relay --no-pager
sudo journalctl -u agentunnel-relay --since "10 minutes ago" --no-pager | tail -n 100
```

Healthy `healthz` output should be JSON with `"status":"ok"`.

## Notes

- `make relay-dev` and `make relay-prod` do not upload the `relay` binary. Run `make relay-bin-dev` or `make relay-bin-prod` first when relay code changed.
- `make migrate-dev` and `make migrate-prod` do not upload `relay-migrate`. Run `make migrator-dev` or `make migrator-prod` first when migration code changed.
- Website deploy paths come from `ansible/inventories/dev.yml` and `ansible/inventories/prod.yml`.
- Use `ANSIBLE_DRY_RUN=1` to preview changes, for example `ANSIBLE_DRY_RUN=1 make relay-dev`.
