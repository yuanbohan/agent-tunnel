# Relay Deploy

This guide is for deploys started from your local checkout. The primary relay deployment path is Docker Compose: the server runs PostgreSQL and Relay containers, and the Relay image is pulled from GitHub Container Registry.

For the complete Docker Compose operating guide, including all remote paths and environment files, use [docker-operation.md](./docker-operation.md).

For commands run after SSHing into the VPS, use [operation.md](./operation.md).

## Files That Matter

- `Dockerfile.relay`
- `.github/workflows/release.yml`
- `deploy/compose/compose.yaml`
- `deploy/compose/.env.example`
- `deploy/postgres/latest.sql`
- `ansible/inventories/dev.yml`
- `ansible/inventories/prod.yml`

## Image Release

Open the private repo Actions tab, run `Release`, select `relay`, and enter a plain version such as `v0.1.0`. The workflow resolves source tag `relay-v0.1.0`, runs tests, builds the Relay image, verifies `relay version`, creates or validates the source tag after Relay-specific validation succeeds, and then pushes the verified image:

```text
ghcr.io/yuanbohan/agent-tunnel-relay:v0.1.0
```

Deploy Compose with explicit version tags. Do not use a mutable `latest` tag as the deployment source of truth.

The GHCR package is private. Set `relay_ghcr_token` in `ansible/host_vars/<env>/relay-secrets.yml` before running `make compose-pull-*` or `make compose-up-*`. The Ansible role logs in to GHCR as `yuanbohan`, and the token only needs package read access. For prod, keep `relay_certbot_email` in the same local file for TLS bootstrap. Runtime Relay and PostgreSQL secrets belong only in the remote Compose `.env`.

## First Host Setup

Install Docker Engine and the Compose plugin on the host, then sync the Compose assets from your checkout:

```bash
make compose-sync-prod   # or compose-sync-dev
```

Create the private remote env file from the checked-in example:

```bash
ssh prod-host
cd /opt/agentunnel/compose
sudo cp .env.example .env
sudo chmod 600 .env
sudoedit .env
```

Set at least:

- `RELAY_IMAGE_TAG`
- `POSTGRES_PASSWORD`
- `RELAY_APP_SECRET`
- `RELAY_OPERATOR_TOKEN`

`POSTGRES_PASSWORD` is interpolated into `RELAY_DATABASE_URL` by Compose, so use URL-safe characters unless you intentionally edit the Compose file to provide an encoded DSN.

The Compose file fixes the non-secret runtime defaults for production operations:

- Relay listens in-container on `0.0.0.0:8586`
- Docker publishes Relay to the host on `127.0.0.1:8586`
- PostgreSQL uses database `agent_tunnel`
- PostgreSQL uses role `relay_user`
- PostgreSQL stores data in Docker volume `relay-postgres-data`

## Ansible Compose Commands

These targets sync Compose assets and run Docker Compose on the remote host. They do not build local Go binaries.

| Command | What it does |
| --- | --- |
| `make compose-sync-dev` | Sync Compose files and `latest.sql` to dev |
| `make compose-sync-prod` | Sync Compose files and `latest.sql` to prod |
| `make compose-pull-dev` | Pull configured images on dev |
| `make compose-pull-prod` | Pull configured images on prod |
| `make compose-up-dev` | Pull images and run `docker compose up -d` on dev |
| `make compose-up-prod` | Pull images and run `docker compose up -d` on prod |
| `make compose-start-dev` | Start existing Compose services on dev |
| `make compose-start-prod` | Start existing Compose services on prod |
| `make compose-stop-dev` | Stop Compose services on dev without removing containers |
| `make compose-stop-prod` | Stop Compose services on prod without removing containers |
| `make compose-down-dev` | Stop and remove Compose containers on dev while keeping named volumes |
| `make compose-down-prod` | Stop and remove Compose containers on prod while keeping named volumes |

Typical prod update:

```bash
make compose-sync-prod
ssh prod-host 'sudoedit /opt/agentunnel/compose/.env'  # update RELAY_IMAGE_TAG
make compose-up-prod
```

## Schema Changes

`deploy/postgres/latest.sql` is the complete schema snapshot for fresh PostgreSQL volumes. PostgreSQL runs it through `/docker-entrypoint-initdb.d/` only when the named data volume is empty.

The Compose deploy path does not run automatic migrations. When a release needs an existing database schema change:

1. Update `deploy/postgres/latest.sql` so fresh databases reproduce the full current schema.
2. Prepare the manual SQL needed to mutate existing databases.
3. Run that SQL on the server intentionally.
4. Deploy the compatible Relay image tag.

The legacy `relay-migrate` command and numbered SQL files remain available for local compatibility work, but they are not part of the Docker Compose production deploy path.

## Verify Relay

Run these on the target VPS:

```bash
cd /opt/agentunnel/compose
sudo docker compose --env-file .env ps
curl -fsS http://127.0.0.1:8586/healthz
sudo docker compose --env-file .env logs --tail 100 relay
sudo tail -f /opt/agentunnel/logs/relay/relay.log
```

Healthy `healthz` output should be JSON with `"status":"ok"`.

## Website Deploy

Website deploy remains separate:

```bash
make deploy-website-dev
make deploy-website-prod
```

Website deploy builds `../agent-tunnel-website`, uploads a release under `/var/www/agentunnel-website/releases`, and atomically repoints `/var/www/agentunnel-website/current`.
