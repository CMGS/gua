.PHONY: all build test lint vet fmt deps clean help

REPO_PATH := github.com/CMGS/gua
LOCALBIN ?= $(shell pwd)/bin

GOLANGCILINT_VERSION ?= v2.9.0
GOLANGCILINT_ROOT := $(LOCALBIN)/golangci-lint-$(GOLANGCILINT_VERSION)
GOLANGCILINT := $(GOLANGCILINT_ROOT)/golangci-lint
GOFMT := $(LOCALBIN)/gofumpt
GOIMPORTS := $(LOCALBIN)/goimports

$(LOCALBIN):
	mkdir -p $(LOCALBIN)

$(GOLANGCILINT):
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(GOLANGCILINT_ROOT) $(GOLANGCILINT_VERSION)

$(GOFMT): | $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install mvdan.cc/gofumpt@latest

$(GOIMPORTS): | $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install golang.org/x/tools/cmd/goimports@latest

# --- Primary targets ---

all: deps fmt lint build test ## Full pipeline

build: ## Build server and bridge binaries
	go build -o bin/gua-server ./cmd/
	go build -o bin/gua-bridge ./agent/claude/bridge/

# --- Dependencies ---

deps: ## Tidy Go modules
	go mod tidy

# --- Testing ---

test: vet ## Run tests
	go test -race -timeout 120s -count=1 -cover ./...

# --- Code quality ---

vet: ## Run go vet
	go vet ./...

lint: $(GOLANGCILINT) ## Run golangci-lint
	$(GOLANGCILINT) run

fmt: $(GOFMT) $(GOIMPORTS) ## Format code
	$(GOFMT) -l -w .
	$(GOIMPORTS) -l -w --local '$(REPO_PATH)' .

# --- Maintenance ---

clean: ## Remove build artifacts and test cache
	rm -rf bin/ dist/ coverage.out
	go clean -testcache

# --- Help ---

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'
