.DEFAULT_GOAL := help

-include .env

export RELAY_DATABASE_URL RELAY_APP_SECRET RELAY_OPERATOR_TOKEN RELAY_LISTEN_ADDR

MAKEFILES_DIR := makefiles

include $(MAKEFILES_DIR)/common.mk
include $(MAKEFILES_DIR)/runtime.mk
include $(MAKEFILES_DIR)/build.mk
include $(MAKEFILES_DIR)/deploy.mk
include $(MAKEFILES_DIR)/local-e2e.mk
