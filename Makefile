.DEFAULT_GOAL := help

.PHONY: all help start stop status build build-linux deploy install clean vet test test-relay

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
## DEPLOY_HOST: SSH host for `make deploy`.
DEPLOY_HOST ?= diarome
## DEPLOY_RELAY_PATH: Remote path for the relay binary.
DEPLOY_RELAY_PATH ?= ~/relay
## DEPLOY_SERVICE: Systemd service name restarted by `make deploy`.
DEPLOY_SERVICE ?= agentunnel-relay
## DEPLOY_INSTALL_PATH: Installed relay path used by systemd ExecStart.
DEPLOY_INSTALL_PATH ?= /usr/local/bin/relay

# Internal paths shared by related targets.
TUNNEL_PKG := ./cmd/agentunnel
RELAY_PKG := ./cmd/relay
TUNNEL_BIN := $(BIN_DIR)/tunnel
RELAY_BUILD_BIN := $(BIN_DIR)/relay
INSTALL_TUNNEL_BIN := $(INSTALL_DIR)/tunnel
INSTALL_RELAY_BIN := $(INSTALL_DIR)/relay

# Default target.
all: build ## Build default local binaries.

# Help and discovery.
help: ## Show available targets and configurable variables.
	@printf "Usage: make <target>\n\n"
	@printf "Targets:\n"
	@awk 'BEGIN {FS = ":.*## "}; /^[a-zA-Z0-9_.-]+:.*## / {printf "  %-14s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@printf "\nVariables:\n"
	@awk '/^## [A-Z0-9_]+: / {line=$$0; sub(/^## /, "", line); name=line; desc=line; sub(/: .*/, "", name); sub(/^[^:]+: /, "", desc); printf "  %-14s %s\n", name, desc}' $(MAKEFILE_LIST)
	@printf "\nExamples:\n"
	@printf "  make build\n"
	@printf "  make start RELAY_BIN=./bin/relay\n"

# Local runtime commands.
start: ## Start the relay binary in the background and write the configured pid file.
	@set -e; \
	mkdir -p "$(LOG_DIR)"; \
	if [ -f "$(RELAY_PID_FILE)" ] && kill -0 "$$(cat "$(RELAY_PID_FILE)")" 2>/dev/null; then \
		echo "relay is already running (pid=$$(cat "$(RELAY_PID_FILE)"))"; \
		exit 1; \
	fi; \
	if [ ! -x "$(RELAY_BIN)" ]; then \
		echo "relay binary not found or not executable: $(RELAY_BIN)"; \
		exit 1; \
	fi; \
	nohup "$(RELAY_BIN)" --port "$(RELAY_PORT)" >> "$(RELAY_LOG_FILE)" 2>&1 & \
	echo $$! > "$(RELAY_PID_FILE)"; \
	sleep 1; \
	pid="$$(cat "$(RELAY_PID_FILE)")"; \
	if kill -0 "$$pid" 2>/dev/null; then \
		echo "relay started in background (pid=$$pid, port=$(RELAY_PORT), log=$(RELAY_LOG_FILE))"; \
	else \
		rm -f "$(RELAY_PID_FILE)"; \
		echo "relay failed to start; check $(RELAY_LOG_FILE)"; \
		exit 1; \
	fi

stop: ## Stop the background relay process tracked in the configured pid file.
	@set -e; \
	if [ ! -f "$(RELAY_PID_FILE)" ]; then \
		echo "relay is not running ($(RELAY_PID_FILE) not found)"; \
		exit 1; \
	fi; \
	pid="$$(cat "$(RELAY_PID_FILE)")"; \
	if kill -0 "$$pid" 2>/dev/null; then \
		kill "$$pid"; \
		echo "relay stopped (pid=$$pid)"; \
	else \
		echo "relay process already exited (pid=$$pid)"; \
	fi; \
	rm -f "$(RELAY_PID_FILE)"

status: ## Show relay process status and inspect the configured listen port.
	@set -e; \
	echo "relay status"; \
	if [ -f "$(RELAY_PID_FILE)" ]; then \
		pid="$$(cat "$(RELAY_PID_FILE)")"; \
		if kill -0 "$$pid" 2>/dev/null; then \
			echo "- pid: $$pid (running)"; \
		else \
			echo "- pid: $$pid (stale pid file, process not running)"; \
		fi; \
	else \
		echo "- pid: not found ($(RELAY_PID_FILE))"; \
	fi; \
	echo "- port: $(RELAY_PORT)"; \
	if command -v ss >/dev/null 2>&1; then \
		ss -lntp | awk 'NR==1 || $$4 ~ /:$(RELAY_PORT)$$/'; \
	elif command -v lsof >/dev/null 2>&1; then \
		lsof -nP -iTCP:$(RELAY_PORT) -sTCP:LISTEN; \
	else \
		echo "no ss/lsof available to inspect listening sockets"; \
	fi

# Build and install commands.
build: ## Build local `tunnel` and `relay` binaries into `$(BIN_DIR)`.
	mkdir -p "$(BIN_DIR)"
	go build -o "$(TUNNEL_BIN)" $(TUNNEL_PKG)
	go build -o "$(RELAY_BUILD_BIN)" $(RELAY_PKG)

build-linux: ## Build Linux amd64 `tunnel` and `relay` binaries into `$(BIN_DIR)`.
	mkdir -p "$(BIN_DIR)"
	GOOS=linux GOARCH=amd64 go build -o "$(TUNNEL_BIN)" $(TUNNEL_PKG)
	GOOS=linux GOARCH=amd64 go build -o "$(RELAY_BUILD_BIN)" $(RELAY_PKG)

install: build ## Install `tunnel` and `relay` into `$(INSTALL_DIR)`.
	@set -e; \
	mkdir -p "$(INSTALL_DIR)"; \
	rm -f "$(INSTALL_TUNNEL_BIN)" "$(INSTALL_RELAY_BIN)"; \
	cp -f "$(TUNNEL_BIN)" "$(RELAY_BUILD_BIN)" "$(INSTALL_DIR)/"; \
	echo "installed tunnel and relay to $(INSTALL_DIR)"

deploy: build-linux ## Build, upload, and restart the relay on the remote host.
	scp $(RELAY_BUILD_BIN) $(DEPLOY_HOST):$(DEPLOY_RELAY_PATH)
	ssh $(DEPLOY_HOST) 'sudo install -m 0755 $(DEPLOY_RELAY_PATH) $(DEPLOY_INSTALL_PATH) && sudo systemctl restart $(DEPLOY_SERVICE)'

clean: ## Remove built binaries from `$(BIN_DIR)`.
	rm -rf "$(BIN_DIR)"

# Verification commands.
vet: ## Run `go vet` across all packages.
	go vet ./...

test: ## Run the full Go test suite.
	go test ./...

test-relay: ## Run the focused protocol and relay package tests.
	go test ./protocol ./relay
