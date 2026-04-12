.PHONY: all build build-linux install migrate clean vet test test-relay

all: build ## Build default local binaries.

build: ## Build local `tunnel`, `relay`, and `relay-migrate` binaries into `$(BIN_DIR)`.
	mkdir -p "$(BIN_DIR)"
	go build -o "$(TUNNEL_BIN)" $(TUNNEL_PKG)
	go build -o "$(RELAY_BUILD_BIN)" $(RELAY_PKG)
	go build -o "$(MIGRATOR_BUILD_BIN)" $(MIGRATOR_PKG)

build-linux: ## Build Linux amd64 `tunnel`, `relay`, and `relay-migrate` binaries into `$(BIN_DIR)`. Relay and migrator are stripped (`-s -w`) to shrink deploy uploads.
	@printf '🔨 Building Linux binaries...\n'
	@mkdir -p "$(BIN_DIR)"
	@GOOS=linux GOARCH=amd64 go build -o "$(TUNNEL_BIN)" $(TUNNEL_PKG)
	@GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o "$(RELAY_BUILD_BIN)" $(RELAY_PKG)
	@GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o "$(MIGRATOR_BUILD_BIN)" $(MIGRATOR_PKG)
	@printf '✅ Linux binaries are ready in %s\n' "$(BIN_DIR)"

install: build ## Install `tunnel`, `relay`, and `relay-migrate` into `$(INSTALL_DIR)`.
	@set -e; \
	mkdir -p "$(INSTALL_DIR)"; \
	rm -f "$(INSTALL_TUNNEL_BIN)" "$(INSTALL_RELAY_BIN)" "$(INSTALL_MIGRATOR_BIN)"; \
	cp -f "$(TUNNEL_BIN)" "$(RELAY_BUILD_BIN)" "$(MIGRATOR_BUILD_BIN)" "$(INSTALL_DIR)/"; \
	echo "installed tunnel, relay, and relay-migrate to $(INSTALL_DIR)"

migrate: ## Run relay schema migrations locally using `$(ENV_FILE)` (default `.env.prod`) or the shell environment.
	go run $(MIGRATOR_PKG) --schema-dir ./schema $(MIGRATOR_ARGS)

clean: ## Remove built binaries from `$(BIN_DIR)`.
	rm -rf "$(BIN_DIR)"

vet: ## Run `go vet` across all packages.
	go vet ./...

test: ## Run the full Go test suite.
	go test ./...

test-relay: ## Run the focused protocol and relay package tests.
	go test ./internal/protocol ./internal/relay/...
