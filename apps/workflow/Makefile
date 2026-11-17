GO ?= go
PKG ?= ./...
BIN := ./bin/workflow-server

.PHONY: all build test lint fmt run clean

all: fmt lint test build

build:
	$(GO) build -o $(BIN) ./cmd/workflow-server

test:
	$(GO) test -race $(PKG)

lint:
	golangci-lint run $(PKG)

fmt:
	$(GO) fmt $(PKG)

run: build
	./$(BIN)

clean:
	rm -rf bin/
