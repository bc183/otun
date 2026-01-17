.PHONY: build build-server build-client test lint clean fmt vet

# Binary names
SERVER_BINARY=otun-server
CLIENT_BINARY=otun-client

# Build directories
BUILD_DIR=bin

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOFMT=$(GOCMD) fmt
GOVET=$(GOCMD) vet
GOMOD=$(GOCMD) mod

# Build flags
LDFLAGS=-ldflags "-s -w"

## build: Build both server and client binaries
build: build-server build-client

## build-server: Build the server binary
build-server:
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(SERVER_BINARY) ./cmd/server

## build-client: Build the client binary
build-client:
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(CLIENT_BINARY) ./cmd/client

## test: Run all tests
test:
	$(GOTEST) -v -race -cover ./...

## test-short: Run tests without race detector (faster)
test-short:
	$(GOTEST) -v -cover ./...

## lint: Run linter (requires golangci-lint)
lint:
	@which golangci-lint > /dev/null || (echo "Installing golangci-lint..." && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
	golangci-lint run ./...

## fmt: Format code
fmt:
	$(GOFMT) ./...

## vet: Run go vet
vet:
	$(GOVET) ./...

## clean: Remove build artifacts
clean:
	rm -rf $(BUILD_DIR)
	$(GOCMD) clean

## tidy: Tidy go modules
tidy:
	$(GOMOD) tidy

## check: Run fmt, vet, and test
check: fmt vet test

## help: Show this help message
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@sed -n 's/^##//p' $(MAKEFILE_LIST) | column -t -s ':' | sed 's/^/ /'

# Default target
.DEFAULT_GOAL := help
