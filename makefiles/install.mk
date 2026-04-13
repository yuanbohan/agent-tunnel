.PHONY: install install-local install-dev install-prod install-remote

INSTALL_SCRIPT := ./scripts/install.sh
INSTALL_SCRIPT_FLAGS := $(if $(filter 1 true TRUE yes YES on ON,$(INSTALL_VERBOSE)),--verbose,) $(if $(filter 1 true TRUE yes YES on ON,$(INSTALL_DRY_RUN)),--dry-run,)

install: install-local ## Install `tunnel`, `relay`, and `relay-migrate` into `$(INSTALL_DIR)`.

install-local: build ## Install `tunnel`, `relay`, and `relay-migrate` into `$(INSTALL_DIR)`.
	@set -e; \
	mkdir -p "$(INSTALL_DIR)"; \
	rm -f "$(INSTALL_TUNNEL_BIN)" "$(INSTALL_RELAY_BIN)" "$(INSTALL_MIGRATOR_BIN)"; \
	cp -f "$(TUNNEL_BIN)" "$(RELAY_BUILD_BIN)" "$(MIGRATOR_BUILD_BIN)" "$(INSTALL_DIR)/"; \
	echo "installed tunnel, relay, and relay-migrate to $(INSTALL_DIR)"

install-dev: ## Bootstrap the dev VPS: install nginx/postgresql if missing, serve the website at `/`, proxy relay routes, and restart nginx.
	@$(MAKE) install-remote INSTALL_ENV=dev ENV_FILE=.env.dev

install-prod: ## Bootstrap prod: dev bootstrap plus certbot issuance/renewal wiring and the HTTPS nginx site that serves the website at `/`. Requires a certbot email and prompts if omitted.
	@$(MAKE) install-remote INSTALL_ENV=prod ENV_FILE=.env.prod

install-remote:
	@ENV_FILE="$(ENV_FILE)" \
	DEPLOY_HOST="$(DEPLOY_HOST)" \
	INSTALL_HOST="$(INSTALL_HOST)" \
	INSTALL_NGINX_SITE_NAME="$(INSTALL_NGINX_SITE_NAME)" \
	INSTALL_NGINX_UPSTREAM_ADDR="$(INSTALL_NGINX_UPSTREAM_ADDR)" \
	INSTALL_DEV_SERVER_NAMES="$(INSTALL_DEV_SERVER_NAMES)" \
	INSTALL_PROD_SERVER_NAMES="$(INSTALL_PROD_SERVER_NAMES)" \
	INSTALL_PROD_PRIMARY_DOMAIN="$(INSTALL_PROD_PRIMARY_DOMAIN)" \
	INSTALL_CERTBOT_EMAIL="$(INSTALL_CERTBOT_EMAIL)" \
	INSTALL_CERTBOT_WEBROOT="$(INSTALL_CERTBOT_WEBROOT)" \
	INSTALL_WEBSITE_ROOT="$(INSTALL_WEBSITE_ROOT)" \
	INSTALL_VERBOSE="$(INSTALL_VERBOSE)" \
	INSTALL_DRY_RUN="$(INSTALL_DRY_RUN)" \
	$(INSTALL_SCRIPT) "$(INSTALL_ENV)" $(INSTALL_SCRIPT_FLAGS)
