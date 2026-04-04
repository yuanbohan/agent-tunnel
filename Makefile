.PHONY: agentunnel relay build clean vet test

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
