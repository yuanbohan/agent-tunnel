# Relay Deploy

This guide is for deploys started from your local checkout. The primary relay deployment path is Docker Compose: the server runs PostgreSQL, Relay HTTP/WebSocket, and Binding-only STUN containers. Relay and STUN use the same release build artifact, published under separate GitHub Container Registry image names with separate tag pins.

For the complete Docker Compose operating guide, including all remote paths and environment files, use [docker-operation.md](./docker-operation.md).

For commands run after SSHing into the VPS, use [operation.md](./operation.md).

## Files That Matter

- `Dockerfile.relay`
- `.github/workflows/release.yml`
- `deploy/compose/compose.yaml`
- `deploy/compose/.env.example`
- `deploy/postgres/latest.sql`
- `ansible/inventories/dev.yml`
- `ansible/inventories/relay-cn.yml`

## Image Release

Open the private repo Actions tab, run `Release`, select `relay`, and enter a plain version such as `v0.1.0`. The workflow resolves source tag `relay-v0.1.0`, runs tests, builds one Relay/STUN image artifact, verifies `relay version` plus the STUN-only command path, creates or validates the source tag after Relay-specific validation succeeds, and then pushes the verified image under both service-specific names:

```text
ghcr.io/yuanbohan/agent-tunnel-relay:v0.1.0
ghcr.io/yuanbohan/agent-tunnel-stun:v0.1.0
```

Deploy Compose with explicit version tags. The first split-service rollout should set both `RELAY_IMAGE_TAG` and `STUN_IMAGE_TAG` to the first release that includes `relay stun serve`; routine Relay updates later change only `RELAY_IMAGE_TAG`. Do not use a mutable `latest` tag as the deployment source of truth.

The GHCR packages are private. Set `relay_ghcr_token` in `ansible/host_vars/<env>/relay-secrets.yml` before running `make compose-pull-*` or `make compose-up-*`. The Ansible role logs in to GHCR as `yuanbohan`, and the token only needs package read access. For `relay-cn`, keep `relay_certbot_email` in the same local file for TLS bootstrap. Runtime Relay and PostgreSQL secrets belong only in the remote Compose `.env`.

## First Host Setup

Install Docker Engine and the Compose plugin on the host, then sync the Compose assets from your checkout:

```bash
make compose-sync-relay-cn   # or compose-sync-dev
```

Create the private remote env file on the host:

```bash
ssh relay-cn-host
cd /opt/agentunnel/compose
sudo install -m 600 /dev/null .env
sudoedit .env
```

Set at least:

- `RELAY_IMAGE_TAG`
- `STUN_IMAGE_TAG`
- `POSTGRES_PASSWORD`
- `RELAY_APP_SECRET`
- `RELAY_OPERATOR_TOKEN`

`POSTGRES_PASSWORD` is interpolated into `RELAY_DATABASE_URL` by Compose, so use URL-safe characters unless you intentionally edit the Compose file to provide an encoded DSN.

The Compose file fixes the non-secret runtime defaults for production operations:

- Relay listens in-container on `0.0.0.0:8586`
- Docker publishes Relay to the host on `127.0.0.1:8586`
- Relay disables embedded STUN in Compose
- STUN runs as a separate `stun` service on UDP `0.0.0.0:3478`
- Docker publishes STUN directly to the host on UDP `3478`; nginx does not proxy STUN
- PostgreSQL uses database `agent_tunnel`
- PostgreSQL uses role `relay_user`
- PostgreSQL stores data in Docker volume `relay-postgres-data`

For `relay-cn`, point `agentunnel.cn` and `www.agentunnel.cn` at the VPS for nginx HTTP/WebSocket traffic. Point `stun.agentunnel.cn` at the same VPS for direct UDP STUN, and allow inbound `3478/udp` in the cloud security group and any host firewall.

## Ansible Compose Commands

These targets sync Compose assets and run Docker Compose on the remote host. They do not build local Go binaries.

| Command | What it does |
| --- | --- |
| `make compose-sync-dev` | Sync Compose files and `latest.sql` to dev |
| `make compose-sync-relay-cn` | Sync Compose files and `latest.sql` to relay-cn |
| `make compose-pull-dev` | Pull the configured Relay image on dev |
| `make compose-pull-relay-cn` | Pull the configured Relay image on relay-cn |
| `make compose-pull-stun-relay-cn` | Pull the configured STUN image on relay-cn |
| `make compose-pull-stack-relay-cn` | Pull all configured Compose images on relay-cn |
| `make compose-up-dev` | Pull and run only the Relay service on dev |
| `make compose-up-relay-cn` | Pull and run only the Relay service on relay-cn |
| `make compose-up-stun-relay-cn` | Pull and run only the STUN service on relay-cn |
| `make compose-up-stack-relay-cn` | Pull and run the full stack on relay-cn |
| `make compose-start-dev` | Start existing Compose services on dev |
| `make compose-start-relay-cn` | Start existing Compose services on relay-cn |
| `make compose-stop-dev` | Stop Compose services on dev without removing containers |
| `make compose-stop-relay-cn` | Stop Compose services on relay-cn without removing containers |
| `make compose-down-dev` | Stop and remove Compose containers on dev while keeping named volumes |
| `make compose-down-relay-cn` | Stop and remove Compose containers on relay-cn while keeping named volumes |

Typical relay-cn update:

```bash
make compose-sync-relay-cn
ssh relay-cn-host 'sudoedit /opt/agentunnel/compose/.env'  # update RELAY_IMAGE_TAG
make compose-up-relay-cn
```

Rare STUN update:

```bash
ssh relay-cn-host 'sudoedit /opt/agentunnel/compose/.env'  # update STUN_IMAGE_TAG
make compose-up-stun-relay-cn
```

First split-service rollout or intentional full-stack restart:

```bash
make compose-sync-relay-cn
ssh relay-cn-host 'sudoedit /opt/agentunnel/compose/.env'  # set RELAY_IMAGE_TAG and STUN_IMAGE_TAG
make compose-up-stack-relay-cn
make relay-cn-status
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
sudo docker compose --env-file .env logs --tail 100 stun
sudo tail -f /opt/agentunnel/logs/relay/relay.log
```

Healthy `healthz` output should be JSON with `"status":"ok"`.
Use `make relay-cn-status` from the checkout to verify nginx route coverage, current plus legacy HTTP/WebSocket paths, and the public STUN Binding response from `stun.agentunnel.cn:3478`. If it reports website HTML fallback for a relay websocket path, re-render nginx with `make nginx-relay-cn`.

## Website Deploy

Website deploy remains separate:

```bash
make deploy-website-dev
make deploy-website-relay-cn
```

Website deploy builds `../agent-tunnel-website`, uploads a release under `/var/www/agentunnel-website/releases`, and atomically repoints `/var/www/agentunnel-website/current`.
