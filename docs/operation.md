# Relay Operations

This guide covers the day-to-day relay CLI commands introduced for PostgreSQL-backed auth, invite-code management, operator maintenance, and explicit schema migrations.

The commands here use the `relay` and `relay-migrate` binaries from this repository.
`relay-migrate` requires an explicit `--schema-dir` so it never guesses where SQL files live.
Runtime relay values are now sourced from current shell environment for local commands, and from `ansible/inventories/dev.yml` or `ansible/inventories/prod.yml` for remote deploy flows via `relay_env_vars`.

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
- nginx should serve the public website on `/`, proxy `/api/` and `/agent/ws`, and keep the operator routes off the public surface.
- Operator commands call the running relay. That means `relay serve` must already be running when you execute `relay invite ...` or `relay user delete`.
- `RELAY_LISTEN_ADDR` is shared by `relay serve` and the operator commands. If you start the relay on a different local address, set the same value before running invite or user commands.
- On a systemd-managed host, the usual source of truth is `/etc/agentunnel/relay.env`. `make deploy-env` pins `RELAY_LOG_FILE=/var/log/agentunnel/relay.log` there. You can source that file before running relay commands, or pass it directly to `relay-migrate --env-file /etc/agentunnel/relay.env`.

## Command Summary

| Command | Purpose |
|---------|---------|
| `relay-migrate --schema-dir <dir>` | Apply PostgreSQL schema migrations from an explicit schema directory |
| `relay-migrate --env-file <path> --schema-dir <dir>` | Apply migrations using literal `KEY=VALUE` pairs loaded from an env file |
| `relay-migrate --env-file <path> --schema-dir <dir> --baseline <version>` | Mark migrations through a known version as already applied |
| `relay serve` | Start the relay HTTP and WebSocket service |
| `relay invite create` | Create one or more invite codes |
| `relay invite disable` | Disable an existing invite code |
| `relay invite list` | List all invite codes with status and current binding |
| `relay user delete` | Delete a user account and free the username |
| `make install` / `make install-local` | Install the local `tunnel`, `relay`, and `relay-migrate` binaries into `$(INSTALL_DIR)` |
| `make install-dev` | Install `nginx` and PostgreSQL on the dev VPS if missing, sync the HTTP nginx site that serves the website plus relay proxy routes, and restart nginx |
| `make install-prod` | Dev bootstrap plus `certbot`, certificate issuance/renewal wiring, and the HTTPS nginx site for prod |
| `make migrate` | Apply local schema migrations from shell env (`RELAY_DATABASE_URL`) |
| `make deploy-env` | Generate `/etc/agentunnel/relay.env` on remote using the active Ansible inventory `relay_env_vars` |
| `make deploy-dev` / `make deploy-prod` | One-shot relay deploy against the dev/prod inventory with schema sync + env + restart |
| `make deploy-website-dev` / `make deploy-website-prod` | Build `../agent-tunnel-website` and publish an atomic website release on the remote host |

Install output controls:

- `INSTALL_DRY_RUN=1` prints the remote install plan in a structured way without changing the remote host.
- `INSTALL_VERBOSE=1` includes remote install debug details.
- `make install-prod` needs a certbot email. Pass `INSTALL_CERTBOT_EMAIL=<ops@example.com>` up front, or set `certbot_email` in `ansible/inventories/prod.yml`.

Deploy output controls:

- `DEPLOY_DRY_RUN=1` prints the deploy plan in a structured way without changing the remote host.
- `DEPLOY_VERBOSE=1` includes deploy debug details.
- Relay deploy manages relay artifacts only: binaries, schema files, `/etc/agentunnel/relay.env`, and the `agentunnel-relay` service restart.
- Website deploy manages the static website bundle only: it runs `npm ci`, builds `../agent-tunnel-website`, rejects bundle symlinks, uploads a release under `DEPLOY_WEBSITE_ROOT/releases`, and atomically repoints `DEPLOY_WEBSITE_ROOT/current`.
- Neither deploy path installs or reconfigures `nginx`, `certbot`, or `postgresql`, and neither rewrites those host-level config files.

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
