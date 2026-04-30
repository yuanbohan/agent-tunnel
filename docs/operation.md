# Relay Operations

This guide is for commands run on the VPS after SSH login. For deploys started from your local checkout, use [deploy.md](./deploy.md).

For the complete Docker Compose operating guide, including all remote paths and environment files, use [docker-operation.md](./docker-operation.md).

The primary relay operations model is Docker Compose. Relay and PostgreSQL run as services in `deploy/compose/compose.yaml`, with runtime secrets stored only in the remote `.env`.

## Environment File

The remote env file usually lives at:

```text
/opt/agentunnel/compose/.env
```

It contains:

| Variable | Purpose |
| --- | --- |
| `RELAY_IMAGE_TAG` | Immutable Relay image tag, for example `v0.1.0` |
| `POSTGRES_PASSWORD` | PostgreSQL password; keep URL-safe unless the DSN is customized |
| `RELAY_APP_SECRET` | HMAC secret used for token and agent-token digests |
| `RELAY_OPERATOR_TOKEN` | Fixed bearer token for local-only operator commands |

Keep this file mode `0600`. Do not commit real `.env` files.

The Compose file fixes these non-secret runtime defaults:

- Relay listens in-container on `0.0.0.0:8586`
- Docker publishes Relay to the host on `127.0.0.1:8586`
- PostgreSQL uses database `agent_tunnel`
- PostgreSQL uses role `relay_user`
- PostgreSQL stores data in Docker volume `relay-postgres-data`

## Service Lifecycle

Run from the Compose directory:

```bash
cd /opt/agentunnel/compose
sudo docker compose --env-file .env pull
sudo docker compose --env-file .env up -d
sudo docker compose --env-file .env ps
```

Stop or start services:

```bash
sudo docker compose --env-file .env stop
sudo docker compose --env-file .env start
```

Remove containers while keeping the named PostgreSQL volume:

```bash
sudo docker compose --env-file .env down
```

Do not remove the configured PostgreSQL volume, usually `relay-postgres-data`, unless you intentionally want to destroy the database and reinitialize from `latest.sql`.

Relay also writes structured logs to the host at:

```text
/opt/agentunnel/logs/relay/relay.log
```

## Health and Logs

```bash
curl -fsS http://127.0.0.1:8586/healthz
sudo docker compose --env-file .env logs --tail 100 relay
sudo docker compose --env-file .env logs --tail 100 postgres
sudo tail -f /opt/agentunnel/logs/relay/relay.log
```

Healthy `healthz` output should include `"status":"ok"`.

## Operator Commands

Operator commands run against the local-only operator API from inside the Relay container:

```bash
cd /opt/agentunnel/compose
sudo docker compose --env-file .env exec relay relay invite create --count 3 --expires-in 7d
sudo docker compose --env-file .env exec relay relay invite list
sudo docker compose --env-file .env exec relay relay invite disable --code AB2C3D
sudo docker compose --env-file .env exec relay relay user delete --username alice
sudo docker compose --env-file .env exec relay relay user tier alice pro
sudo docker compose --env-file .env exec relay relay user tier alice pro --json
```

The operator routes remain outside the public `/api/` namespace and should not be exposed through nginx.

From the repo root, the same operations are wrapped as local Make targets for `relay-cn`:

```bash
make relay-cn-relay-version
make relay-cn-invite-create RELAY_CN_INVITE_COUNT=3 RELAY_CN_INVITE_EXPIRES_IN=7d
make relay-cn-invite-list
make relay-cn-invite-disable RELAY_CN_INVITE_CODE=AB2C3D
make relay-cn-user-delete RELAY_CN_USERNAME=alice
make relay-cn-psql
```

Use these when the relay is Docker-managed and you want one remembered local entrypoint instead of retyping the `ssh` + `docker compose exec` form.

## Schema Changes

Fresh PostgreSQL volumes are initialized from:

```text
/opt/agentunnel/postgres/latest.sql
```

The PostgreSQL image runs that file only when the data volume is empty. Existing databases are never automatically migrated by Compose.

When a release needs a database schema change:

1. Confirm the repository change updated `deploy/postgres/latest.sql`.
2. Review the manual SQL required for the existing database.
3. Back up the database.
4. Execute the SQL intentionally on the server, for example with `psql`.
5. Deploy the compatible Relay image tag.

Example `psql` session:

```bash
cd /opt/agentunnel/compose
sudo docker compose --env-file .env exec postgres sh -lc 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB"'
```

The legacy `relay-migrate` binary may still exist on older hosts for local compatibility work, but it is not part of the Docker Compose deployment path.

Step 2 connectivity auth/pairing requires this manual SQL on existing databases before deploying a Relay binary that reads the new columns:

```sql
alter table users
  add column if not exists subscription_tier text not null default 'free';

do $$
begin
  if not exists (
    select 1 from pg_constraint where conname = 'users_subscription_tier_check'
  ) then
    alter table users
      add constraint users_subscription_tier_check
      check (subscription_tier in ('free', 'pro'));
  end if;
end $$;

alter table app_sessions
  add column if not exists device_fingerprint text not null default '';

create index if not exists app_sessions_user_device_fingerprint_idx
  on app_sessions (user_id, device_fingerprint)
  where device_fingerprint <> '';
```

## Troubleshooting

If Relay is unhealthy:

1. Confirm PostgreSQL is healthy with `docker compose ps`.
2. Confirm `.env` contains `RELAY_APP_SECRET`, `RELAY_OPERATOR_TOKEN`, and URL-safe PostgreSQL credentials.
3. Confirm Relay is listening on the host-local port: `curl http://127.0.0.1:8586/healthz`.
4. Confirm nginx proxies `/api/`, `/agent/ws`, `/device/ws`, and `/healthz` to the same local port.
5. Inspect Relay logs with `docker compose logs relay` or `/opt/agentunnel/logs/relay/relay.log`.
