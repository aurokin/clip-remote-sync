GO ?= go
GOLANGCI_LINT ?= $(if $(shell $(GO) env GOBIN),$(shell $(GO) env GOBIN),$(shell $(GO) env GOPATH)/bin)/golangci-lint

.PHONY: build build-windows checksums verify-release-tree fmt lint lint-fix test race vet check install tools

build:
	$(GO) build -o bin/crs ./cmd/crs

build-windows:
	GOOS=windows GOARCH=amd64 $(GO) build -o bin/crs-windows-amd64.exe ./cmd/crs

checksums: build build-windows
	cd bin && sha256sum crs crs-windows-amd64.exe > SHA256SUMS

verify-release-tree:
	./scripts/check-release-tree.sh

fmt:
	$(GO) fmt ./...

lint:
	$(GOLANGCI_LINT) run

lint-fix:
	$(GOLANGCI_LINT) run --fix

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

check: verify-release-tree fmt vet test race lint build build-windows

install:
	$(GO) install ./cmd/crs

tools:
	$(GO) install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8
