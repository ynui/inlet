BINARY_SERVER=inletd
BINARY_CLIENT=inlet
BINARY_ADMIN=inlet-admin

VERSION ?= $(shell git describe --tags --abbrev=0 2>/dev/null)
COMMIT ?= $(shell git rev-parse --short=7 HEAD 2>/dev/null || echo unknown)
DEFAULT_SERVER_URL ?= wss://inletd.onrender.com
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.defaultServerURL=$(DEFAULT_SERVER_URL)

ifeq ($(strip $(VERSION)),)
$(error no version tag found; create a release tag such as v0.0.1)
endif

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY_SERVER) ./cmd/server
	go build -ldflags "$(LDFLAGS)" -o $(BINARY_CLIENT) ./cmd/client
	go build -ldflags "$(LDFLAGS)" -o $(BINARY_ADMIN) ./cmd/admin

build-all:
	mkdir -p dist
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w $(LDFLAGS)" -o dist/$(BINARY_SERVER)-linux-amd64 ./cmd/server
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w $(LDFLAGS)" -o dist/$(BINARY_CLIENT)-linux-amd64 ./cmd/client
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w $(LDFLAGS)" -o dist/$(BINARY_ADMIN)-linux-amd64 ./cmd/admin
	GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "-s -w $(LDFLAGS)" -o dist/$(BINARY_SERVER)-darwin-arm64 ./cmd/server
	GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "-s -w $(LDFLAGS)" -o dist/$(BINARY_CLIENT)-darwin-arm64 ./cmd/client
	GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "-s -w $(LDFLAGS)" -o dist/$(BINARY_ADMIN)-darwin-arm64 ./cmd/admin

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
	rm -f $(BINARY_SERVER) $(BINARY_CLIENT) $(BINARY_ADMIN) server
	rm -rf dist



dev:
	@scripts/dev.sh start

dev-stop:
	@scripts/dev.sh stop
