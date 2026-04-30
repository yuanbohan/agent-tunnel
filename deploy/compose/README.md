# Relay Compose Deployment

This Compose project runs the Relay service and PostgreSQL. It is intended to be copied to a server, configured with a private `.env`, and managed with `docker compose`.

## Configure

```sh
install -m 600 /dev/null .env
```

Populate `.env` with `RELAY_IMAGE_TAG`, `POSTGRES_PASSWORD`, `RELAY_APP_SECRET`, and `RELAY_OPERATOR_TOKEN`. Set `RELAY_IMAGE_TAG` to an immutable release tag such as `v0.1.0`. Do not use a mutable tag as the deployment source of truth.

`POSTGRES_PASSWORD` is interpolated into `RELAY_DATABASE_URL`, so use URL-safe characters for the password unless you replace the composed DSN with an explicitly encoded one in `compose.yaml`.
The checked-in `.env.example` is a local reference file; the Ansible sync path intentionally does not upload it to the server.
The Compose file fixes these non-secret runtime defaults:

- Relay listens in-container on `0.0.0.0:8586`
- Docker publishes Relay to the host as `127.0.0.1:8586`
- Relay listens for Binding-only STUN on UDP `0.0.0.0:3478`
- Docker publishes STUN to the host as UDP `3478`
- PostgreSQL uses database `agent_tunnel`
- PostgreSQL uses role `relay_user`
- PostgreSQL stores data in Docker volume `relay-postgres-data`

## Start

```sh
docker compose --env-file .env pull
docker compose --env-file .env up -d
docker compose --env-file .env ps
curl -fsS http://127.0.0.1:8586/healthz
```

PostgreSQL stores data in the fixed `relay-postgres-data` named volume. Relay structured logs are written to `../logs/relay/relay.log` on the host. `../postgres/latest.sql` is mounted into the official PostgreSQL init directory and runs only when that volume is empty.

Expose UDP `3478` through the host firewall and point the STUN hostname, for example `stun.<relay-domain>`, at the same edge host. This STUN service is Binding-only; it does not provide TURN relay or media forwarding.

## Update

Change `RELAY_IMAGE_TAG` in `.env`, then run:

```sh
docker compose --env-file .env pull relay
docker compose --env-file .env up -d relay
```

The Compose flow does not run schema migrations. If a release needs a schema change for an existing database, apply that SQL manually before starting the compatible Relay image.

## Operator Commands

Run operator commands inside the Relay container so they use the same private environment:

```sh
docker compose --env-file .env exec relay relay invite create --count 3 --expires-in 7d
docker compose --env-file .env exec relay relay invite list
docker compose --env-file .env exec relay relay user delete --username alice
```
