.PHONY: deploy-install deploy-schema deploy-env deploy-migrate deploy-restart deploy

deploy-install: build-linux ## Build, upload, and install the relay and migrator binaries on the remote host.
	scp $(RELAY_BUILD_BIN) $(DEPLOY_HOST):$(DEPLOY_RELAY_PATH)
	scp $(MIGRATOR_BUILD_BIN) $(DEPLOY_HOST):$(DEPLOY_MIGRATOR_PATH)
	ssh $(DEPLOY_HOST) 'sudo install -m 0755 $(DEPLOY_RELAY_PATH) $(DEPLOY_INSTALL_PATH) && sudo install -m 0755 $(DEPLOY_MIGRATOR_PATH) $(DEPLOY_MIGRATOR_INSTALL_PATH)'

deploy-schema: ## Upload relay schema SQL files to the remote host.
	ssh $(DEPLOY_HOST) 'sudo mkdir -p $(DEPLOY_SCHEMA_DIR)'
	ssh $(DEPLOY_HOST) 'rm -rf /tmp/agentunnel-relay-schema && mkdir -p /tmp/agentunnel-relay-schema'
	scp schema/*.sql $(DEPLOY_HOST):/tmp/agentunnel-relay-schema/
	ssh $(DEPLOY_HOST) 'sudo /bin/sh -lc '"'"'install -m 0644 /tmp/agentunnel-relay-schema/*.sql $(DEPLOY_SCHEMA_DIR)/ && rm -rf /tmp/agentunnel-relay-schema'"'"''

deploy-env: ## Install local `.env` on the remote host as `$(DEPLOY_ENV_FILE)`.
	test -f .env
	scp .env $(DEPLOY_HOST):/tmp/agentunnel-relay.env
	ssh $(DEPLOY_HOST) 'sudo install -d -m 0755 $(dir $(DEPLOY_ENV_FILE))'
	ssh $(DEPLOY_HOST) 'sudo install -m 0600 /tmp/agentunnel-relay.env $(DEPLOY_ENV_FILE) && rm -f /tmp/agentunnel-relay.env'

deploy-migrate: ## Run relay schema migrations on the remote host using the installed migrator.
	ssh $(DEPLOY_HOST) 'sudo /bin/sh -lc '"'"'set -a && . $(DEPLOY_ENV_FILE) && set +a && $(DEPLOY_MIGRATOR_INSTALL_PATH) --schema-dir $(DEPLOY_SCHEMA_DIR) $(MIGRATOR_ARGS)'"'"''

deploy-restart: ## Restart the relay systemd service on the remote host.
	ssh $(DEPLOY_HOST) 'sudo systemctl restart $(DEPLOY_SERVICE)'

deploy: deploy-install deploy-schema deploy-env deploy-restart ## Build, upload, install, sync `.env`, upload schema files, and restart the relay on the remote host.
