BINARY_NAME=gitgate
BUILD_DIR=bin
MAIN_PATH=./cmd/gitgate
VERSION?=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS=-ldflags "-X main.Version=$(VERSION) -s -w"
GOFLAGS=-trimpath

.PHONY: all build test lint fmt clean install doctor help

all: fmt lint build test ## Run fmt, lint, build, and test

build: ## Build the gitgate binary
	@mkdir -p $(BUILD_DIR)
	go build $(GOFLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)$(shell go env GOEXE) $(MAIN_PATH)
	@echo "✅ Built: $(BUILD_DIR)/$(BINARY_NAME)"

build-all: ## Build for all supported platforms
	@mkdir -p $(BUILD_DIR)
	GOOS=linux   GOARCH=amd64 go build $(GOFLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64   $(MAIN_PATH)
	GOOS=linux   GOARCH=arm64 go build $(GOFLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64   $(MAIN_PATH)
	GOOS=darwin  GOARCH=amd64 go build $(GOFLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64  $(MAIN_PATH)
	GOOS=darwin  GOARCH=arm64 go build $(GOFLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64  $(MAIN_PATH)
	GOOS=windows GOARCH=amd64 go build $(GOFLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe $(MAIN_PATH)
	@echo "✅ Built all platform binaries in $(BUILD_DIR)/"

test: ## Run all tests
	go test -v -race -timeout 120s ./...

test-short: ## Run tests without integration tests
	go test -short -timeout 30s ./...

lint: ## Run linter
	@if command -v golangci-lint > /dev/null; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not found, running go vet instead"; \
		go vet ./...; \
	fi

fmt: ## Format source code
	gofmt -w -s .
	@if command -v goimports > /dev/null; then \
		goimports -w .; \
	fi

clean: ## Remove build artifacts
	rm -rf $(BUILD_DIR)
	go clean -testcache

install: build ## Install gitgate to GOPATH/bin
	go install $(GOFLAGS) $(LDFLAGS) $(MAIN_PATH)
	@echo "✅ Installed to $(shell go env GOPATH)/bin/$(BINARY_NAME)"

deps: ## Download and tidy dependencies
	go mod download
	go mod tidy

generate: ## Run go generate
	go generate ./...

doctor: build ## Run gitgate doctor to check system requirements
	./$(BUILD_DIR)/$(BINARY_NAME) doctor

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'
