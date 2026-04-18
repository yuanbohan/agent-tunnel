# Relay Operations

This guide is for commands run on the VPS after SSH login.
For deploys started from your local checkout with `make` plus Ansible, use [deploy.md](./deploy.md).

This guide covers the day-to-day relay CLI commands introduced for PostgreSQL-backed auth, invite-code management, operator maintenance, and explicit schema migrations.

The commands here use the `relay` and `relay-migrate` binaries from this repository.
`relay-migrate` requires an explicit `--schema-dir` so it never guesses where SQL files live, and cobra enforces that requirement before execution.
Remote install and deploy now run through Ansible inventories under `ansible/inventories/` plus per-environment secrets in `ansible/host_vars/dev/relay-secrets.yml` and `ansible/host_vars/prod/relay-secrets.yml`. Local commands such as `make migrate` and `make start` read the current shell environment only.

## Environment Variables

These are the relay-side environment variables that matter now:

| Variable | Required for | Purpose |
|----------|--------------|---------|
| `RELAY_DATABASE_URL` | `relay-migrate`, `relay serve` | PostgreSQL DSN for durable auth state |
| `RELAY_APP_SECRET` | `relay serve` | HMAC secret used for token and agent-token digests |
| `RELAY_OPERATOR_TOKEN` | `relay serve`, `relay invite ...`, `relay user delete` | Fixed bearer token for the local-only operator control path |
| `RELAY_LISTEN_ADDR` | optional | Relay listen address; defaults to `127.0.0.1:8586` |
| `RELAY_LOG_FILE` | optional | Append structured relay logs to this file instead of stderr |

Notes:

- Operator commands call the running relay over a local-only operator API outside `/api/`. They must run on the relay host, or through an SSH tunnel that lands on the relay host loopback interface.
- nginx should serve the public website on `/`, proxy `/api/`, `/agent/ws`, `/device/ws`, and `/healthz`, and keep the operator routes off the public surface.
- Operator commands call the running relay. That means `relay serve` must already be running when you execute `relay invite ...` or `relay user delete`.
- `RELAY_LISTEN_ADDR` is shared by `relay serve` and the operator commands. If you start the relay on a different local address, set the same value before running invite or user commands.
- Operator commands are intentionally local-only. They do not accept a relay-address flag. `relay invite disable` requires `--code`, and `relay user delete` requires `--username`.
- On a systemd-managed host, the usual source of truth is `/etc/agentunnel/relay.env`. `make env-dev` or `make env-prod` renders that file from the selected inventory plus the matching host secret file. You can source that file before running relay commands, or pass it directly to `relay-migrate --env-file /etc/agentunnel/relay.env`.

## Command Summary

| Command | Purpose |
|---------|---------|
| `relay-migrate --schema-dir <dir>` | Apply PostgreSQL schema migrations from an explicit schema directory |
| `relay-migrate --env-file <path> --schema-dir <dir>` | Apply migrations using literal `KEY=VALUE` pairs loaded from an env file |
| `relay-migrate --env-file <path> --schema-dir <dir> --baseline <version>` | Mark migrations through a known version as already applied |
| `relay serve` | Start the relay HTTP and WebSocket service |
| `relay invite create` | Create one or more invite codes |
| `relay invite disable --code <code>` | Disable an existing invite code |
| `relay invite list` | List all invite codes with status and current binding |
| `relay user delete --username <name>` | Delete a user account and free the username |
| `make install` / `make install-local` | Install the local `tunnel`, `relay`, and `relay-migrate` binaries into `$(INSTALL_DIR)` |
| `make init-dev` / `make init-prod` | Fresh-host bootstrap: install base packages, create the relay PostgreSQL user and database, render nginx, and (prod only) issue the Let's Encrypt cert and switch nginx to TLS |
| `make install-dev` | Install base packages on the dev host and sync the HTTP nginx site that fronts the relay |
| `make install-prod` | Install base packages on the prod host plus `certbot`, certificate issuance/renewal wiring, and the HTTPS nginx site (assumes the cert already exists) |
| `make migrate` | Apply local schema migrations using the current shell environment |
| `make migrator-dev` / `make migrator-prod` | Build `relay-migrate` locally and install it on the target host |
| `make relay-bin-dev` / `make relay-bin-prod` | Build `relay` locally and install it on the target host |
| `make env-dev` / `make env-prod` | Render `/etc/agentunnel/relay.env` from the selected inventory and Ansible secrets |
| `make deploy-dev` / `make deploy-prod` | Build Linux binaries, sync schema, rerun migrations, update relay env and systemd, then restart the relay |
| `make deploy-website-dev` / `make deploy-website-prod` | Build `../agent-tunnel-website` and publish an atomic website release on the remote host |
| `make deps-dev` / `make deps-prod` / `make certbot-dev` / `make certbot-prod` / `make nginx-dev` / `make nginx-prod` / `make postgres-dev` / `make postgres-prod` / `make relay-dev` / `make relay-prod` | Run one isolated Ansible slice for lower-risk operational changes |

Ansible controls:

- `ANSIBLE_DRY_RUN=1` runs playbooks in check mode.
- `ANSIBLE_EXTRA_VARS_FILE=<path>` layers an additional vars file on top of the selected inventory.
- `make install-prod` requires `relay_certbot_email` to be set in `ansible/host_vars/prod/relay-secrets.yml`.
- Relay deploy manages relay artifacts only: binaries, schema files, `/etc/agentunnel/relay.env`, the systemd unit, and the `agentunnel-relay` service restart.
- `make migrate-dev` and `make migrate-prod` no longer install `relay-migrate`; they assume the remote migrator binary already exists. Run `make migrator-dev` or `make migrator-prod` first when you change migrator code or bootstrap a fresh host.
- `make relay-dev` and `make relay-prod` no longer install `relay`; they assume the remote relay binary already exists. Run `make relay-bin-dev` or `make relay-bin-prod` first when you change relay code or bootstrap a fresh host.
- Website deploy manages the static website bundle only: it runs `npm ci`, builds `../agent-tunnel-website`, rejects bundle symlinks, uploads a release under `/var/www/agentunnel-website/releases`, and atomically repoints `/var/www/agentunnel-website/current`.
- Package installation, cert issuance, nginx config, PostgreSQL config, relay deploy, and website deploy can all be run independently through dedicated targets.

## Start the Relay

Example:

```bash
export RELAY_DATABASE_URL=postgres://relay_user:change-me-db-password@localhost/agent_tunnel?sslmode=disable
export RELAY_APP_SECRET=change-me
export RELAY_OPERATOR_TOKEN=change-me-operator-token

relay-migrate --schema-dir ./schema
relay serve --listen-addr 127.0.0.1:8586
```

`relay serve` requires:

- `RELAY_DATABASE_URL`
- `RELAY_APP_SECRET`
- `RELAY_OPERATOR_TOKEN`

## Run Schema Migrations

Apply all unapplied SQL files from `schema/`:

```bash
export RELAY_DATABASE_URL=postgres://relay_user:change-me-db-password@localhost/agent_tunnel?sslmode=disable

relay-migrate --schema-dir ./schema
```

On a systemd-managed relay host:

```bash
relay-migrate --env-file /etc/agentunnel/relay.env --schema-dir /etc/agentunnel/schema
```

Baseline an existing database that already matches migrations through `0002_operator_audit.sql`:

```bash
export RELAY_DATABASE_URL=postgres://relay_user:change-me-db-password@localhost/agent_tunnel?sslmode=disable

relay-migrate --schema-dir ./schema --baseline 0002_operator_audit.sql
```

Notes:

- `--baseline` records migrations up to and including the specified file as applied without executing their SQL.
- `--schema-dir` is required. In a checked-out repo that is usually `./schema`; on a deployed host it is usually `/etc/agentunnel/schema`.
- Use baseline only once when adopting migration tracking for an already-initialized database.
- Regular schema updates should use `relay-migrate --schema-dir ...` with no baseline flag.

## Create Invite Codes

Create three invite codes that expire after seven days:

```bash
export RELAY_OPERATOR_TOKEN=change-me-operator-token
export RELAY_LISTEN_ADDR=127.0.0.1:8586

relay invite create --count 3 --expires-in 7d
```

Example output:

```text
AB2C3D
EF4G5H
JK7M8N
```

Notes:

- `--count` defaults to `1`.
- `--expires-in` defaults to `7d`.
- `--expires-in` only supports whole days, for example `1d` or `7d`.
- Invite codes are six characters, case-insensitive for user input, and chosen to be easy to type manually.

## Disable an Invite Code

Disable an invite code before it is consumed:

```bash
export RELAY_OPERATOR_TOKEN=change-me-operator-token
export RELAY_LISTEN_ADDR=127.0.0.1:8586

relay invite disable --code AB2C3D
```

Example output:

```text
disabled AB2C3D
```

The code is normalized case-insensitively, so `ab2c3d` and `AB2C3D` behave the same.

## List Invite Codes

Check current invite codes and whether they are still available:

```bash
export RELAY_OPERATOR_TOKEN=change-me-operator-token
export RELAY_LISTEN_ADDR=127.0.0.1:8586

relay invite list
```

Example output:

```text
CODE      STATUS    CONSUMED_BY  DISABLED_BY  EXPIRES_AT
AB2C3D    available -           -            2026-04-18T09:00:00Z
EF4G5H    consumed  alice       -            2026-04-18T09:00:00Z
JK7M8N    disabled  -           operator     2026-04-18T09:00:00Z
```

Use this to verify whether a code is already used and by which user before reusing a batch.

## Delete a User

Delete an account and free the username for reuse:

```bash
export RELAY_OPERATOR_TOKEN=change-me-operator-token
export RELAY_LISTEN_ADDR=127.0.0.1:8586

relay user delete --username alice
```

Example output:

```text
deleted alice
```

Notes:

- The username is normalized case-insensitively.
- User deletion is audited as an operator action.
- Any live relay sessions owned by that user are disconnected immediately.

## Typical Operator Sequence

Bring up a new relay, create invites, and later clean up an abandoned account:

```bash
export RELAY_DATABASE_URL=postgres://relay_user:change-me-db-password@localhost/agent_tunnel?sslmode=disable
export RELAY_APP_SECRET=change-me
export RELAY_OPERATOR_TOKEN=change-me-operator-token
export RELAY_LISTEN_ADDR=127.0.0.1:8586

relay-migrate --schema-dir ./schema
relay serve --listen-addr "$RELAY_LISTEN_ADDR"

# In another shell on the same host:
relay invite create --count 5 --expires-in 7d
relay invite disable --code AB2C3D
relay user delete --username alice
```

## Troubleshooting

If an operator command fails:

1. Confirm the relay service is running.
2. Confirm `RELAY_OPERATOR_TOKEN` matches the value used by `relay serve`.
3. Confirm `RELAY_LISTEN_ADDR` points at the same local address used by `relay serve`.
4. Confirm the relay can still reach PostgreSQL.
5. Confirm you are talking to the relay directly on the host, not through public nginx.

Useful checks:

```bash
curl http://127.0.0.1:8586/healthz
ss -lntp | grep 8586
```
