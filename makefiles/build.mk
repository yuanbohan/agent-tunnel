GIT_COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
GIT_BRANCH ?= $(shell git rev-parse --abbrev-ref HEAD 2>/dev/null || echo unknown)
BUILD_TIME ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

LDFLAGS_BASE := -X yuanbohan/tunnel/internal/buildinfo.GitCommit=$(GIT_COMMIT) -X yuanbohan/tunnel/internal/buildinfo.GitBranch=$(GIT_BRANCH) -X yuanbohan/tunnel/internal/buildinfo.BuildTime=$(BUILD_TIME)

.PHONY: all build build-linux docker-relay-image-test tag-version migrate clean vet test test-relay _resolve_version

all: build ## Build default local binaries.

_resolve_version:
	$(eval VERSION_VAL := $(shell ./scripts/git-version.sh "$(VERSION)"))
	$(eval LDFLAGS := $(LDFLAGS_BASE) -X yuanbohan/tunnel/internal/buildinfo.Version=$(VERSION_VAL))
	@printf '🚀 Building version: %s\n' "$(VERSION_VAL)"

build: _resolve_version ## Build local `tunnel`, `relay`, and `relay-migrate` binaries.
	mkdir -p "$(BIN_DIR)"
	$(GO) build -ldflags="$(LDFLAGS)" -o "$(TUNNEL_BIN)" $(TUNNEL_PKG)
	$(GO) build -ldflags="$(LDFLAGS)" -o "$(RELAY_BUILD_BIN)" $(RELAY_PKG)
	$(GO) build -ldflags="$(LDFLAGS)" -o "$(MIGRATOR_BUILD_BIN)" $(MIGRATOR_PKG)

build-linux: _resolve_version ## Build Linux binaries with stripped symbols.
	@printf '🔨 Building Linux binaries...\n'
	@mkdir -p "$(BIN_DIR)"
	@GOOS=linux GOARCH=amd64 $(GO) build -ldflags="$(LDFLAGS)" -o "$(TUNNEL_BIN)" $(TUNNEL_PKG)
	@GOOS=linux GOARCH=amd64 $(GO) build -ldflags="-s -w $(LDFLAGS)" -o "$(RELAY_BUILD_BIN)" $(RELAY_PKG)
	@GOOS=linux GOARCH=amd64 $(GO) build -ldflags="-s -w $(LDFLAGS)" -o "$(MIGRATOR_BUILD_BIN)" $(MIGRATOR_PKG)
	@printf '✅ Linux binaries are ready in %s\n' "$(BIN_DIR)"

docker-relay-image-test: ## Build the Relay Docker image and verify its embedded version metadata.
	@VERSION="$(VERSION)" ./scripts/test-relay-docker-image.sh "$$(./scripts/git-version.sh "$(VERSION)")"

tag-version: ## Resolve the next version, create the git tag if needed, and push it to origin.
	@version=$$(./scripts/git-version.sh --push "$(VERSION)"); \
	printf 'pushed version tag: %s\n' "$$version"

migrate: ## Run relay schema migrations locally using the current shell environment.
	$(GO) run $(MIGRATOR_PKG) --schema-dir ./schema $(MIGRATOR_ARGS)

clean: ## Remove built binaries from `$(BIN_DIR)`.
	rm -rf "$(BIN_DIR)"

vet: ## Run `go vet` across all packages.
	$(GO) vet ./...

test: ## Run the full Go test suite.
	$(GO) test ./...

test-relay: ## Run the focused protocol and relay package tests.
	$(GO) test ./internal/protocol ./internal/relay/...
