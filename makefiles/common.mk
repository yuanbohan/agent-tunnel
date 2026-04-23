# Runtime configuration.

## RELAY_BIN: Relay binary path used by `make start`.
RELAY_BIN ?= ./relay
## RELAY_PORT: Relay port used by `make start` and `make status`.
RELAY_PORT ?= 8586
## GO: Go toolchain binary used by build and test targets.
GO ?= go
## BIN_DIR: Output directory for built binaries.
BIN_DIR ?= bin
## RELEASE_DIR: Output directory root for packaged tunnel release artifacts.
RELEASE_DIR ?= dist/releases

## INSTALL_DIR: Install destination for `make install`.
INSTALL_DIR ?= $(HOME)/.local/bin

## LOG_DIR: Directory for relay background logs and pid files.
LOG_DIR ?= logs
## RELAY_PID_FILE: PID file used by `make start`, `make stop`, and `make status`.
RELAY_PID_FILE ?= $(LOG_DIR)/relay.pid
## RELAY_LOG_FILE: Relay log file written by `make start`.
RELAY_LOG_FILE ?= $(LOG_DIR)/relay.log

## RELAY_SCHEMA_DIR: Remote directory for relay SQL files.
RELAY_SCHEMA_DIR ?= /etc/agentunnel/schema
## ANSIBLE: Binary used for automation playbooks.
ANSIBLE ?= ansible-playbook
## ANSIBLE_CONFIG: Path to ansible config file for this project.
ANSIBLE_CONFIG ?= $(CURDIR)/ansible/ansible.cfg
## ANSIBLE_ROLES_PATH: Role search path used by Ansible playbooks in this project.
ANSIBLE_ROLES_PATH ?= $(CURDIR)/ansible/roles
## ANSIBLE_STDOUT_CALLBACK: Callback plugin used for Ansible command output.
ANSIBLE_STDOUT_CALLBACK ?= default
## ANSIBLE_CALLBACK_RESULT_FORMAT: Output format used by the default Ansible callback.
ANSIBLE_CALLBACK_RESULT_FORMAT ?= yaml
## ANSIBLE_PROJECT_ROOT: Repository root passed to Ansible for local artifact paths.
ANSIBLE_PROJECT_ROOT ?= $(shell pwd)
## ANSIBLE_INVENTORY: Inventory file for target host.
ANSIBLE_INVENTORY ?= ansible/inventories/dev.yml
## ANSIBLE_PLAYBOOK: Primary Ansible playbook file.
ANSIBLE_PLAYBOOK ?= ansible/playbooks/site.yml
## ANSIBLE_EXTRA_VARS_FILE: Optional YAML/JSON vars file (recommended for secrets).
ANSIBLE_EXTRA_VARS_FILE ?=
## ANSIBLE_WEBSITE_REPO_DIR: Absolute path to the website checkout for remote website deploy.
ANSIBLE_WEBSITE_REPO_DIR ?= $(abspath $(WEBSITE_REPO_DIR))
## ANSIBLE_TAGS: Optional comma-separated tag list passed to ansible-playbook.
ANSIBLE_TAGS ?=
## ANSIBLE_DRY_RUN: Set to 1 to run ansible in check mode.
ANSIBLE_DRY_RUN ?= 0
## DEPLOY_RETRY_COUNT: Retry count for relay binary upload targets when SSH file transfer flakes.
DEPLOY_RETRY_COUNT ?= 3
## DEPLOY_RETRY_DELAY: Seconds to wait between retry attempts for relay binary upload targets.
DEPLOY_RETRY_DELAY ?= 2
## RELAY_COMPOSE_ACTION: Compose lifecycle action for compose-* deploy targets (`sync`, `pull`, `up`, `start`, `stop`, `down`).
RELAY_COMPOSE_ACTION ?= sync
## RELAY_CN_SSH_DEST: SSH destination used by relay-cn helper targets.
RELAY_CN_SSH_DEST ?= ubuntu@8.133.195.191
## RELAY_CN_COMPOSE_DIR: Remote Compose directory used by relay-cn helper targets.
RELAY_CN_COMPOSE_DIR ?= /opt/agentunnel/compose
## RELAY_CN_INVITE_COUNT: Invite count used by `make relay-cn-invite-create`.
RELAY_CN_INVITE_COUNT ?= 3
## RELAY_CN_INVITE_EXPIRES_IN: Invite expiry used by `make relay-cn-invite-create`.
RELAY_CN_INVITE_EXPIRES_IN ?= 7d
## RELAY_CN_INVITE_CODE: Invite code used by `make relay-cn-invite-disable`.
RELAY_CN_INVITE_CODE ?=
## RELAY_CN_USERNAME: Username used by `make relay-cn-user-delete`.
RELAY_CN_USERNAME ?=

export ANSIBLE_CONFIG ANSIBLE_ROLES_PATH ANSIBLE_STDOUT_CALLBACK ANSIBLE_CALLBACK_RESULT_FORMAT

## WEBSITE_REPO_DIR: Local checkout used to build the deployable website bundle.
WEBSITE_REPO_DIR ?= ../agent-tunnel-website
## WEBSITE_BUILD_DIR: Build output directory inside `$(WEBSITE_REPO_DIR)`.
WEBSITE_BUILD_DIR ?= dist
## LOCAL_E2E_PG_IMAGE: Fixed Docker image tag used for local E2E PostgreSQL.
LOCAL_E2E_PG_IMAGE ?= postgres:16.11-alpine
## LOCAL_E2E_PG_CONTAINER: Docker container name used for local E2E PostgreSQL.
LOCAL_E2E_PG_CONTAINER ?= agent-tunnel-e2e-postgres
## LOCAL_E2E_PG_VOLUME: Docker volume name used for local E2E PostgreSQL.
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
	@printf "Targets:\n\n"
	@awk '\
	function group_name(file) {\
		if (file ~ /\/Makefile$$/ || file == "Makefile") return "General";\
		if (file ~ /common\.mk$$/) return "General";\
		if (file ~ /runtime\.mk$$/) return "Runtime";\
		if (file ~ /build\.mk$$/) return "Build and test";\
		if (file ~ /release\.mk$$/) return "Release";\
		if (file ~ /install\.mk$$/) return "Install";\
		if (file ~ /deploy\.mk$$/) return "Deploy";\
		if (file ~ /local-e2e\.mk$$/) return "Local E2E";\
		return "Other";\
	}\
	BEGIN { FS = ":.*## " }\
	/^[a-zA-Z0-9_.-]+:.*## / {\
		group = group_name(FILENAME);\
		if (!(group in seen)) {\
			order[++count] = group;\
			seen[group] = 1;\
		}\
		entries[group] = entries[group] sprintf("  %-22s %s\n", $$1, $$2);\
	}\
	END {\
		for (i = 1; i <= count; i++) {\
			group = order[i];\
			printf "%s:\n%s\n", group, entries[group];\
		}\
	}' $(MAKEFILE_LIST)
	@printf "\nVariables:\n"
	@awk '/^## [A-Z0-9_]+: / {line=$$0; sub(/^## /, "", line); name=line; desc=line; sub(/: .*/, "", name); sub(/^[^:]+: /, "", desc); printf "  %-22s %s\n", name, desc}' $(MAKEFILE_LIST)
	@printf "\nExamples:\n"
	@printf "  make <target>\n"
	@printf "  make migrate\n"
	@printf "  make start RELAY_BIN=./bin/relay\n"
