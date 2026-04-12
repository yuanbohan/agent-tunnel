.PHONY: all build build-linux migrate clean vet test test-relay

all: build ## Build default local binaries.

build: ## Build local `tunnel`, `relay`, and `relay-migrate` binaries into `$(BIN_DIR)`.
	mkdir -p "$(BIN_DIR)"
	$(GO) build -o "$(TUNNEL_BIN)" $(TUNNEL_PKG)
	$(GO) build -o "$(RELAY_BUILD_BIN)" $(RELAY_PKG)
	$(GO) build -o "$(MIGRATOR_BUILD_BIN)" $(MIGRATOR_PKG)

build-linux: ## Build Linux amd64 `tunnel`, `relay`, and `relay-migrate` binaries into `$(BIN_DIR)`. Relay and migrator are stripped (`-s -w`) to shrink deploy uploads.
	@printf '🔨 Building Linux binaries...\n'
	@mkdir -p "$(BIN_DIR)"
	@GOOS=linux GOARCH=amd64 $(GO) build -o "$(TUNNEL_BIN)" $(TUNNEL_PKG)
	@GOOS=linux GOARCH=amd64 $(GO) build -ldflags="-s -w" -o "$(RELAY_BUILD_BIN)" $(RELAY_PKG)
	@GOOS=linux GOARCH=amd64 $(GO) build -ldflags="-s -w" -o "$(MIGRATOR_BUILD_BIN)" $(MIGRATOR_PKG)
	@printf '✅ Linux binaries are ready in %s\n' "$(BIN_DIR)"

migrate: ## Run relay schema migrations locally using `$(ENV_FILE)` (default `.env.prod`) or the shell environment.
	$(GO) run $(MIGRATOR_PKG) --schema-dir ./schema $(MIGRATOR_ARGS)

clean: ## Remove built binaries from `$(BIN_DIR)`.
	rm -rf "$(BIN_DIR)"

vet: ## Run `go vet` across all packages.
	$(GO) vet ./...

test: ## Run the full Go test suite.
	$(GO) test ./...

test-relay: ## Run the focused protocol and relay package tests.
	$(GO) test ./internal/protocol ./internal/relay/...
