VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION)"
BIN     := qmd

.PHONY: build test lint fmt release clean

build:
	go build $(LDFLAGS) -o $(BIN) ./cmd/qmd/

test:
	go test -race -count=1 ./...

lint:
	golangci-lint run ./...

fmt:
	gofumpt -w .
	goimports -w .

release: clean
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BIN)-darwin-arm64 ./cmd/qmd/
	GOOS=linux  GOARCH=amd64 go build $(LDFLAGS) -o dist/$(BIN)-linux-amd64  ./cmd/qmd/
	GOOS=linux  GOARCH=arm64 go build $(LDFLAGS) -o dist/$(BIN)-linux-arm64  ./cmd/qmd/

clean:
	rm -f $(BIN)
	rm -rf dist/
