.PHONY: release-package test-release-package test-release-installer test-release-publish

RELEASE_PACKAGE_SCRIPT := ./scripts/release-package.sh
TEST_RELEASE_PACKAGE_SCRIPT := ./scripts/test-release-package.sh
TEST_RELEASE_INSTALLER_SCRIPT := ./scripts/test-release-installer.sh
TEST_RELEASE_PUBLISH_SCRIPT := ./scripts/test-release-publish.sh

release-package: ## Package tunnel release archives into $(RELEASE_DIR)/$(RELEASE_VERSION). Requires RELEASE_VERSION=vX.Y.Z.
	@if [ -z "$(RELEASE_VERSION)" ]; then \
		printf 'error: RELEASE_VERSION is required, for example make release-package RELEASE_VERSION=v0.1.0\n' >&2; \
		exit 1; \
	fi
	@RELEASE_DIR="$(RELEASE_DIR)" GO="$(GO)" "$(RELEASE_PACKAGE_SCRIPT)" "$(RELEASE_VERSION)"

test-release-package: ## Run fixture-backed smoke tests for tunnel release packaging.
	@GO="$(GO)" "$(TEST_RELEASE_PACKAGE_SCRIPT)"

test-release-installer: ## Run fixture-backed smoke tests for the public tunnel installer.
	@GO="$(GO)" "$(TEST_RELEASE_INSTALLER_SCRIPT)"

test-release-publish: ## Run dry-run smoke tests for the public tunnel release publisher.
	@GO="$(GO)" "$(TEST_RELEASE_PUBLISH_SCRIPT)"
