BINARY_SERVER=inletd
BINARY_CLIENT=inlet

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY_SERVER) ./cmd/server
	go build -ldflags "$(LDFLAGS)" -o $(BINARY_CLIENT) ./cmd/client

build-all:
	mkdir -p dist
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o dist/$(BINARY_SERVER)-linux-amd64 ./cmd/server
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o dist/$(BINARY_CLIENT)-linux-amd64 ./cmd/client
	GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o dist/$(BINARY_SERVER)-darwin-arm64 ./cmd/server
	GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o dist/$(BINARY_CLIENT)-darwin-arm64 ./cmd/client

run-server:
	go run ./cmd/server

run-client:
	go run ./cmd/client http 3000

run-demo-server:
	go run ./scripts/demo-server

test:
	go test -race ./...
	go vet ./...

clean:
	rm -f $(BINARY_SERVER) $(BINARY_CLIENT) server
	rm -rf dist



dev:
	@scripts/dev.sh start

dev-stop:
	@scripts/dev.sh stop
