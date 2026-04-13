.PHONY: ansible-run install install-local install-dev install-prod install-remote

install: install-local ## Install `tunnel`, `relay`, and `relay-migrate` into `$(INSTALL_DIR)`.

install-local: build ## Install `tunnel`, `relay`, and `relay-migrate` into `$(INSTALL_DIR)`.
	@set -e; \
	mkdir -p "$(INSTALL_DIR)"; \
	rm -f "$(INSTALL_TUNNEL_BIN)" "$(INSTALL_RELAY_BIN)" "$(INSTALL_MIGRATOR_BIN)"; \
	cp -f "$(TUNNEL_BIN)" "$(RELAY_BUILD_BIN)" "$(MIGRATOR_BUILD_BIN)" "$(INSTALL_DIR)/"; \
	echo "installed tunnel, relay, and relay-migrate to $(INSTALL_DIR)"

ANSIBLE_OPTS := -i "$(ANSIBLE_INVENTORY)"
ANSIBLE_OPTS += -e "project_root=$(ANSIBLE_PROJECT_ROOT)"
ANSIBLE_OPTS += -e "website_repo_dir=$(ANSIBLE_WEBSITE_REPO_DIR)"
ANSIBLE_OPTS += -e "website_build_dir=$(WEBSITE_BUILD_DIR)"

ANSIBLE_OPTS += $(if $(strip $(ANSIBLE_LIMIT)),--limit "$(ANSIBLE_LIMIT)",)
ANSIBLE_OPTS += $(if $(strip $(ANSIBLE_EXTRA_VARS_FILE)), -e "@$(ANSIBLE_EXTRA_VARS_FILE)",)
ANSIBLE_OPTS += $(if $(strip $(ANSIBLE_TAGS)), --tags "$(ANSIBLE_TAGS)",)
ANSIBLE_OPTS += $(if $(filter 1 true TRUE yes YES on ON,$(ANSIBLE_DRY_RUN)),--check,)

ansible-run:
	@echo "ANSIBLE inventory=$(strip $(ANSIBLE_INVENTORY)) tags=$(strip $(ANSIBLE_TAGS))"
	@ANSIBLE_CONFIG="$(ANSIBLE_CONFIG)" \
	ANSIBLE_ROLES_PATH="$(ANSIBLE_ROLES_PATH)" \
	ANSIBLE_STDOUT_CALLBACK="$(ANSIBLE_STDOUT_CALLBACK)" \
	ANSIBLE_CALLBACK_RESULT_FORMAT="$(ANSIBLE_CALLBACK_RESULT_FORMAT)" \
	$(ANSIBLE) $(ANSIBLE_OPTS) $(ANSIBLE_PLAYBOOK)

install-dev: ## Bootstrap the dev host (dependencies + nginx). Use on first machine bootstrap before relay or website deploys.
	@$(MAKE) install-remote ANSIBLE_INVENTORY=ansible/inventories/dev.yml ANSIBLE_TAGS="deps,nginx"

install-prod: ## Bootstrap the prod host (dependencies + certbot/tls + nginx). Use on first machine bootstrap before relay or website deploys.
	@$(MAKE) install-remote ANSIBLE_INVENTORY=ansible/inventories/prod.yml ANSIBLE_TAGS="deps,certbot,nginx"

install-remote: ansible-run
