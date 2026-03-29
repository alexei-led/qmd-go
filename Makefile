VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"
BIN     := qmd
GO      := go

.PHONY: build test lint fmt mock release clean help

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "%-20s %s\n", $$1, $$2}'

build: ## Build binary with version embedding
	$(GO) build $(LDFLAGS) -o $(BIN) ./cmd/qmd/

test: ## Run tests with race detector
	$(GO) test -race -count=1 ./...

test-coverage: ## Run tests with coverage report
	$(GO) test -race -count=1 -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out
	@echo "HTML report: go tool cover -html=coverage.out"

lint: ## Run golangci-lint
	golangci-lint run ./...

fmt: ## Format code (gofumpt + goimports)
	gofumpt -w .
	goimports -w .

mock: ## Regenerate provider mocks (mockery)
	$(GO) generate ./internal/provider/...

release: clean ## Cross-compile for all platforms
	GOOS=darwin  GOARCH=arm64 $(GO) build $(LDFLAGS) -o dist/$(BIN)-darwin-arm64  ./cmd/qmd/
	GOOS=darwin  GOARCH=amd64 $(GO) build $(LDFLAGS) -o dist/$(BIN)-darwin-amd64  ./cmd/qmd/
	GOOS=linux   GOARCH=amd64 $(GO) build $(LDFLAGS) -o dist/$(BIN)-linux-amd64   ./cmd/qmd/
	GOOS=linux   GOARCH=arm64 $(GO) build $(LDFLAGS) -o dist/$(BIN)-linux-arm64   ./cmd/qmd/

clean: ## Remove build artifacts
	rm -f $(BIN)
	rm -rf dist/
	rm -f coverage.out

install: ## Install binary to GOPATH/bin
	$(GO) install $(LDFLAGS) ./cmd/qmd/

.DEFAULT_GOAL := help
