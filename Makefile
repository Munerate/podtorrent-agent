.PHONY: build test lint dev

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/ ./cmd/...

test:
	go test -race ./...

lint:
	go vet ./...
	golangci-lint run

# Run mock engine, mock network and the agent together; then try:
#   curl -N localhost:8787/v1/chat/completions -H 'Authorization: Bearer dev-api-key' \
#     -d '{"model":"mock/echo-1","stream":true,"messages":[{"role":"user","content":"hi"}]}'
dev: build
	./bin/mock-engine & ./bin/mock-network & \
	PODTORRENT_NODE_TOKEN=dev-node-token ./bin/podtorrent-agent \
	  -network ws://127.0.0.1:8787/v0/node/connect -manifest examples/manifest.json; \
	kill %1 %2
