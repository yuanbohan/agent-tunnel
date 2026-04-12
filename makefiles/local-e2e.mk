.PHONY: test-local-e2e local-e2e-db-up local-e2e-db-down local-e2e-db-reset local-e2e-db-status local-e2e-db-logs test-local-e2e-docker test-local-e2e-clean

test-local-e2e: ## Run the local end-to-end regression flow against a configured local PostgreSQL database.
	@test -n "$$AGENTUNNEL_TEST_DATABASE_URL" || (echo "AGENTUNNEL_TEST_DATABASE_URL is required"; exit 1)
	AGENTUNNEL_RUN_LOCAL_E2E=1 go test ./internal/e2e -count=1 -v

local-e2e-db-up: ## Start the fixed-version Docker PostgreSQL used for local E2E and wait until it accepts queries.
	@$(LOCAL_E2E_PG_ENV) "$(LOCAL_E2E_PG_SCRIPT)" up

local-e2e-db-down: ## Stop and remove the Docker PostgreSQL container used for local E2E while keeping its volume.
	@$(LOCAL_E2E_PG_ENV) "$(LOCAL_E2E_PG_SCRIPT)" down

local-e2e-db-reset: ## Stop and remove the Docker PostgreSQL container and its named volume for a clean local E2E database.
	@$(LOCAL_E2E_PG_ENV) "$(LOCAL_E2E_PG_SCRIPT)" reset

local-e2e-db-status: ## Show Docker PostgreSQL status, health, and DSN details for local E2E.
	@$(LOCAL_E2E_PG_ENV) "$(LOCAL_E2E_PG_SCRIPT)" status

local-e2e-db-logs: ## Show recent Docker PostgreSQL logs for the local E2E database.
	@$(LOCAL_E2E_PG_ENV) "$(LOCAL_E2E_PG_SCRIPT)" logs

test-local-e2e-docker: ## Start fixed-version Docker PostgreSQL and run the local end-to-end regression against it.
	@set -e; \
	$(LOCAL_E2E_PG_ENV) "$(LOCAL_E2E_PG_SCRIPT)" up; \
	dsn="$$( $(LOCAL_E2E_PG_ENV) "$(LOCAL_E2E_PG_SCRIPT)" dsn )"; \
	AGENTUNNEL_TEST_DATABASE_URL="$$dsn" AGENTUNNEL_RUN_LOCAL_E2E=1 go test ./internal/e2e -count=1 -v

test-local-e2e-clean: ## Reset Docker PostgreSQL, run local E2E, save output to `$(LOCAL_E2E_OUTPUT_FILE)`, and clean up Docker state afterward.
	@LOCAL_E2E_OUTPUT_FILE="$(LOCAL_E2E_OUTPUT_FILE)" $(LOCAL_E2E_PG_ENV) "$(LOCAL_E2E_RUNNER_SCRIPT)"
