.PHONY: _deploy-built _deploy-ansible _deploy-website \
	init-dev init-prod \
	deploy-dev deploy-prod \
	deploy-website-dev deploy-website-prod \
	deps-dev deps-prod \
	postgres-dev postgres-prod \
	nginx-dev nginx-prod \
	certbot-dev certbot-prod \
	schema-dev schema-prod \
	migrator-dev migrator-prod \
	relay-bin-dev relay-bin-prod \
	env-dev env-prod \
	migrate-dev migrate-prod \
	compose-sync-dev compose-sync-prod \
	compose-pull-dev compose-pull-prod \
	compose-up-dev compose-up-prod \
	compose-start-dev compose-start-prod \
	compose-stop-dev compose-stop-prod \
	compose-down-dev compose-down-prod \
	restart-dev restart-prod \
	relay-dev relay-prod

DEPLOY_DEFAULT_TAGS := schema,relay-migrator,migrate,relay-binary,relay-env,relay-service,relay-restart
DEPLOY_MIGRATOR_TAGS := relay-migrator
DEPLOY_RELAY_BINARY_TAGS := relay-binary
DEPLOY_RELAY_TAGS := relay-env,relay-service,relay-restart
DEPLOY_MIGRATE_TAGS := schema,migrate
DEPLOY_COMPOSE_TAGS := relay-compose
INIT_DEV_TAGS := deps,nginx
INIT_PROD_TAGS := deps,nginx,certbot

init-dev: ## Bootstrap the dev host for Compose: install base packages and render the HTTP nginx site. Use on first machine bootstrap.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS="$(INIT_DEV_TAGS)"

init-prod: ## Bootstrap the prod host for Compose: install nginx/certbot, render HTTP nginx, issue TLS cert, then re-render nginx with TLS. Use on first machine bootstrap.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS="$(INIT_PROD_TAGS)"
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS=nginx

_deploy-built:
	@$(MAKE) build-linux
	@$(MAKE) _deploy-ansible ANSIBLE_TAGS="$(strip $(ANSIBLE_TAGS))"

_deploy-ansible:
	@$(MAKE) ansible-run

deps-dev: ## Install remote OS packages for the dev host. Use on first machine bootstrap or when package requirements change.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS=deps

deps-prod: ## Install remote OS packages for the prod host. Use on first machine bootstrap or when package requirements change.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS=deps

postgres-dev: ## Legacy/systemd only: ensure host PostgreSQL user/database/password state. Do not use for Docker Compose deployments.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS=postgres

postgres-prod: ## Legacy/systemd only: ensure host PostgreSQL user/database/password state. Do not use for Docker Compose deployments.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS=postgres

nginx-dev: ## Render `/etc/nginx/...` config, site files, websocket map, and reload nginx. Use after domain/upstream/reverse-proxy changes.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS=nginx

nginx-prod: ## Render `/etc/nginx/...` config, site files, websocket map, and reload nginx. Use after domain/upstream/reverse-proxy changes.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS=nginx

certbot-dev: ## Manage `/etc/letsencrypt/...` certificate issuance, renewal hook, and timer. Use for first TLS setup or domain/email/cert renewal changes.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS=certbot

certbot-prod: ## Manage `/etc/letsencrypt/...` certificate issuance, renewal hook, and timer. Use for first TLS setup or domain/email/cert renewal changes.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS=certbot

schema-dev: ## Sync local `schema/` files to the remote schema dir (default `/etc/agentunnel/schema`). Use after SQL migration files change.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS=schema

schema-prod: ## Sync local `schema/` files to the remote schema dir (default `/etc/agentunnel/schema`). Use after SQL migration files change.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS=schema

migrator-dev: ## Build `relay-migrate` locally and install it on the dev host. Use after migrator code changes or when the remote migrator binary is missing.
	@$(MAKE) _deploy-built ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS="$(DEPLOY_MIGRATOR_TAGS)"

migrator-prod: ## Build `relay-migrate` locally and install it on the prod host. Use after migrator code changes or when the remote migrator binary is missing.
	@$(MAKE) _deploy-built ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS="$(DEPLOY_MIGRATOR_TAGS)"

relay-bin-dev: ## Build `relay` locally and install it on the dev host. Use after relay code changes or when the remote relay binary is missing.
	@set -e; \
	attempt=1; \
	while [ "$$attempt" -le "$(DEPLOY_RETRY_COUNT)" ]; do \
		echo "relay-bin-dev attempt $$attempt/$(DEPLOY_RETRY_COUNT)"; \
		if $(MAKE) _deploy-built ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS="$(DEPLOY_RELAY_BINARY_TAGS)"; then \
			exit 0; \
		fi; \
		if [ "$$attempt" -eq "$(DEPLOY_RETRY_COUNT)" ]; then \
			exit 1; \
		fi; \
		attempt=$$((attempt + 1)); \
		sleep "$(DEPLOY_RETRY_DELAY)"; \
	done

relay-bin-prod: ## Build `relay` locally and install it on the prod host. Use after relay code changes or when the remote relay binary is missing.
	@set -e; \
	attempt=1; \
	while [ "$$attempt" -le "$(DEPLOY_RETRY_COUNT)" ]; do \
		echo "relay-bin-prod attempt $$attempt/$(DEPLOY_RETRY_COUNT)"; \
		if $(MAKE) _deploy-built ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS="$(DEPLOY_RELAY_BINARY_TAGS)"; then \
			exit 0; \
		fi; \
		if [ "$$attempt" -eq "$(DEPLOY_RETRY_COUNT)" ]; then \
			exit 1; \
		fi; \
		attempt=$$((attempt + 1)); \
		sleep "$(DEPLOY_RETRY_DELAY)"; \
	done

env-dev: ## Render the remote relay env file (default `/etc/agentunnel/relay.env`). Use after relay config or secret changes.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS=relay-env

env-prod: ## Render the remote relay env file (default `/etc/agentunnel/relay.env`). Use after relay config or secret changes.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS=relay-env

migrate-dev: ## Sync `schema/` and run DB migrations on the dev host. Use after SQL changes once the remote `relay-migrate` binary is already in place.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS="$(DEPLOY_MIGRATE_TAGS)"

migrate-prod: ## Sync `schema/` and run DB migrations on the prod host. Use after SQL changes once the remote `relay-migrate` binary is already in place.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS="$(DEPLOY_MIGRATE_TAGS)"

compose-sync-dev: ## Sync Docker Compose relay assets to the dev host without starting services.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=sync

compose-sync-prod: ## Sync Docker Compose relay assets to the prod host without starting services.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=sync

compose-pull-dev: ## Pull configured Relay/PostgreSQL images on the dev host.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=pull

compose-pull-prod: ## Pull configured Relay/PostgreSQL images on the prod host.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=pull

compose-up-dev: ## Pull and start the Docker Compose relay stack on the dev host.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=up

compose-up-prod: ## Pull and start the Docker Compose relay stack on the prod host.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=up

compose-start-dev: ## Start existing Docker Compose relay services on the dev host.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=start

compose-start-prod: ## Start existing Docker Compose relay services on the prod host.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=start

compose-stop-dev: ## Stop Docker Compose relay services on the dev host without removing containers.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=stop

compose-stop-prod: ## Stop Docker Compose relay services on the prod host without removing containers.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=stop

compose-down-dev: ## Stop and remove Docker Compose relay containers on the dev host while keeping named volumes.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=down

compose-down-prod: ## Stop and remove Docker Compose relay containers on the prod host while keeping named volumes.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=down

restart-dev: ## Restart only the remote `agentunnel-relay` systemd service. Use after manual config fixes or to bounce the process without redeploying binaries.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS=relay-restart

restart-prod: ## Restart only the remote `agentunnel-relay` systemd service. Use after manual config fixes or to bounce the process without redeploying binaries.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS=relay-restart

relay-dev: ## Render relay env and systemd config on the dev host, then restart the relay service. Use after config changes once the remote relay binary is already in place.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS="$(DEPLOY_RELAY_TAGS)"

relay-prod: ## Render relay env and systemd config on the prod host, then restart the relay service. Use after config changes once the remote relay binary is already in place.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS="$(DEPLOY_RELAY_TAGS)"

deploy-dev: ## Full relay deploy: build Linux binaries, install `relay-migrate`, sync schema, run migrations, install relay, render env/systemd, and restart relay.
	@$(MAKE) _deploy-built ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS="$(DEPLOY_DEFAULT_TAGS)"

deploy-prod: ## Full relay deploy: build Linux binaries, install `relay-migrate`, sync schema, run migrations, install relay, render env/systemd, and restart relay.
	@$(MAKE) _deploy-built ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS="$(DEPLOY_DEFAULT_TAGS)"

_deploy-website:
	@$(MAKE) ansible-run ANSIBLE_TAGS="$(if $(strip $(ANSIBLE_TAGS)),$(strip $(ANSIBLE_TAGS)),website)"

deploy-website-dev: ## Build the website locally, upload it to dev, switch `/var/www/agentunnel-website/current`, and reload nginx. Use after website frontend changes.
	@$(MAKE) _deploy-website ANSIBLE_INVENTORY=ansible/inventories/dev.yml

deploy-website-prod: ## Build the website locally, upload it to prod, switch `/var/www/agentunnel-website/current`, and reload nginx. Use after website frontend changes.
	@$(MAKE) _deploy-website ANSIBLE_INVENTORY=ansible/inventories/prod.yml
