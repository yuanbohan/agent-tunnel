---
date: 2026-04-22
topic: relay-docker-deployment
---

# Relay Docker Deployment

## Problem Frame

Relay deployment currently depends on locally built binaries, Ansible syncing those binaries, systemd, host PostgreSQL provisioning, and the `relay-migrate` schema runner. The desired operational model is simpler: build a Relay container image from tagged source, publish it to GitHub Container Registry, deploy Relay and PostgreSQL with Docker Compose, and let Ansible only synchronize Compose/runtime configuration and start or stop services.

The database workflow also changes. Docker/PostgreSQL bootstrap should use a complete `latest.sql` schema snapshot for fresh databases. Future schema changes are applied manually on the server, and every schema change must keep `latest.sql` complete enough to recreate the full current schema from an empty database.

---

## Actors

- A1. Maintainer: tags releases, reviews deployment docs, and updates schema artifacts when database shape changes.
- A2. GitHub Actions workflow: validates the tagged revision, builds the Relay image, embeds release metadata, and pushes the image to GHCR.
- A3. Remote deployment automation: synchronizes Compose files and environment placeholders to the server, then runs Docker Compose lifecycle commands.
- A4. Relay operator: manages secrets in a remote `.env`, starts/stops services, creates invites, and manually applies future SQL changes.
- A5. PostgreSQL container: initializes a fresh data volume from the checked-in complete schema snapshot.

---

## Key Flows

- F1. Tagged image release
  - **Trigger:** A maintainer pushes a semver tag such as `v0.1.0`.
  - **Actors:** A1, A2
  - **Steps:** GitHub Actions checks out the tag, runs tests, builds the Relay Docker image with embedded version/build metadata, verifies the image reports the tag version, logs in to GHCR, and pushes an immutable image tag matching the git tag.
  - **Outcome:** GHCR contains a Relay image tag such as `v0.1.0` that can be used directly by Compose.
  - **Covered by:** R1, R2, R3, R4

- F2. Fresh Compose bootstrap
  - **Trigger:** A relay host is initialized with an empty PostgreSQL volume.
  - **Actors:** A3, A4, A5
  - **Steps:** Deployment automation syncs Compose files, the operator provides `.env`, Compose starts PostgreSQL with a named persistent volume, PostgreSQL runs `latest.sql` once through its init mechanism, then Relay starts against that database.
  - **Outcome:** The host has a running Relay service backed by a PostgreSQL schema that matches the repository's current schema snapshot.
  - **Covered by:** R5, R6, R7, R8, R9, R10

- F3. Routine service update
  - **Trigger:** A new Relay image tag has been published and should be deployed.
  - **Actors:** A3, A4
  - **Steps:** The remote `.env` is updated to the desired image tag, deployment automation syncs Compose files, runs Compose pull/up, and Relay restarts with the new image while PostgreSQL data remains mounted.
  - **Outcome:** Relay runs the requested version without rebuilding binaries or running automatic database migrations.
  - **Covered by:** R3, R7, R11, R12

- F4. Manual schema change
  - **Trigger:** A code change requires PostgreSQL schema changes after the database already exists.
  - **Actors:** A1, A4
  - **Steps:** The repository updates `latest.sql` to represent the full new schema, documents or commits the manual SQL needed to mutate existing databases, the operator executes that SQL on the server, then deploys the compatible Relay image.
  - **Outcome:** Existing production data is changed intentionally by a human-operated SQL step, and a fresh database can still be recreated from `latest.sql`.
  - **Covered by:** R13, R14, R15, R16

---

## Requirements

**Container Image**

- R1. Add a Relay-focused Dockerfile that builds only the `cmd/relay` binary for Linux and packages it into a small runtime image suitable for long-running server use.
- R2. The Docker build must embed release metadata into `internal/buildinfo`: version from the git tag, git commit, git branch/ref, build time, and official release marker when building tagged images.
- R3. The default container command must start `relay serve`, and the container must listen on an address usable inside Docker networking, not the relay's current loopback-only default.
- R4. The image build must be verifiable in CI by running the image and checking `relay version` reports the triggering tag.

**GitHub Container Registry Release**

- R5. Add a GitHub Actions workflow triggered by pushed semver tags such as `v0.1.0`; workflow dispatch may exist as a manual fallback, but tag push is the release path.
- R6. The workflow must run the relevant Go tests before pushing an image.
- R7. The workflow must push the Relay image to GHCR with an image tag exactly matching the git tag, for example `v0.1.0`.
- R8. The workflow must use GitHub's package publishing permission path (`packages: write`) and avoid introducing a custom registry token unless GitHub requires it.
- R9. Compose deployments must pin explicit version tags by default. A mutable `latest` tag is not the deployment source of truth.

**Compose Deployment**

- R10. Add Docker Compose configuration that runs Relay and PostgreSQL together, with PostgreSQL data in a named persistent volume.
- R11. Compose must read sensitive runtime values from a remote `.env` file, with a committed example file showing required keys but no real secrets.
- R12. Required environment must include database credentials or DSN inputs, `RELAY_APP_SECRET`, `RELAY_OPERATOR_TOKEN`, the Relay image tag/version, and the Relay listen/host port configuration.
- R13. The Relay service must depend on PostgreSQL health before startup, but it must not run schema migrations automatically.
- R14. The Compose topology must keep the host reverse-proxy model viable: nginx can continue proxying `/api/`, `/agent/ws`, `/device/ws`, and `/healthz` to the local Relay port, while operator routes remain host-local.
- R15. The deployment files must support Ansible's new role as a thin file sync and lifecycle runner: sync Compose assets, then execute `docker compose pull`, `up -d`, `stop`, `start`, or `down` as needed.

**Schema Snapshot and Manual Changes**

- R16. Add a complete `latest.sql` schema snapshot for fresh PostgreSQL initialization, based on the current schema represented by the existing SQL migrations.
- R17. PostgreSQL Compose initialization must mount `latest.sql` into the official PostgreSQL init directory so it runs only for a fresh empty data volume.
- R18. Future schema changes must update `latest.sql` in the same change that changes repository code depending on the new schema.
- R19. Future schema changes for existing servers must be manually applied by the operator; the Docker deployment flow must not silently mutate existing databases.
- R20. The current migration implementation and existing migration SQL should not be broken or deleted as part of introducing this new flow; they may remain available for tests, legacy local workflows, or later cleanup.

**Documentation**

- R21. Update `README.md` quick-start/deployment guidance to describe the Docker Compose deployment path, GHCR image tags, `.env`, PostgreSQL volume behavior, and manual schema-change responsibility.
- R22. Update `docs/deploy.md` and `docs/operation.md` so the primary relay deployment path is Compose-based, while any remaining Ansible content describes syncing Compose files and running Compose lifecycle commands.
- R23. Update `docs/release-distribution.md` or a dedicated deployment release doc to explain that the `Release Tunnel` workflow remains for public CLI binaries, while the new Relay workflow publishes the Relay image to GHCR.
- R24. Update `CLAUDE.md` and therefore `AGENTS.md` to state that `latest.sql` must always recreate the full current PostgreSQL schema and that existing-server schema changes are applied manually.

---

## Acceptance Examples

- AE1. **Covers R5, R7.** Given a maintainer pushes `v0.1.0`, when the Relay image workflow completes, GHCR has a Relay image tagged `v0.1.0`, not only `latest` or a SHA tag.
- AE2. **Covers R2, R4.** Given the workflow built from tag `v0.1.0`, when CI runs `relay version` inside the image, the output includes `relay v0.1.0`.
- AE3. **Covers R10, R16, R17.** Given an empty Docker volume, when `docker compose up -d` starts PostgreSQL, the database contains all current Relay tables from `latest.sql`.
- AE4. **Covers R13, R19.** Given an existing PostgreSQL volume, when a new Relay image is deployed with Compose, no automatic migration or `latest.sql` replay mutates the database.
- AE5. **Covers R14.** Given nginx already proxies to the local Relay port, when Compose starts Relay, `/api/`, `/agent/ws`, `/device/ws`, and `/healthz` remain reachable through the existing reverse-proxy pattern.

---

## Success Criteria

- A tagged Relay release can be produced without local binary packaging or manual image tagging.
- A remote host can be updated by syncing Compose files, updating `.env`, pulling the image, and restarting services.
- Fresh PostgreSQL bootstraps are reproducible from `latest.sql`.
- Existing database schema changes are explicit human operations, not hidden deployment side effects.
- Current non-Docker development, tests, and legacy migration code continue to work until deliberately removed in a separate change.

---

## Scope Boundaries

- This does not redesign Relay auth, APIs, session routing, or PostgreSQL repository behavior.
- This does not publish `tunnel` CLI binaries or replace the existing public `yuanbohan/tunnel` distribution workflow.
- This does not require bundled nginx, TLS, or certbot inside Compose for the first version; the existing host nginx model can remain.
- This does not automate production schema migrations. Manual SQL execution is intentional.
- This does not require deleting `cmd/migrate`, `internal/migration`, or existing numbered SQL files in the same implementation.
- This does not manage backups, point-in-time recovery, or PostgreSQL major-version upgrades, though docs should warn operators not to treat the named volume as a backup.

---

## Key Decisions

- Use tag push as the primary image release trigger: this makes the git tag and Docker tag share one source of truth.
- Keep Compose deployments pinned to immutable semver image tags: this prevents a remote host from drifting because a mutable tag changed.
- Use `latest.sql` only for fresh database initialization: the official PostgreSQL init directory naturally avoids replaying it against existing volumes.
- Keep schema mutation manual for existing servers: this matches the operator preference and makes database changes explicit.
- Introduce Docker deployment alongside existing migration/binary tooling first: this reduces rollout risk and avoids breaking current workflows while the new path settles.

---

## Dependencies / Assumptions

- The repository has a symlinked `AGENTS.md -> CLAUDE.md`; updating `CLAUDE.md` satisfies both unless that structure changes.
- The current schema is represented by `schema/0001_auth_schema.sql` and `schema/0002_operator_audit.sql`; `latest.sql` should include their resulting full schema.
- The relay currently defaults to `127.0.0.1:8586`, so Compose must override listen address to `0.0.0.0:8586`.
- The current host nginx config can keep terminating TLS and proxying to a local Relay port.
- GHCR package naming is fixed to `ghcr.io/yuanbohan/agent-tunnel-relay` and clearly documented for Compose.

---

## Outstanding Questions

### Deferred to Planning

- [Resolved][Affects R7][Technical] Use the exact GHCR image name `ghcr.io/yuanbohan/agent-tunnel-relay`.
- [Affects R10, R12][Technical] Decide whether Compose should construct `RELAY_DATABASE_URL` from PostgreSQL variables or require the operator to provide the full DSN directly.
- [Affects R15][Technical] Decide whether to modify existing Ansible roles now or add a separate minimal Compose deploy path first.
- [Affects R16][Technical] Decide whether `latest.sql` should live at `schema/latest.sql` or under a deployment-specific directory while still being the canonical full snapshot.

---

## Next Steps

-> `/ce-plan` for structured implementation planning
