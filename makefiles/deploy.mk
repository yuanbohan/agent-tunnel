.PHONY: _deploy-built _deploy-ansible _deploy-website \
	init-dev init-prod init-relay-cn \
	deploy-dev deploy-prod \
	deploy-website-dev deploy-website-prod deploy-website-relay-cn \
	deps-dev deps-prod deps-relay-cn \
	postgres-dev postgres-prod \
	nginx-dev nginx-prod nginx-relay-cn \
	certbot-dev certbot-prod certbot-relay-cn \
	schema-dev schema-prod \
	migrator-dev migrator-prod \
	relay-bin-dev relay-bin-prod \
	env-dev env-prod \
	migrate-dev migrate-prod \
	compose-sync-dev compose-sync-prod compose-sync-relay-cn \
	compose-pull-dev compose-pull-prod compose-pull-relay-cn \
	compose-pull-stun-dev compose-pull-stun-prod compose-pull-stun-relay-cn \
	compose-pull-stack-dev compose-pull-stack-prod compose-pull-stack-relay-cn \
	compose-up-dev compose-up-prod compose-up-relay-cn \
	compose-up-stun-dev compose-up-stun-prod compose-up-stun-relay-cn \
	compose-up-stack-dev compose-up-stack-prod compose-up-stack-relay-cn \
	compose-start-dev compose-start-prod compose-start-relay-cn \
	compose-stop-dev compose-stop-prod compose-stop-relay-cn \
	compose-down-dev compose-down-prod compose-down-relay-cn \
	relay-cn-ops relay-cn-relay-version relay-cn-invite-create relay-cn-invite-list relay-cn-invite-disable relay-cn-user-delete relay-cn-psql relay-cn-logs \
	relay-cn-status \
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

init-relay-cn: ## Bootstrap the relay-cn host for Compose: install nginx/certbot, render HTTP nginx, issue TLS cert, then re-render nginx with TLS. Use on first machine bootstrap.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/relay-cn.yml ANSIBLE_TAGS="$(INIT_PROD_TAGS)"
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/relay-cn.yml ANSIBLE_TAGS=nginx

_deploy-built:
	@$(MAKE) build-linux
	@$(MAKE) _deploy-ansible ANSIBLE_TAGS="$(strip $(ANSIBLE_TAGS))"

_deploy-ansible:
	@$(MAKE) ansible-run

deps-dev: ## Install remote OS packages for the dev host. Use on first machine bootstrap or when package requirements change.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS=deps

deps-prod: ## Install remote OS packages for the prod host. Use on first machine bootstrap or when package requirements change.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS=deps

deps-relay-cn: ## Install remote OS packages for the relay-cn host. Use on first machine bootstrap or when package requirements change.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/relay-cn.yml ANSIBLE_TAGS=deps

postgres-dev: ## Legacy/systemd only: ensure host PostgreSQL user/database/password state. Do not use for Docker Compose deployments.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS=postgres

postgres-prod: ## Legacy/systemd only: ensure host PostgreSQL user/database/password state. Do not use for Docker Compose deployments.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS=postgres

nginx-dev: ## Render `/etc/nginx/...` config, site files, websocket map, and reload nginx. Use after domain/upstream/reverse-proxy changes.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS=nginx

nginx-prod: ## Render `/etc/nginx/...` config, site files, websocket map, and reload nginx. Use after domain/upstream/reverse-proxy changes.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS=nginx

nginx-relay-cn: ## Render `/etc/nginx/...` config, site files, websocket map, and reload nginx on relay-cn. Use after domain/upstream/reverse-proxy changes.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/relay-cn.yml ANSIBLE_TAGS=nginx

certbot-dev: ## Manage `/etc/letsencrypt/...` certificate issuance, renewal hook, and timer. Use for first TLS setup or domain/email/cert renewal changes.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS=certbot

certbot-prod: ## Manage `/etc/letsencrypt/...` certificate issuance, renewal hook, and timer. Use for first TLS setup or domain/email/cert renewal changes.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS=certbot

certbot-relay-cn: ## Manage `/etc/letsencrypt/...` certificate issuance, renewal hook, and timer on relay-cn. Use for first TLS setup or domain/email/cert renewal changes.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/relay-cn.yml ANSIBLE_TAGS=certbot

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

compose-sync-relay-cn: ## Sync Docker Compose relay assets to the relay-cn host without starting services.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/relay-cn.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=sync

compose-pull-dev: ## Pull the configured Relay image on the dev host without touching STUN.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=pull-relay

compose-pull-prod: ## Pull the configured Relay image on the prod host without touching STUN.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=pull-relay

compose-pull-relay-cn: ## Pull the configured Relay image on relay-cn without touching STUN.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/relay-cn.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=pull-relay

compose-pull-stun-dev: ## Pull the configured STUN image on the dev host without touching Relay.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=pull-stun

compose-pull-stun-prod: ## Pull the configured STUN image on the prod host without touching Relay.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=pull-stun

compose-pull-stun-relay-cn: ## Pull the configured STUN image on relay-cn without touching Relay.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/relay-cn.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=pull-stun

compose-pull-stack-dev: ## Pull all configured Compose images on the dev host.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=pull

compose-pull-stack-prod: ## Pull all configured Compose images on the prod host.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=pull

compose-pull-stack-relay-cn: ## Pull all configured Compose images on relay-cn.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/relay-cn.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=pull

compose-up-dev: ## Pull and start only the Relay Compose service on the dev host.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=up-relay

compose-up-prod: ## Pull and start only the Relay Compose service on the prod host.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=up-relay

compose-up-relay-cn: ## Pull and start only the Relay Compose service on relay-cn.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/relay-cn.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=up-relay

compose-up-stun-dev: ## Pull and start only the STUN Compose service on the dev host.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=up-stun

compose-up-stun-prod: ## Pull and start only the STUN Compose service on the prod host.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=up-stun

compose-up-stun-relay-cn: ## Pull and start only the STUN Compose service on relay-cn.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/relay-cn.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=up-stun

compose-up-stack-dev: ## Pull and start the full Docker Compose stack on the dev host.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=up

compose-up-stack-prod: ## Pull and start the full Docker Compose stack on the prod host.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=up

compose-up-stack-relay-cn: ## Pull and start the full Docker Compose stack on relay-cn.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/relay-cn.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=up

compose-start-dev: ## Start existing Docker Compose relay services on the dev host.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=start

compose-start-prod: ## Start existing Docker Compose relay services on the prod host.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=start

compose-start-relay-cn: ## Start existing Docker Compose relay services on the relay-cn host.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/relay-cn.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=start

compose-stop-dev: ## Stop Docker Compose relay services on the dev host without removing containers.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=stop

compose-stop-prod: ## Stop Docker Compose relay services on the prod host without removing containers.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=stop

compose-stop-relay-cn: ## Stop Docker Compose relay services on the relay-cn host without removing containers.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/relay-cn.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=stop

compose-down-dev: ## Stop and remove Docker Compose relay containers on the dev host while keeping named volumes.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=down

compose-down-prod: ## Stop and remove Docker Compose relay containers on the prod host while keeping named volumes.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=down

compose-down-relay-cn: ## Stop and remove Docker Compose relay containers on the relay-cn host while keeping named volumes.
	@$(MAKE) _deploy-ansible ANSIBLE_INVENTORY=ansible/inventories/relay-cn.yml ANSIBLE_TAGS="$(DEPLOY_COMPOSE_TAGS)" RELAY_COMPOSE_ACTION=down

relay-cn-ops: ## Print the common Docker Compose operator commands for relay-cn.
	@printf '%s\n' \
	'relay-cn quick ops (run from your Mac in this repo):' \
	'  make compose-sync-relay-cn                            # sync Compose assets without starting services' \
	'  make compose-up-relay-cn                              # routine Relay-only update' \
	'  make compose-up-stun-relay-cn                         # rare STUN-only update' \
	'  make compose-up-stack-relay-cn                        # first rollout/full-stack update' \
	'  make relay-cn-relay-version                              # running relay version/build' \
	'  make relay-cn-invite-create RELAY_CN_INVITE_COUNT=3 RELAY_CN_INVITE_EXPIRES_IN=7d' \
	'  make relay-cn-invite-list' \
	'  make relay-cn-invite-disable RELAY_CN_INVITE_CODE=AB2C3D' \
	'  make relay-cn-user-delete RELAY_CN_USERNAME=alice        # destructive' \
	'  make relay-cn-psql                                       # open PostgreSQL shell' \
	'  make relay-cn-logs                                       # tail Relay structured logs' \
	'  make relay-cn-status                                     # end-to-end health check incl. public STUN'

relay-cn-relay-version: ## Print the relay version from the running relay-cn container.
	@ssh "$(RELAY_CN_SSH_DEST)" 'cd "$(RELAY_CN_COMPOSE_DIR)" && sudo docker compose --env-file .env exec relay relay version'

relay-cn-invite-create: ## Create invite codes on relay-cn. Override RELAY_CN_INVITE_COUNT and RELAY_CN_INVITE_EXPIRES_IN as needed.
	@ssh "$(RELAY_CN_SSH_DEST)" 'cd "$(RELAY_CN_COMPOSE_DIR)" && sudo docker compose --env-file .env exec relay relay invite create --count "$(RELAY_CN_INVITE_COUNT)" --expires-in "$(RELAY_CN_INVITE_EXPIRES_IN)"'

relay-cn-invite-list: ## List invite codes on relay-cn.
	@ssh "$(RELAY_CN_SSH_DEST)" 'cd "$(RELAY_CN_COMPOSE_DIR)" && sudo docker compose --env-file .env exec relay relay invite list'

relay-cn-invite-disable: ## Disable one invite code on relay-cn. Set RELAY_CN_INVITE_CODE=<code>.
	@if [ -z "$(RELAY_CN_INVITE_CODE)" ]; then \
		printf 'RELAY_CN_INVITE_CODE is required\n' >&2; \
		exit 1; \
	fi
	@ssh "$(RELAY_CN_SSH_DEST)" 'cd "$(RELAY_CN_COMPOSE_DIR)" && sudo docker compose --env-file .env exec relay relay invite disable --code "$(RELAY_CN_INVITE_CODE)"'

relay-cn-user-delete: ## Delete one user on relay-cn. Set RELAY_CN_USERNAME=<username>.
	@if [ -z "$(RELAY_CN_USERNAME)" ]; then \
		printf 'RELAY_CN_USERNAME is required\n' >&2; \
		exit 1; \
	fi
	@ssh "$(RELAY_CN_SSH_DEST)" 'cd "$(RELAY_CN_COMPOSE_DIR)" && sudo docker compose --env-file .env exec relay relay user delete --username "$(RELAY_CN_USERNAME)"'

relay-cn-psql: ## Open a PostgreSQL shell on relay-cn using the Compose postgres service.
	@ssh -t "$(RELAY_CN_SSH_DEST)" 'cd "$(RELAY_CN_COMPOSE_DIR)" && sudo docker compose --env-file .env exec postgres sh -lc '"'"'psql -U "$$POSTGRES_USER" -d "$$POSTGRES_DB"'"'"''

relay-cn-logs: ## Tail the persisted Relay structured log on relay-cn.
	@ssh -t "$(RELAY_CN_SSH_DEST)" 'sudo tail -n 100 -f /opt/agentunnel/logs/relay/relay.log'

relay-cn-status: ## Check relay-cn DNS, website, relay health, API auth paths, websocket auth paths, and Compose service state.
	@./scripts/relay-cn-status.sh

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

deploy-website-relay-cn: ## Build the website locally, upload it to relay-cn, switch `/var/www/agentunnel-website/current`, and reload nginx. Use after website frontend changes.
	@$(MAKE) _deploy-website ANSIBLE_INVENTORY=ansible/inventories/relay-cn.yml
