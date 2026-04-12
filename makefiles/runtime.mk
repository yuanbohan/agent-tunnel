.PHONY: start stop status

start: ## Start the relay binary in the background and write the configured pid file.
	@set -e; \
	mkdir -p "$(LOG_DIR)"; \
	if [ -f "$(RELAY_PID_FILE)" ] && kill -0 "$$(cat "$(RELAY_PID_FILE)")" 2>/dev/null; then \
		echo "relay is already running (pid=$$(cat "$(RELAY_PID_FILE)"))"; \
		exit 1; \
	fi; \
	if [ ! -x "$(RELAY_BIN)" ]; then \
		echo "relay binary not found or not executable: $(RELAY_BIN)"; \
		exit 1; \
	fi; \
	nohup "$(RELAY_BIN)" serve --listen-addr "127.0.0.1:$(RELAY_PORT)" >> "$(RELAY_LOG_FILE)" 2>&1 & \
	echo $$! > "$(RELAY_PID_FILE)"; \
	sleep 1; \
	pid="$$(cat "$(RELAY_PID_FILE)")"; \
	if kill -0 "$$pid" 2>/dev/null; then \
		echo "relay started in background (pid=$$pid, port=$(RELAY_PORT), log=$(RELAY_LOG_FILE))"; \
	else \
		rm -f "$(RELAY_PID_FILE)"; \
		echo "relay failed to start; check $(RELAY_LOG_FILE)"; \
		exit 1; \
	fi

stop: ## Stop the background relay process tracked in the configured pid file.
	@set -e; \
	if [ ! -f "$(RELAY_PID_FILE)" ]; then \
		echo "relay is not running ($(RELAY_PID_FILE) not found)"; \
		exit 1; \
	fi; \
	pid="$$(cat "$(RELAY_PID_FILE)")"; \
	if kill -0 "$$pid" 2>/dev/null; then \
		kill "$$pid"; \
		echo "relay stopped (pid=$$pid)"; \
	else \
		echo "relay process already exited (pid=$$pid)"; \
	fi; \
	rm -f "$(RELAY_PID_FILE)"

status: ## Show relay process status and inspect the configured listen port.
	@set -e; \
	echo "relay status"; \
	if [ -f "$(RELAY_PID_FILE)" ]; then \
		pid="$$(cat "$(RELAY_PID_FILE)")"; \
		if kill -0 "$$pid" 2>/dev/null; then \
			echo "- pid: $$pid (running)"; \
		else \
			echo "- pid: $$pid (stale pid file, process not running)"; \
		fi; \
	else \
		echo "- pid: not found ($(RELAY_PID_FILE))"; \
	fi; \
	echo "- port: $(RELAY_PORT)"; \
	if command -v ss >/dev/null 2>&1; then \
		ss -lntp | awk 'NR==1 || $$4 ~ /:$(RELAY_PORT)$$/'; \
	elif command -v lsof >/dev/null 2>&1; then \
		lsof -nP -iTCP:$(RELAY_PORT) -sTCP:LISTEN; \
	else \
		echo "no ss/lsof available to inspect listening sockets"; \
	fi
