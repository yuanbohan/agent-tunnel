.PHONY: deploy-install deploy-install-relay deploy-install-migrator deploy-schema deploy-env deploy-migrate deploy-schema-migrate deploy-restart deploy deploy-dev deploy-prod

DEPLOY_SCRIPT := ./scripts/deploy.sh
DEPLOY_SCRIPT_FLAGS := $(if $(filter 1 true TRUE yes YES on ON,$(DEPLOY_VERBOSE)),--verbose,) $(if $(filter 1 true TRUE yes YES on ON,$(DEPLOY_DRY_RUN)),--dry-run,)

deploy-install-migrator: ## Build, upload, and install the relay migrator on the remote host when it changes.
	@$(DEPLOY_SCRIPT) install-migrator $(DEPLOY_SCRIPT_FLAGS)

deploy-install-relay: ## Build, upload, and install the relay binary on the remote host when it changes.
	@$(DEPLOY_SCRIPT) install-relay $(DEPLOY_SCRIPT_FLAGS)

deploy-install: ## Build, upload, and install the relay and migrator binaries on the remote host when they change.
	@$(DEPLOY_SCRIPT) install $(DEPLOY_SCRIPT_FLAGS)

deploy-schema: ## Sync relay schema SQL files to the remote host.
	@$(DEPLOY_SCRIPT) sync-schema $(DEPLOY_SCRIPT_FLAGS)

deploy-env: ## Install local `$(ENV_FILE)` on the remote host as `$(DEPLOY_ENV_FILE)`.
	@$(DEPLOY_SCRIPT) install-env $(DEPLOY_SCRIPT_FLAGS)

deploy-migrate: ## Run relay schema migrations on the remote host using the selected local env file.
	@$(DEPLOY_SCRIPT) migrate $(DEPLOY_SCRIPT_FLAGS)

deploy-schema-migrate: ## Sync schema files and run relay migrations using the selected local env file.
	@$(DEPLOY_SCRIPT) migrate $(DEPLOY_SCRIPT_FLAGS)

deploy-restart: ## Restart the relay systemd service on the remote host.
	@$(DEPLOY_SCRIPT) restart $(DEPLOY_SCRIPT_FLAGS)

deploy: ## Build, safely rerun migrations, install the relay, sync env, and restart the relay on the remote host.
	@$(DEPLOY_SCRIPT) deploy $(DEPLOY_SCRIPT_FLAGS)

deploy-dev: ## Convenience: `make deploy` using `.env.dev`.
	@$(MAKE) deploy ENV_FILE=.env.dev

deploy-prod: ## Convenience: `make deploy` using `.env.prod`.
	@$(MAKE) deploy ENV_FILE=.env.prod
