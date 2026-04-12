.PHONY: help

# Runtime configuration. These can be overridden on the command line.
## RELAY_BIN: Relay binary path used by `make start`.
RELAY_BIN ?= ./relay
## RELAY_PORT: Relay port used by `make start` and `make status`.
RELAY_PORT ?= 8586
## BIN_DIR: Output directory for built binaries.
BIN_DIR ?= bin
## INSTALL_DIR: Install destination for `make install`.
INSTALL_DIR ?= $(HOME)/.local/bin
## LOG_DIR: Directory for relay background logs and pid files.
LOG_DIR ?= logs
## RELAY_PID_FILE: PID file used by `make start`, `make stop`, and `make status`.
RELAY_PID_FILE ?= $(LOG_DIR)/relay.pid
## RELAY_LOG_FILE: Relay log file written by `make start`.
RELAY_LOG_FILE ?= $(LOG_DIR)/relay.log
## DEPLOY_HOST: SSH host for deploy targets.
DEPLOY_HOST ?= diarome
## DEPLOY_RELAY_PATH: Remote staging path for the relay binary.
DEPLOY_RELAY_PATH ?= ~/relay
## DEPLOY_MIGRATOR_PATH: Remote staging path for the relay migrator binary.
DEPLOY_MIGRATOR_PATH ?= ~/relay-migrate
## DEPLOY_SERVICE: Systemd service name restarted by deploy targets.
DEPLOY_SERVICE ?= agentunnel-relay
## DEPLOY_INSTALL_PATH: Installed relay path used by systemd ExecStart.
DEPLOY_INSTALL_PATH ?= /usr/local/bin/relay
## DEPLOY_MIGRATOR_INSTALL_PATH: Installed relay migrator path.
DEPLOY_MIGRATOR_INSTALL_PATH ?= /usr/local/bin/relay-migrate
## DEPLOY_ENV_FILE: Remote env file installed by `make deploy-env` and read by systemd on service start.
DEPLOY_ENV_FILE ?= /etc/agentunnel/relay.env
## DEPLOY_RELAY_LOG_FILE: Relay log file path written into the deployed remote env file.
DEPLOY_RELAY_LOG_FILE ?= /var/log/agentunnel/relay.log
## DEPLOY_SCHEMA_DIR: Remote directory containing relay schema SQL files.
DEPLOY_SCHEMA_DIR ?= /etc/agentunnel/schema
## MIGRATOR_ARGS: Extra arguments passed to the remote migrator, for example `--baseline 0002_operator_audit.sql`.
MIGRATOR_ARGS ?=
## DEPLOY_VERBOSE: Set to 1 to print deploy debug details.
DEPLOY_VERBOSE ?= 0
## DEPLOY_DRY_RUN: Set to 1 for a structured deploy preview without executing commands.
DEPLOY_DRY_RUN ?= 0
## LOCAL_E2E_PG_IMAGE: Fixed Docker image tag used for local E2E PostgreSQL.
LOCAL_E2E_PG_IMAGE ?= postgres:16.11-alpine
## LOCAL_E2E_PG_CONTAINER: Docker container name used for local E2E PostgreSQL.
LOCAL_E2E_PG_CONTAINER ?= agent-tunnel-e2e-postgres
## LOCAL_E2E_PG_VOLUME: Docker volume name used for local E2E PostgreSQL data.
LOCAL_E2E_PG_VOLUME ?= agent-tunnel-e2e-pgdata
## LOCAL_E2E_PG_PORT: Host port bound for local E2E PostgreSQL.
LOCAL_E2E_PG_PORT ?= 55432
## LOCAL_E2E_PG_HOST: Hostname used in the local E2E PostgreSQL DSN.
LOCAL_E2E_PG_HOST ?= 127.0.0.1
## LOCAL_E2E_PG_USER: Username provisioned inside the local E2E PostgreSQL container.
LOCAL_E2E_PG_USER ?= agentunnel
## LOCAL_E2E_PG_PASSWORD: Password provisioned inside the local E2E PostgreSQL container.
LOCAL_E2E_PG_PASSWORD ?= agentunnel
## LOCAL_E2E_PG_DATABASE: Database provisioned inside the local E2E PostgreSQL container.
LOCAL_E2E_PG_DATABASE ?= agent_tunnel_e2e
## LOCAL_E2E_PG_READY_TIMEOUT: Seconds to wait for the local E2E PostgreSQL container to become queryable.
LOCAL_E2E_PG_READY_TIMEOUT ?= 30
## LOCAL_E2E_OUTPUT_FILE: Gitignored local file used to store the latest clean local E2E run output.
LOCAL_E2E_OUTPUT_FILE ?= tmp/local-e2e/latest.log

# Internal paths shared by related targets.
TUNNEL_PKG := ./cmd/tunnel
RELAY_PKG := ./cmd/relay
MIGRATOR_PKG := ./cmd/migrate
TUNNEL_BIN := $(BIN_DIR)/tunnel
RELAY_BUILD_BIN := $(BIN_DIR)/relay
MIGRATOR_BUILD_BIN := $(BIN_DIR)/relay-migrate
INSTALL_TUNNEL_BIN := $(INSTALL_DIR)/tunnel
INSTALL_RELAY_BIN := $(INSTALL_DIR)/relay
INSTALL_MIGRATOR_BIN := $(INSTALL_DIR)/relay-migrate
LOCAL_E2E_PG_SCRIPT := ./scripts/local-e2e-postgres.sh
LOCAL_E2E_RUNNER_SCRIPT := ./scripts/local-e2e-run.sh
LOCAL_E2E_PG_ENV = LOCAL_E2E_PG_IMAGE="$(LOCAL_E2E_PG_IMAGE)" LOCAL_E2E_PG_CONTAINER="$(LOCAL_E2E_PG_CONTAINER)" LOCAL_E2E_PG_VOLUME="$(LOCAL_E2E_PG_VOLUME)" LOCAL_E2E_PG_PORT="$(LOCAL_E2E_PG_PORT)" LOCAL_E2E_PG_HOST="$(LOCAL_E2E_PG_HOST)" LOCAL_E2E_PG_USER="$(LOCAL_E2E_PG_USER)" LOCAL_E2E_PG_PASSWORD="$(LOCAL_E2E_PG_PASSWORD)" LOCAL_E2E_PG_DATABASE="$(LOCAL_E2E_PG_DATABASE)" LOCAL_E2E_PG_READY_TIMEOUT="$(LOCAL_E2E_PG_READY_TIMEOUT)"

help: ## Show available targets and configurable variables.
	@printf "Usage: make <target>\n\n"
	@printf "Targets:\n"
	@awk 'BEGIN {FS = ":.*## "}; /^[a-zA-Z0-9_.-]+:.*## / {printf "  %-22s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@printf "\nVariables:\n"
	@awk '/^## [A-Z0-9_]+: / {line=$$0; sub(/^## /, "", line); name=line; desc=line; sub(/: .*/, "", name); sub(/^[^:]+: /, "", desc); printf "  %-22s %s\n", name, desc}' $(MAKEFILE_LIST)
	@printf "\nExamples:\n"
	@printf "  make build\n"
	@printf "  make migrate\n"
	@printf "  make start RELAY_BIN=./bin/relay\n"
