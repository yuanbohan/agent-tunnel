.PHONY: agentunnel relay build clean vet test test-real-hitl

agentunnel:
	@test -n "$(LAUNCHER)" || (echo "usage: make agentunnel LAUNCHER=claude" && exit 1)
	go run ./cmd/agentunnel $(LAUNCHER)

relay:
	go run ./cmd/relay

build:
	go build -o bin/agentunnel ./cmd/agentunnel
	go build -o bin/relay ./cmd/relay

clean:
	rm -rf bin/

vet:
	go vet ./...

test:
	go test ./...

test-real-hitl:
	go build -o bin/relay ./cmd/relay
	go build -o bin/agentunnel ./cmd/agentunnel
	AGENTUNNEL_REAL_HITL_RELAY_BINARY=./bin/relay AGENTUNNEL_REAL_HITL_BINARY=./bin/agentunnel node ./scripts/real_hitl_smoke.mjs
