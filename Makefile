.PHONY: agent client build clean vet test

agent:
	go run ./cmd/agent

client:
	go run ./cmd/client

build:
	go build -o bin/agent  ./cmd/agent
	go build -o bin/client ./cmd/client

clean:
	rm -rf bin/

vet:
	go vet ./...

test:
	go test ./...
