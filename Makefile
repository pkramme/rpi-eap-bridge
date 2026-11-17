VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
REPO ?=
GO ?= go

.PHONY: all build test vet demo release pi clean

all: vet test build

## build: build for this computer (bin/eap-bridge)
build:
	$(GO) build -ldflags "-X main.version=$(VERSION)" -o bin/eap-bridge ./cmd/eap-bridge

## test: run the unit tests
test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

## demo: try the web interface with a simulated network on http://127.0.0.1:8080
demo:
	$(GO) run ./cmd/eap-bridge serve --demo

## pi: build only the 64-bit Raspberry Pi program (bin/eap-bridge-linux-arm64)
pi:
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GO) build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/eap-bridge-linux-arm64 ./cmd/eap-bridge

## release: build all release files into dist/ (REPO=owner/name fills in the installer)
release:
	VERSION=$(VERSION) REPO=$(REPO) bash scripts/package-release.sh

clean:
	rm -rf bin dist build
