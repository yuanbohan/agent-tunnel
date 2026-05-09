# Docker Operation

This document is the complete Docker Compose operating guide for the Relay service. It covers the GitHub Container Registry image, remote file layout, required environment files, Ansible commands, logs, and PostgreSQL schema handling.

## Image

The Relay image is published by GitHub Actions from a manual release dispatch. Open the private repo Actions tab, run `Release`, select `relay`, and enter a plain version such as `v0.1.0`. The workflow resolves source tag `relay-v0.1.0`, verifies the image, and only then creates or validates that source tag before push.

The workflow is:

```text
.github/workflows/release.yml
```

It builds `Dockerfile.relay`, verifies `relay version`, and pushes:

```text
ghcr.io/yuanbohan/agent-tunnel-relay:<version>
```

For example:

```text
ghcr.io/yuanbohan/agent-tunnel-relay:v0.1.0
```

The image name does not need to match the GitHub repository name. The repository can be `agent-tunnel` while the GHCR package/image is `agent-tunnel-relay`.

Do not deploy from `latest`. The deployed version is the exact value of `RELAY_IMAGE_TAG` in the remote Compose `.env`.

## GitHub Setup

Confirm the repository allows GitHub Actions to publish packages:

```text
Repository -> Settings -> Actions -> General
```

The workflow itself declares:

```yaml
permissions:
  contents: read
  packages: write
```

After the first successful tag release, check:

```text
GitHub repository or owner page -> Packages -> agent-tunnel-relay
```

The Relay GHCR package is private. Docker login uses the fixed GitHub username `yuanbohan`, so the only deployment secret you need is a token that can read packages. Store it only in the local Ansible secrets file, not in the repository:

```yaml
# ansible/host_vars/relay-cn/relay-secrets.yml
relay_ghcr_token: YOUR_READ_PACKAGES_TOKEN
```

Use the same keys for dev when needed:

```yaml
# ansible/host_vars/dev/relay-secrets.yml
relay_ghcr_token: YOUR_READ_PACKAGES_TOKEN
```

This value is required for Docker Compose deployments. The Compose Ansible role uses it to run:

```bash
docker login ghcr.io --username yuanbohan --password-stdin
```

before `pull` or `up`.

Create `relay_ghcr_token` in GitHub:

1. Open GitHub as `yuanbohan`.
2. Go to `Settings -> Developer settings -> Personal access tokens`.
3. Create a token that can read packages. For a classic token, select the `read:packages` scope. If the UI requires repository access for private packages, restrict it to the `agent-tunnel` repository.
4. Copy the token once and put it in `ansible/host_vars/relay-cn/relay-secrets.yml` and, if needed, `ansible/host_vars/dev/relay-secrets.yml`.
5. Do not commit the real secrets files.

## Repository Files

Docker-related files in the repository:

```text
Dockerfile.relay
.dockerignore
.github/workflows/release.yml
deploy/compose/compose.yaml
deploy/compose/.env.example
deploy/compose/README.md
deploy/postgres/latest.sql
scripts/test-relay-docker-image.sh
```

Ansible files used by the Docker flow:

```text
ansible/playbooks/site.yml
ansible/inventories/dev.yml
ansible/inventories/relay-cn.yml
ansible/roles/relay_compose/tasks/main.yml
ansible/host_vars/dev/relay-secrets.example.yml
ansible/host_vars/relay-cn/relay-secrets.example.yml
```

## Remote Layout

The Compose assets are synced to:

```text
/opt/agentunnel
```

Expected remote paths:

```text
/opt/agentunnel/compose/compose.yaml
/opt/agentunnel/compose/.env
/opt/agentunnel/compose/README.md
/opt/agentunnel/postgres/latest.sql
/opt/agentunnel/logs/relay/relay.log
```

The real `.env` is remote-only:

```text
/opt/agentunnel/compose/.env
```

Ansible does not sync `.env.example`, and it does not overwrite the real `.env`.

## Local Ansible Secrets

Create local secrets from the example files:

```bash
cp ansible/host_vars/relay-cn/relay-secrets.example.yml ansible/host_vars/relay-cn/relay-secrets.yml
cp ansible/host_vars/dev/relay-secrets.example.yml ansible/host_vars/dev/relay-secrets.yml
```

Required relay-cn values:

```yaml
relay_ghcr_token: YOUR_READ_PACKAGES_TOKEN
relay_certbot_email: you@example.com
```

Required dev values:

```yaml
relay_ghcr_token: YOUR_READ_PACKAGES_TOKEN
```

The Compose deployment path does not render this token into the remote Compose `.env`. It is used only for required GHCR login. Keep runtime Relay and PostgreSQL secrets only in `/opt/agentunnel/compose/.env` on the server so Compose remains the single operational source of truth.

## Remote Compose Environment

Create the remote `.env` after syncing Compose assets:

```bash
ssh relay-cn-host
cd /opt/agentunnel/compose
sudo install -m 600 /dev/null .env
sudoedit .env
```

The file must contain:

```env
RELAY_IMAGE_TAG=v0.1.0

POSTGRES_PASSWORD=REPLACE_WITH_URL_SAFE_DB_PASSWORD

RELAY_APP_SECRET=REPLACE_WITH_LONG_RANDOM_SECRET
RELAY_OPERATOR_TOKEN=REPLACE_WITH_LONG_RANDOM_TOKEN
```

`POSTGRES_PASSWORD` is interpolated into `RELAY_DATABASE_URL` by Compose. Use URL-safe characters unless you intentionally edit `compose.yaml` to use an encoded DSN.

The Compose file fixes these non-secret runtime defaults:

- Relay listens in-container on `0.0.0.0:8586`
- Docker publishes Relay to the host on `127.0.0.1:8586`
- PostgreSQL uses database `agent_tunnel`
- PostgreSQL uses role `relay_user`
- PostgreSQL stores data in Docker volume `relay-postgres-data`

For production operations, treat this remote `.env` as the only runtime configuration source. Do not duplicate `POSTGRES_PASSWORD`, `RELAY_APP_SECRET`, or `RELAY_OPERATOR_TOKEN` into local Ansible secret files.

## First Host Setup

The host must have Docker Engine and the Docker Compose plugin installed before running Compose lifecycle commands.

This Ansible flow does not install Docker. Verify the remote host first:

```bash
ssh relay-cn-host 'docker --version && docker compose version'
```

Bootstrap relay-cn nginx/TLS host state:

```bash
make init-relay-cn
```

Bootstrap dev nginx host state:

```bash
make init-dev
```

These commands do not start the Relay Compose stack.

## Sync Compose Assets

Sync relay-cn:

```bash
make compose-sync-relay-cn
```

Sync dev:

```bash
make compose-sync-dev
```

This creates or updates:

```text
/opt/agentunnel/compose/compose.yaml
/opt/agentunnel/compose/README.md
/opt/agentunnel/postgres/latest.sql
/opt/agentunnel/logs/relay/
```

It does not create or overwrite:

```text
/opt/agentunnel/compose/.env
```

## Start or Update

Start or update relay-cn:

```bash
make compose-up-relay-cn
```

Start or update dev:

```bash
make compose-up-dev
```

These targets pull configured images and run `docker compose up -d` on the remote host.

To only pull images:

```bash
make compose-pull-relay-cn
make compose-pull-dev
```

To stop services without removing containers:

```bash
make compose-stop-relay-cn
make compose-stop-dev
```

To start existing stopped services:

```bash
make compose-start-relay-cn
make compose-start-dev
```

To remove containers while keeping the PostgreSQL named volume:

```bash
make compose-down-relay-cn
make compose-down-dev
```

## Direct Remote Commands

Run these on the VPS when operating directly:

```bash
cd /opt/agentunnel/compose
sudo docker compose --env-file .env pull
sudo docker compose --env-file .env up -d
sudo docker compose --env-file .env ps
```

Stop/start:

```bash
sudo docker compose --env-file .env stop
sudo docker compose --env-file .env start
```

Remove containers but keep data:

```bash
sudo docker compose --env-file .env down
```

## Verification

Check service state:

```bash
cd /opt/agentunnel/compose
sudo docker compose --env-file .env ps
```

Check Relay health:

```bash
curl -fsS http://127.0.0.1:8586/healthz
```

Expected response includes:

```json
{"status":"ok"}
```

Check logs:

```bash
sudo docker compose --env-file .env logs --tail 100 relay
sudo docker compose --env-file .env logs --tail 100 postgres
sudo tail -f /opt/agentunnel/logs/relay/relay.log
```

## Operator Commands

Run operator commands from inside the Relay container:

```bash
cd /opt/agentunnel/compose
sudo docker compose --env-file .env exec relay relay invite create --count 3 --expires-in 7d
sudo docker compose --env-file .env exec relay relay invite list
sudo docker compose --env-file .env exec relay relay invite disable --code AB2C3D
sudo docker compose --env-file .env exec relay relay user delete --username alice
sudo docker compose --env-file .env exec relay relay user tier alice pro --json
```

The operator routes are local maintenance routes and must stay outside the public `/api/` namespace. Do not expose them through nginx.

If you prefer running these from your local checkout, the repo also exposes relay-cn-specific wrappers:

```bash
make relay-cn-relay-version
make relay-cn-invite-create RELAY_CN_INVITE_COUNT=3 RELAY_CN_INVITE_EXPIRES_IN=7d
make relay-cn-invite-list
make relay-cn-invite-disable RELAY_CN_INVITE_CODE=AB2C3D
make relay-cn-user-delete RELAY_CN_USERNAME=alice
make relay-cn-psql
```

## Logs

Docker logs are available through Compose:

```bash
cd /opt/agentunnel/compose
sudo docker compose --env-file .env logs --tail 100 relay
```

Relay structured logs are also persisted on the host:

```text
/opt/agentunnel/logs/relay/relay.log
```

Follow the persistent log:

```bash
sudo tail -f /opt/agentunnel/logs/relay/relay.log
```

The local repository ignores generated logs under:

```text
deploy/logs/
```

## PostgreSQL Data

PostgreSQL data is stored in a Docker named volume:

```text
relay-postgres-data
```

or whatever value is set in:

```text
RELAY_POSTGRES_VOLUME
```

Do not remove this volume unless intentionally destroying the database.

The fresh database initialization SQL is:

```text
/opt/agentunnel/postgres/latest.sql
```

from the repository file:

```text
deploy/postgres/latest.sql
```

The official PostgreSQL image runs this file only when the data volume is empty.

## Schema Changes

The Docker Compose deployment path does not run automatic migrations.

When a release changes PostgreSQL schema:

1. Update `deploy/postgres/latest.sql` so a fresh database can reproduce the full current schema.
2. Prepare the manual SQL needed for existing deployed databases.
3. Back up the production database.
4. Execute the manual SQL on the server.
5. Deploy the compatible Relay image tag.

Open a PostgreSQL shell on the server:

```bash
cd /opt/agentunnel/compose
sudo docker compose --env-file .env exec postgres sh -lc 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB"'
```

For multi-statement schema changes, run `psql` with `ON_ERROR_STOP=1` and wrap the statements in one transaction so partial changes do not remain after an error:

```bash
cd /opt/agentunnel/compose
sudo docker compose --env-file .env exec postgres sh -lc 'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB"'
```

The legacy `relay-migrate` binary and numbered SQL migration files may still exist for local compatibility work, but they are not part of the Docker Compose production deployment path.

Step 2 connectivity auth/pairing requires this manual SQL on existing Docker Compose databases before deploying the matching Relay image:

```sql
begin;

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

commit;
```

## Release Update Checklist

For a normal Relay-only update, run the private repo `Release` workflow, select `relay`, and enter `v0.1.1`. The workflow resolves source tag `relay-v0.1.1`, verifies the image, and then creates or validates that source tag before push.

Wait for the GitHub Actions workflow to publish:

```text
ghcr.io/yuanbohan/agent-tunnel-relay:v0.1.1
```

Then update the remote `.env`:

```bash
ssh relay-cn-host
sudoedit /opt/agentunnel/compose/.env
```

Set:

```env
RELAY_IMAGE_TAG=v0.1.1
```

Apply:

```bash
make compose-up-relay-cn
```

Verify:

```bash
ssh relay-cn-host
curl -fsS http://127.0.0.1:8586/healthz
sudo tail -n 100 /opt/agentunnel/logs/relay/relay.log
```

## Troubleshooting

If image pull fails with `denied`, confirm `relay_ghcr_token` is set in the relevant Ansible secrets file and that the token has package read access for the private `agent-tunnel-relay` GHCR package.

If Compose says a required variable is missing, edit:

```text
/opt/agentunnel/compose/.env
```

If Relay is unhealthy:

1. Check `sudo docker compose --env-file .env ps`.
2. Check PostgreSQL logs.
3. Check Relay logs.
4. Confirm `RELAY_APP_SECRET`, `RELAY_OPERATOR_TOKEN`, and URL-safe PostgreSQL credentials are set.
5. Confirm nginx proxies `/api/`, `/agent/ws`, `/device/ws`, and `/healthz` to `127.0.0.1:8586`.

If a fresh database did not initialize, check whether the configured Docker volume already existed. PostgreSQL only runs `/docker-entrypoint-initdb.d/` files for an empty data directory.
