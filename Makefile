.PHONY: all test lint vet fmt fmt-check deps clean coverage cloc help

REPO_PATH := github.com/CMGS/gua

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool versions
GOLANGCILINT_VERSION ?= v2.9.0
GOLANGCILINT_ROOT := $(LOCALBIN)/golangci-lint-$(GOLANGCILINT_VERSION)
GOLANGCILINT := $(GOLANGCILINT_ROOT)/golangci-lint

GOFMT := $(LOCALBIN)/gofumpt
GOIMPORTS := $(LOCALBIN)/goimports

## Tool download targets
.PHONY: golangci-lint
golangci-lint: $(GOLANGCILINT)
$(GOLANGCILINT):
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(GOLANGCILINT_ROOT) $(GOLANGCILINT_VERSION)

.PHONY: gofumpt
gofumpt: $(GOFMT)
$(GOFMT): | $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install mvdan.cc/gofumpt@latest

.PHONY: goimports
goimports: $(GOIMPORTS)
$(GOIMPORTS): | $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install golang.org/x/tools/cmd/goimports@latest

# --- Primary targets ---

all: deps fmt lint test ## Full pipeline: deps, fmt, lint, test

# --- Dependencies ---

deps: ## Tidy Go modules
	go mod tidy

# --- Testing ---

test: vet ## Run tests with race detection and coverage
	go test -race -timeout 120s -count=1 -cover -coverprofile=coverage.out ./...

coverage: test ## Generate and display coverage report
	go tool cover -func=coverage.out
	@echo ""
	@echo "To view HTML coverage report: go tool cover -html=coverage.out"

# --- Code quality ---

vet: ## Run go vet
	go vet ./...

lint: golangci-lint ## Run golangci-lint
	$(GOLANGCILINT) run

fmt: gofumpt goimports ## Format code with gofumpt and goimports
	$(GOFMT) -l -w .
	$(GOIMPORTS) -l -w --local '$(REPO_PATH)' .

fmt-check: gofumpt goimports ## Check formatting (fails if files need formatting)
	@test -z "$$($(GOFMT) -l .)" || { echo "Files need formatting (gofumpt):"; $(GOFMT) -l .; exit 1; }
	@test -z "$$($(GOIMPORTS) -l .)" || { echo "Files need formatting (goimports):"; $(GOIMPORTS) -l .; exit 1; }

# --- Maintenance ---

clean: ## Remove build artifacts, coverage files, and test cache
	rm -rf bin/ dist/
	rm -f coverage.out coverage.html coverage.txt
	go clean -testcache

cloc: ## Count lines of code excluding tests (requires cloc)
	cloc --exclude-dir=vendor,dist,bot --exclude-ext=json --not-match-f='_test\.go$$' .

# --- Help ---

help: ## Show this help message
	@echo "Gua Makefile targets:"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'
	@echo ""
