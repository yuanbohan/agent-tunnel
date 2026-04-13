.PHONY: deploy-built deploy-install deploy-install-migrator deploy-install-relay deploy-schema deploy-env deploy-migrate deploy-schema-migrate deploy-restart deploy-postgres deploy-deps deploy-certbot deploy-nginx deploy-relay deploy deploy-dev deploy-prod deploy-website deploy-website-dev deploy-website-prod

DEPLOY_DEFAULT_TAGS := schema,migrate,relay-binary,relay-env,relay-service,relay-restart
DEPLOY_RELAY_TAGS := relay-binary,relay-env,relay-service,relay-restart
DEPLOY_RELAY_INSTALL_TAGS := relay-binary,relay-service
DEPLOY_BINARY_INSTALL_TAGS := relay-binary,relay-migrator,relay-service

deploy-built:
	@$(MAKE) build-linux
	@$(MAKE) deploy-ansible ANSIBLE_TAGS="$(strip $(ANSIBLE_TAGS))"

deploy-install-migrator: ## Build and install the migrator binary only.
	@$(MAKE) deploy-built ANSIBLE_TAGS=relay-migrator

deploy-install-relay: ## Build and install the relay binary only.
	@$(MAKE) deploy-built ANSIBLE_TAGS="$(DEPLOY_RELAY_INSTALL_TAGS)"

deploy-install: ## Build and install relay + migrator binaries.
	@$(MAKE) deploy-built ANSIBLE_TAGS="$(DEPLOY_BINARY_INSTALL_TAGS)"

deploy-schema: ## Sync schema SQL files to the remote host.
	@$(MAKE) deploy-ansible ANSIBLE_TAGS=schema

deploy-env: ## Write relay env file on the remote host.
	@$(MAKE) deploy-ansible ANSIBLE_TAGS=relay-env

deploy-migrate: ## Run relay migrations remotely using the current host env and schema.
	@$(MAKE) deploy-ansible ANSIBLE_TAGS="schema,migrate"

deploy-schema-migrate: deploy-migrate ## Alias for schema sync + migrate.

deploy-restart: ## Restart relay service.
	@$(MAKE) deploy-ansible ANSIBLE_TAGS=relay-restart

deploy-postgres: ## Ensure PostgreSQL is enabled and relay database user/db are present.
	@$(MAKE) deploy-ansible ANSIBLE_TAGS=postgres

deploy-deps: ## Install base OS packages required by relay/nginx/postgres/certbot.
	@$(MAKE) deploy-ansible ANSIBLE_TAGS=deps

deploy-certbot: ## Configure certbot renewal + TLS certificate issuance.
	@$(MAKE) deploy-ansible ANSIBLE_TAGS=certbot

deploy-nginx: ## Configure nginx reverse proxy and websocket/certbot mapping.
	@$(MAKE) deploy-ansible ANSIBLE_TAGS=nginx

deploy-relay: ## Deploy relay binaries/env/systemd and restart relay service.
	@$(MAKE) deploy-built ANSIBLE_TAGS="$(DEPLOY_RELAY_TAGS)"

deploy: ## Build linux binaries and run ansible to install schema, environment, run migrate, and restart relay on the selected host.
	@$(MAKE) deploy-built ANSIBLE_TAGS="$(DEPLOY_DEFAULT_TAGS)"

deploy-ansible:
	@$(MAKE) ansible-run

deploy-dev: ## One-shot deploy against the `dev` inventory.
	@$(MAKE) deploy ANSIBLE_INVENTORY=ansible/inventories/dev.yml

deploy-prod: ## One-shot deploy against the `prod` inventory.
	@$(MAKE) deploy ANSIBLE_INVENTORY=ansible/inventories/prod.yml

deploy-website: ## Build and publish website release only.
	@$(MAKE) ansible-run ANSIBLE_TAGS="$(if $(strip $(ANSIBLE_TAGS)),$(strip $(ANSIBLE_TAGS)),website)"

deploy-website-dev: ## Convenience website deploy for dev.
	@$(MAKE) deploy-website ANSIBLE_INVENTORY=ansible/inventories/dev.yml

deploy-website-prod: ## Convenience website deploy for prod.
	@$(MAKE) deploy-website ANSIBLE_INVENTORY=ansible/inventories/prod.yml
