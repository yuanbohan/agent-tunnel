.PHONY: agent client agentunnel build clean vet test web web-install web-build

agent:
	go run ./cmd/agent

client:
	go run ./cmd/client

agentunnel: web-build
	@test -n "$(LAUNCHER)" || (echo "usage: make agentunnel LAUNCHER=claude" && exit 1)
	go run ./cmd/agentunnel $(LAUNCHER)

web-install:
	cd web && npm install

web-build:
	cd web && npm run build

web:
	cd web && npm run dev

build: web-build
	go build -o bin/agent ./cmd/agent
	go build -o bin/client ./cmd/client
	go build -o bin/agentunnel ./cmd/agentunnel

clean:
	rm -rf bin/

vet:
	go vet ./...

test:
	go test ./...
	cd web && npm test
