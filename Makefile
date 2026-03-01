VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
DATE    ?= $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')

LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(DATE)"

.PHONY: build build-wasm build-all clean test lint

## Build yantra binary
build:
	go build $(LDFLAGS) -o yantra ./cmd/yantra/

## Build WASM guest for file tool sandbox (requires wasm/guest/)
build-wasm:
	@if [ ! -d wasm/guest ]; then echo "wasm/guest not yet created, skipping"; exit 0; fi
	@mkdir -p internal/sandbox
	cd wasm/guest && GOOS=wasip1 GOARCH=wasm go build -o ../../internal/sandbox/guest.wasm .

## Cross-compile for all platforms
build-all:
	GOOS=linux  GOARCH=amd64 go build $(LDFLAGS) -o dist/yantra-linux-amd64  ./cmd/yantra/
	GOOS=linux  GOARCH=arm64 go build $(LDFLAGS) -o dist/yantra-linux-arm64  ./cmd/yantra/
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o dist/yantra-darwin-arm64 ./cmd/yantra/

## Run all tests
test:
	go test ./... -race -count=1

## Run linter
lint:
	golangci-lint run ./...

## Clean build artifacts
clean:
	rm -f yantra
	rm -rf dist/
	rm -f internal/sandbox/guest.wasm

## Install locally
install: build
	cp yantra $(GOPATH)/bin/yantra 2>/dev/null || cp yantra $(HOME)/go/bin/yantra
