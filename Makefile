.PHONY: agentunnel relay start start-relay stop-relay status status-relay build build-linux install clean vet test test-relay

RELAY_BIN ?= ./relay
RELAY_PORT ?= 8586

agentunnel:
	@test -n "$(LAUNCHER)" || (echo "usage: make agentunnel LAUNCHER=claude" && exit 1)
	go run ./cmd/agentunnel $(LAUNCHER)

relay:
	go run ./cmd/relay

start-relay:
	@set -e; \
	mkdir -p logs; \
	if [ -f logs/relay.pid ] && kill -0 "$$(cat logs/relay.pid)" 2>/dev/null; then \
		echo "relay is already running (pid=$$(cat logs/relay.pid))"; \
		exit 1; \
	fi; \
	if [ ! -x "$(RELAY_BIN)" ]; then \
		echo "relay binary not found or not executable: $(RELAY_BIN)"; \
		exit 1; \
	fi; \
	nohup "$(RELAY_BIN)" --port "$(RELAY_PORT)" >> logs/relay.log 2>&1 & \
	echo $$! > logs/relay.pid; \
	sleep 1; \
	pid="$$(cat logs/relay.pid)"; \
	if kill -0 "$$pid" 2>/dev/null; then \
		echo "relay started in background (pid=$$pid, port=$(RELAY_PORT), log=logs/relay.log)"; \
	else \
		rm -f logs/relay.pid; \
		echo "relay failed to start; check logs/relay.log"; \
		exit 1; \
	fi

start: start-relay

stop-relay:
	@set -e; \
	if [ ! -f logs/relay.pid ]; then \
		echo "relay is not running (logs/relay.pid not found)"; \
		exit 1; \
	fi; \
	pid="$$(cat logs/relay.pid)"; \
	if kill -0 "$$pid" 2>/dev/null; then \
		kill "$$pid"; \
		echo "relay stopped (pid=$$pid)"; \
	else \
		echo "relay process already exited (pid=$$pid)"; \
	fi; \
	rm -f logs/relay.pid

status-relay:
	@set -e; \
	echo "relay status"; \
	if [ -f logs/relay.pid ]; then \
		pid="$$(cat logs/relay.pid)"; \
		if kill -0 "$$pid" 2>/dev/null; then \
			echo "- pid: $$pid (running)"; \
		else \
			echo "- pid: $$pid (stale pid file, process not running)"; \
		fi; \
	else \
		echo "- pid: not found (logs/relay.pid)"; \
	fi; \
	echo "- port: $(RELAY_PORT)"; \
	if command -v ss >/dev/null 2>&1; then \
		ss -lntp | awk 'NR==1 || $$4 ~ /:$(RELAY_PORT)$$/'; \
	elif command -v lsof >/dev/null 2>&1; then \
		lsof -nP -iTCP:$(RELAY_PORT) -sTCP:LISTEN; \
	else \
		echo "no ss/lsof available to inspect listening sockets"; \
	fi

status: status-relay

build:
	go build -o bin/tunnel ./cmd/agentunnel
	go build -o bin/relay ./cmd/relay

build-linux:
	GOOS=linux GOARCH=amd64 go build -o bin/relay ./cmd/relay
	GOOS=linux GOARCH=amd64 go build -o bin/tunnel ./cmd/agentunnel

install: build
	@set -e; \
	mkdir -p "$(HOME)/.local/bin"; \
	cp -f bin/* "$(HOME)/.local/bin/"; \
	echo "installed binaries to $(HOME)/.local/bin"

clean:
	rm -rf bin/

vet:
	go vet ./...

test:
	go test ./...

test-relay:
	go test ./protocol ./relay
