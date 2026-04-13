.PHONY: deploy-install deploy-install-relay deploy-install-migrator deploy-schema deploy-env deploy-migrate deploy-schema-migrate deploy-restart deploy deploy-dev deploy-prod deploy-website deploy-website-dev deploy-website-prod

DEPLOY_SCRIPT := ./scripts/deploy.sh
DEPLOY_WEBSITE_SCRIPT := ./scripts/deploy-website.sh
DEPLOY_SCRIPT_FLAGS := $(if $(filter 1 true TRUE yes YES on ON,$(DEPLOY_VERBOSE)),--verbose,) $(if $(filter 1 true TRUE yes YES on ON,$(DEPLOY_DRY_RUN)),--dry-run,)

deploy-install-migrator: ## Build, upload, and install the relay migrator on the remote host when it changes. Does not install or reconfigure host infrastructure.
	@$(DEPLOY_SCRIPT) install-migrator $(DEPLOY_SCRIPT_FLAGS)

deploy-install-relay: ## Build, upload, and install the relay binary on the remote host when it changes. Does not install or reconfigure host infrastructure.
	@$(DEPLOY_SCRIPT) install-relay $(DEPLOY_SCRIPT_FLAGS)

deploy-install: ## Build, upload, and install the relay and migrator binaries on the remote host when they change. Does not install or reconfigure host infrastructure.
	@$(DEPLOY_SCRIPT) install $(DEPLOY_SCRIPT_FLAGS)

deploy-schema: ## Sync relay schema SQL files to the remote host. Does not touch nginx/certbot/postgresql packages or configs.
	@$(DEPLOY_SCRIPT) sync-schema $(DEPLOY_SCRIPT_FLAGS)

deploy-env: ## Install local `$(ENV_FILE)` on the remote host as `$(DEPLOY_ENV_FILE)`. Does not touch nginx/certbot/postgresql packages or configs.
	@$(DEPLOY_SCRIPT) install-env $(DEPLOY_SCRIPT_FLAGS)

deploy-migrate: ## Run relay schema migrations on the remote host using the selected local env file. Does not touch nginx/certbot/postgresql packages or configs.
	@$(DEPLOY_SCRIPT) migrate $(DEPLOY_SCRIPT_FLAGS)

deploy-schema-migrate: deploy-migrate ## Alias for schema sync + migrate using the selected local env file.

deploy-restart: ## Restart the relay systemd service on the remote host. Does not touch nginx/certbot/postgresql packages or configs.
	@$(DEPLOY_SCRIPT) restart $(DEPLOY_SCRIPT_FLAGS)

deploy: ## Build, safely rerun migrations, install the relay, sync env, and restart the relay on the remote host. Does not install or reconfigure nginx/certbot/postgresql.
	@$(DEPLOY_SCRIPT) deploy $(DEPLOY_SCRIPT_FLAGS)

deploy-dev: ## Convenience: `make deploy` using `.env.dev`.
	@$(MAKE) deploy ENV_FILE=.env.dev

deploy-prod: ## Convenience: `make deploy` using `.env.prod`.
	@$(MAKE) deploy ENV_FILE=.env.prod

deploy-website: ## Build the website from `$(WEBSITE_REPO_DIR)` and publish it as an atomic remote release. Does not install or reconfigure nginx/certbot/postgresql.
	@$(DEPLOY_WEBSITE_SCRIPT) deploy $(DEPLOY_SCRIPT_FLAGS)

deploy-website-dev: ## Convenience: `make deploy-website` using `.env.dev`.
	@$(MAKE) deploy-website ENV_FILE=.env.dev

deploy-website-prod: ## Convenience: `make deploy-website` using `.env.prod`.
	@$(MAKE) deploy-website ENV_FILE=.env.prod
