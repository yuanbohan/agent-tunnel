.DEFAULT_GOAL := help

# ENV_FILE selects which env file to load and deploy. Defaults to .env.prod.
# Override per-command, e.g. `make deploy ENV_FILE=.env.dev`.
ENV_FILE ?= .env.prod
-include $(ENV_FILE)

MAKEFILES_DIR := makefiles

include $(MAKEFILES_DIR)/common.mk
include $(MAKEFILES_DIR)/runtime.mk
include $(MAKEFILES_DIR)/build.mk
include $(MAKEFILES_DIR)/release.mk
include $(MAKEFILES_DIR)/install.mk
include $(MAKEFILES_DIR)/deploy.mk
include $(MAKEFILES_DIR)/local-e2e.mk

# Export after included defaults are assigned; exporting earlier would define
# these variables as empty and bypass the `?=` defaults in `common.mk`.
export ENV_FILE BIN_DIR RELAY_DATABASE_URL RELAY_APP_SECRET RELAY_OPERATOR_TOKEN RELAY_LISTEN_ADDR RELAY_LOG_FILE
export GO RELEASE_DIR
export DEPLOY_HOST DEPLOY_RELAY_PATH DEPLOY_MIGRATOR_PATH DEPLOY_SERVICE DEPLOY_INSTALL_PATH DEPLOY_MIGRATOR_INSTALL_PATH DEPLOY_ENV_FILE DEPLOY_RELAY_LOG_FILE DEPLOY_SCHEMA_DIR MIGRATOR_ARGS DEPLOY_VERBOSE DEPLOY_DRY_RUN
