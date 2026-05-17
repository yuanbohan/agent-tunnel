# Local E2E Guide

This document explains the local end-to-end regression path for the real relay stack:

- real PostgreSQL
- real schema migration via `cmd/migrate`
- real relay via `cmd/relay serve`
- real operator invite creation
- real register/login/password-change APIs
- real agent token issuance
- real `tunnel auth login`
- real `tunnel` startup
- real local PTY input/output while removed Relay session routes stay absent

Everything here is intentionally local-only. Do not point this flow at `agentunnel.cn` or any other remote relay.

The recommended local database for this workflow is a Dockerized `postgres:16.11-alpine` started through the repo script and Make targets added for local E2E.

## What This Test Proves

`make test-local-e2e` is not a unit test and not a relay-only integration test. It exercises the user-facing path that matters for local development:

1. operator creates an invite
2. user registers with that invite
3. user logs in and gets an app session
4. user creates an agent token, either directly over HTTP or through `tunnel auth login`
5. a real `tunnel` process connects to the local relay
6. the removed Relay session discovery route returns the standard endpoint-not-found envelope
7. the local PTY emits deterministic output
8. the harness sends real local terminal input back into the tunnel
9. password change revokes app sessions without stopping the tunnel
10. the old app token is rejected
11. the user logs back in with the new password
12. PostgreSQL durable state matches the expected auth and token changes

It is designed to catch regressions across the seams between auth, HTTP handlers, Relay agent registration, local PTY behavior, removed Relay session routes, and durable auth state.

## Scope Boundary

This flow does verify:

- local relay startup and schema migration
- public HTTP auth flows
- removed Relay session discovery behavior
- local PTY input/output through `tunnel run`
- tunnel registration and reconnect-sensitive session identity
- password-change side effects on app sessions while the tunnel stays online
- durable PostgreSQL mutations for invites, users, app sessions, and agent tokens

This flow does not verify:

- any hosted environment
- CI deployment wiring
- transcript retention, because the relay does not persist transcript history
- relay in-memory session state via SQL, because live session ownership is intentionally not durable

## Architecture View

```mermaid
flowchart LR
    A[Test Harness] --> B[Build local binaries]
    B --> C[Run migrator]
    C --> D[(Local PostgreSQL)]
    B --> E[Start relay serve on 127.0.0.1]
    A --> F[Create invite via relay CLI]
    A --> G[Register and login over HTTP]
    G --> E
    A --> H[Create agent token over HTTP]
    H --> E
    A --> I[Start real tunnel]
    I --> E
    I --> J[Deterministic e2e-launcher]
    A --> K[Verify removed Relay session route]
    K --> E
    A --> L[Write local PTY input]
    L --> I
    A --> M[Change password over HTTP]
    M --> E
    A --> N[Re-login with new password]
    N --> E
    A --> O[Assert durable DB rows]
    O --> D
```

## Automated Flow

The default fully automatic entry point is:

```bash
make test-local-e2e-clean
```

This path does all of the following without manual intervention:

1. reset the Docker PostgreSQL container and volume before the run
2. start a fresh fixed-version PostgreSQL container
3. run the local E2E flow against that fresh database
4. save the full command output to a gitignored local file
5. reset the Docker PostgreSQL container and volume again at the end

The command fails if either the E2E scenario itself fails or the final Docker reset fails.

The default output file is:

```text
tmp/local-e2e/latest.log
```

The database helper uses a fixed PostgreSQL image tag:

- `postgres:16.11-alpine`

and these default local settings:

- container: `agent-tunnel-e2e-postgres`
- volume: `agent-tunnel-e2e-pgdata`
- host: `127.0.0.1`
- port: `55432`
- database: `agent_tunnel_e2e`
- user: `agentunnel`
- password: `agentunnel`

That means the default DSN is:

```text
postgres://agentunnel:agentunnel@127.0.0.1:55432/agent_tunnel_e2e?sslmode=disable
```

If you want the lower-level Docker-backed path without automatic cleanup, that entry point is:

```bash
make local-e2e-db-up
make test-local-e2e-docker
```

If you already have a separate local PostgreSQL instance you want to use, the generic entry point is still:

```bash
export AGENTUNNEL_TEST_DATABASE_URL=postgres://relay_user:change-me-db-password@localhost/agent_tunnel_e2e?sslmode=disable
make test-local-e2e
```

`make test-local-e2e-clean` uses `scripts/local-e2e-run.sh` to orchestrate the full clean lifecycle and to write the full run output into `tmp/local-e2e/latest.log`.

`make local-e2e-db-up` starts the fixed-version Docker PostgreSQL container and waits until both the container health check and a real `select 1` SQL probe succeed.

Human-facing `up` and `status` output redact the password in the DSN. If you need the raw DSN for scripting, use `./scripts/local-e2e-postgres.sh dsn`.

`make test-local-e2e` sets `AGENTUNNEL_RUN_LOCAL_E2E=1` and runs `go test ./internal/e2e -count=1 -v`.

`make test-local-e2e-docker` first ensures the Docker PostgreSQL instance is up, then injects the generated DSN into the same test package.

### Detailed Step-by-Step

The test implementation lives in `internal/e2e/`. At a high level it runs these steps:

1. Build real binaries

   The harness builds:

   - `./cmd/migrate`
   - `./cmd/relay`
   - `./cmd/tunnel`
   - `./cmd/e2e-launcher`

2. Run real schema migrations

   The harness executes the migrator binary against `AGENTUNNEL_TEST_DATABASE_URL`.

3. Start a real local relay

   The relay listens on a random loopback address such as `127.0.0.1:<port>`. The harness waits for `GET /healthz` before continuing.

4. Create a real invite code

   The test calls:

   ```bash
   relay invite create --count 1 --expires-in 7d
   ```

   This goes through the operator CLI path, not a mock.

5. Register a brand-new user over HTTP

   The test calls:

   - `POST /api/auth/register`

   with a unique username and the invite code.

6. Log in over HTTP

   The test calls:

   - `POST /api/auth/login`

   and receives a real app session access token.

7. Create an agent token over HTTP

   The test calls:

   - `POST /api/agent-tokens`

8. Start a real `tunnel` process

   The tunnel is started against the local relay, not a hosted relay. The regression package now covers both auth entrypoints:

   - direct env override with `TUNNEL_AUTH_TOKEN`
   - `tunnel auth login`, which writes `~/.tunnel/auth.json`, followed by `tunnel run` without `TUNNEL_AUTH_TOKEN`

   In both cases the tunnel launches a deterministic helper program named `e2e-launcher`.

9. Verify removed Relay session discovery

   The test calls the removed Relay session list route and expects the standard endpoint-not-found envelope. This prevents the local regression harness from accidentally depending on the deleted Relay session data plane.

10. Verify real terminal output

    The helper launcher prints a deterministic banner:

    - `READY e2e-launcher`

    The test asserts that this banner appears in the local tunnel PTY output.

11. Send real local terminal input

    The test writes `ping` to the local tunnel PTY and waits for output containing:

    - `REPLY ping`

12. Change password over HTTP

    The test calls:

    - `POST /api/auth/password/change`

    and verifies that the running tunnel session is not stopped by app-session revocation.

13. Verify old app session is revoked

    The test reuses the old access token against:

    - `GET /api/account/policy`

    and expects `401`.

14. Log in again with the new password

    The test creates a fresh app session through `POST /api/auth/login`.

15. Verify PostgreSQL durable state

    The test checks:

    - `users`: the user row exists
    - `invite_codes`: the invite is marked consumed by that user
    - `app_sessions`: there are two sessions, the first revoked with `password_changed`, the second still active
    - `agent_tokens`: one token exists, it is active, and `last_used_at` is set

## Why The Helper Launcher Exists

`tunnel` expects to launch a real command and own its PTY. For E2E validation we need terminal behavior that is deterministic enough to assert from a test.

`cmd/e2e-launcher` is that command. It:

- prints `READY e2e-launcher`
- echoes submitted lines as `REPLY <command>`
- handles `CR`, `LF`, and `CRLF`
- exits on `exit`

That gives the test a stable way to verify:

- local terminal startup
- local PTY input/output
- app auth revocation without stopping the running tunnel

## Sequence Diagram

```mermaid
sequenceDiagram
    participant T as go test ./internal/e2e
    participant PG as Local PostgreSQL
    participant R as relay serve
    participant C as App Client
    participant U as tunnel
    participant L as e2e-launcher

    T->>PG: migrate schema
    T->>R: start on 127.0.0.1
    T->>R: relay invite create
    C->>R: POST /api/auth/register
    C->>R: POST /api/auth/login
    C->>R: POST /api/agent-tokens
    T->>U: start tunnel with local base URL
    U->>R: /agent/ws register session
    U->>L: launch PTY command
    C->>R: removed Relay session route returns endpoint-not-found
    T->>U: write "ping" to local PTY
    L-->>U: REPLY ping
    C->>R: POST /api/auth/password/change
    R-->>C: revoke old app session
    C->>R: POST /api/auth/login with new password
    T->>PG: assert durable rows
```

## How Collaborators Should Run It

Every collaborator should use their own local PostgreSQL database and keep this test pointed at loopback. The default path should be the pinned Docker PostgreSQL instance unless there is a clear reason to use a different local Postgres.

### 1. Use the clean one-command flow

Recommended:

```bash
make test-local-e2e-clean
```

This gives you:

- a fresh PostgreSQL container and volume at the start
- a full run log in `tmp/local-e2e/latest.log`
- no leftover Docker container or volume after the run finishes

If the run fails, the output file still remains so it can be reviewed after cleanup. That includes failures from the final cleanup step itself.

### 2. Use the lower-level Docker commands only when you are debugging the environment

These are still useful when you want to inspect PostgreSQL manually during a run:

```bash
make local-e2e-db-up
make local-e2e-db-status
make local-e2e-db-logs
make test-local-e2e-docker
```

Useful checks:

```bash
make local-e2e-db-status
make local-e2e-db-logs
```

If you need a clean database:

```bash
make local-e2e-db-reset
make local-e2e-db-up
```

If you want to use a different local PostgreSQL instance, export `AGENTUNNEL_TEST_DATABASE_URL` yourself instead.

Use a dedicated database instead of a shared development database so the E2E run can create and mutate auth state freely.

### 3. Run the automated regression with a custom local PostgreSQL instance

```bash
make test-local-e2e-docker
```

Run this after changes in:

- auth
- removed Relay session route behavior
- WebSocket transport
- tunnel registration or input handling
- password-change revocation logic
- migrations or auth persistence

### 4. Run the manual acceptance path when behavior feels risky

Use the same Docker-backed database as the automated test:

```bash
export AGENTUNNEL_TEST_DATABASE_URL="$(./scripts/local-e2e-postgres.sh dsn)"
export RELAY_DATABASE_URL="$AGENTUNNEL_TEST_DATABASE_URL"
export RELAY_APP_SECRET=change-me
export RELAY_OPERATOR_TOKEN=change-me-operator-token
export RELAY_LISTEN_ADDR=127.0.0.1:8586
```

Then bring up the local stack:

```bash
go run ./cmd/migrate --schema-dir ./schema
go run ./cmd/relay serve --listen-addr "$RELAY_LISTEN_ADDR"
```

Create an invite:

```bash
go run ./cmd/relay invite create --count 1 --expires-in 7d
```

Then validate this checklist:

1. Register a fresh user or log in with an existing user stored in that same local database.
2. Create or reuse an agent token owned by that same user.
3. Start `tunnel` against `http://127.0.0.1:8586`.
4. Confirm removed Relay session routes return endpoint-not-found.
5. Confirm local terminal input/output works through the tunnel PTY.
6. Change the password.
7. Confirm the old app token is unauthorized.
8. Log in with the new password while the original tunnel stays online.

### 5. Inspect the database when you need to debug state

The automated test already asserts DB state, but these queries are useful when a collaborator wants to inspect the run manually:

```sql
select id, username, username_norm, created_at
from users
order by id desc
limit 5;

select id, consumed_at, consumed_by_user_id, consumed_by_username
from invite_codes
order by id desc
limit 5;

select id, user_id, revoked_at, revoke_reason, created_at
from app_sessions
order by created_at desc
limit 10;

select id, user_id, name, last_used_at, revoked_at, created_at
from agent_tokens
order by created_at desc
limit 10;
```

## Failure Triage

When the test fails, split the failure by layer:

- migration or startup failure:
  check PostgreSQL availability, `AGENTUNNEL_TEST_DATABASE_URL`, and `make local-e2e-db-status`
- invite/register/login failure:
  inspect relay logs and auth handler behavior
- removed session route failure:
  check router `NoRoute` behavior and the local E2E client assertion
- local PTY input/output failure:
  check tunnel startup logs, `/agent/ws` registration, and the e2e launcher process
- password-change failure:
  inspect app-session revocation and tunnel liveness handling
- DB assertion failure:
  inspect auth persistence and migration shape

## Current Entry Points

- clean automated run with captured output: `make test-local-e2e-clean`
- Docker PostgreSQL up: `make local-e2e-db-up`
- Docker PostgreSQL status: `make local-e2e-db-status`
- Docker PostgreSQL reset: `make local-e2e-db-reset`
- Docker-backed automated run: `make test-local-e2e-docker`
- automated: `make test-local-e2e`
- test package: `go test ./internal/e2e -count=1 -v`
- primary doc: `docs/local-e2e.md`

Use the automated path as the default regression guard. Use the manual path when you need to judge the real local interactive experience end to end.
