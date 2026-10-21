.PHONY: build test lint vet vuln check tools run clean

BIN     := piplon
PKG     := ./...
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
  -X github.com/piplon-sh/piplon/internal/version.Version=$(VERSION) \
  -X github.com/piplon-sh/piplon/internal/version.Commit=$(COMMIT) \
  -X github.com/piplon-sh/piplon/internal/version.Date=$(DATE)

build:
	CGO_ENABLED=0 go build -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/piplon

test:
	go test -race -count=1 $(PKG)

vet:
	go vet $(PKG)

lint:
	golangci-lint run

vuln:
	govulncheck $(PKG)

check: build vet lint test vuln
	@echo "all checks passed"

tools:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	go install golang.org/x/vuln/cmd/govulncheck@latest

run: build
	./$(BIN) run

clean:
	rm -f $(BIN) coverage.out
	rm -rf dist
