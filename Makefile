.PHONY: agent client build clean vet test web web-install

agent:
	go run ./cmd/agent

client:
	go run ./cmd/client

web-install:
	cd web && npm install

web:
	cd web && npm run dev

build:
	go build -o bin/agent  ./cmd/agent
	go build -o bin/client ./cmd/client

clean:
	rm -rf bin/

vet:
	go vet ./...

test:
	go test ./...
	cd web && npm test
