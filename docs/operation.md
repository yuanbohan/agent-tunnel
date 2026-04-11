# Relay Operations

This guide covers the day-to-day relay CLI commands introduced for PostgreSQL-backed auth, invite-code management, and operator maintenance.

The commands here all use the `relay` binary from this repository.

## Environment Variables

These are the relay-side environment variables that matter now:

| Variable | Required for | Purpose |
|----------|--------------|---------|
| `RELAY_DATABASE_URL` | `relay migrate`, `relay serve` | PostgreSQL DSN for durable auth state |
| `RELAY_APP_SECRET` | `relay serve` | HMAC secret used for token and invite-code digests |
| `RELAY_OPERATOR_TOKEN` | `relay serve`, `relay invite ...`, `relay user delete` | Fixed bearer token for the local-only operator control path |
| `RELAY_LISTEN_ADDR` | optional | Relay listen address; defaults to `127.0.0.1:8586` |

Notes:

- Operator commands call the running relay over a local-only operator API outside `/api/`. They must run on the relay host, or through an SSH tunnel that lands on the relay host loopback interface.
- nginx should proxy `/api/` and `/agent/ws` only. It should not proxy the operator routes.
- Operator commands call the running relay. That means `relay serve` must already be running when you execute `relay invite ...` or `relay user delete`.
- `RELAY_LISTEN_ADDR` is shared by `relay serve` and the operator commands. If you start the relay on a different local address, set the same value before running invite or user commands.
- On a systemd-managed host, the usual source of truth is `/etc/agentunnel/relay.env`. You can source that file before running the commands below instead of exporting variables by hand.

## Command Summary

| Command | Purpose |
|---------|---------|
| `relay migrate` | Apply PostgreSQL schema migrations |
| `relay serve` | Start the relay HTTP and WebSocket service |
| `relay invite create` | Create one or more invite codes |
| `relay invite disable` | Disable an existing invite code |
| `relay user delete` | Delete a user account and free the username |

## Start the Relay

Example:

```bash
export RELAY_DATABASE_URL=postgres://localhost/agent_tunnel?sslmode=disable
export RELAY_APP_SECRET=change-me
export RELAY_OPERATOR_TOKEN=change-me-operator-token

relay migrate
relay serve --listen-addr 127.0.0.1:8586
```

`relay serve` requires:

- `RELAY_DATABASE_URL`
- `RELAY_APP_SECRET`
- `RELAY_OPERATOR_TOKEN`

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
export RELAY_DATABASE_URL=postgres://localhost/agent_tunnel?sslmode=disable
export RELAY_APP_SECRET=change-me
export RELAY_OPERATOR_TOKEN=change-me-operator-token
export RELAY_LISTEN_ADDR=127.0.0.1:8586

relay migrate
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
