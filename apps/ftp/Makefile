GO ?= go
PKG ?= ./...
BIN := ./bin/ftp-server

.PHONY: all build test lint fmt run clean

all: fmt lint test build

build:
	$(GO) build -o $(BIN) ./cmd/ftp-server

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
