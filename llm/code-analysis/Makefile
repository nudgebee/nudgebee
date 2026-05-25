# Variables
BINARY_NAME=code-analysis-agent
VERSION=1.0.0
BUILD_DIR=build
DOCKER_IMAGE=nudgebee/code-analysis-cli
DOCKER_TAG=latest

# Go variables
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOCLEAN=$(GOCMD) clean
GOMOD=$(GOCMD) mod

# Build flags
LDFLAGS=-ldflags "-X main.version=$(VERSION) -w -s"

.PHONY: help build test clean run docker-build docker-run deps lint fmt vet

help: ## Show this help message
	@echo "Available targets:"
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

build: ## Build the application
	@echo "Building $(BINARY_NAME)..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) cmd/main.go

build-tools: ## Build additional tools
	@echo "Building log parser..."
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/log-parser cmd/log_parser.go

build-linux: ## Build for Linux
	@echo "Building $(BINARY_NAME) for Linux..."
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME)-linux cmd/main.go

run: ## Run the application
	@echo "Running $(BINARY_NAME)..."
	$(GOCMD) run cmd/main.go

test: ## Run tests
	@echo "Running tests..."
	$(GOTEST) -v -race -coverprofile=coverage.out ./...

test-coverage: test ## Run tests with coverage report
	@echo "Generating coverage report..."
	$(GOCMD) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"

deps: ## Download dependencies
	@echo "Downloading dependencies..."
	$(GOMOD) download
	$(GOMOD) tidy

##@ Code Quality

lint: ## Run linter
	@echo "Running linter..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not installed. Install with: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
	fi

fmt: ## Format code
	@echo "Formatting code..."
	$(GOCMD) fmt ./...

vet: ## Run go vet
	@echo "Running go vet..."
	$(GOCMD) vet ./...

##@ Docker

docker-build: ## Build Docker image
	@echo "Building Docker image..."
	docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) -t $(DOCKER_IMAGE):$(VERSION) .

docker-run: ## Run Docker container in CLI mode
	@echo "Running Docker CLI container..."
	docker run --rm -it \
		-e GITHUB_TOKEN="$(GITHUB_TOKEN)" \
		-e LLM_PROVIDER="$(LLM_PROVIDER)" \
		-e LLM_MODEL_NAME="$(LLM_MODEL_NAME)" \
		-e LLM_PROVIDER_REGION="$(LLM_PROVIDER_REGION)" \
		-e LLM_PROVIDER_API_KEY="$(LLM_PROVIDER_API_KEY)" \
		-v "$(PWD):/workspace" \
		$(DOCKER_IMAGE):$(DOCKER_TAG)

docker-push: ## Push Docker image
	@echo "Pushing Docker image..."
	docker push $(DOCKER_IMAGE):$(DOCKER_TAG)
	docker push $(DOCKER_IMAGE):$(VERSION)

docker-compose-up: ## Start services with docker-compose
	@echo "Starting services with docker-compose..."
	docker-compose up -d

docker-compose-down: ## Stop services with docker-compose
	@echo "Stopping services with docker-compose..."
	docker-compose down

docker-compose-logs: ## View docker-compose logs
	@echo "Viewing docker-compose logs..."
	docker-compose logs -f

docker-shell: ## Get shell access to CLI container
	@echo "Starting interactive shell in CLI container..."
	docker run --rm -it \
		-e GITHUB_TOKEN="$(GITHUB_TOKEN)" \
		-e LLM_PROVIDER="$(LLM_PROVIDER)" \
		-e LLM_MODEL_NAME="$(LLM_MODEL_NAME)" \
		-e LLM_PROVIDER_REGION="$(LLM_PROVIDER_REGION)" \
		-e LLM_PROVIDER_API_KEY="$(LLM_PROVIDER_API_KEY)" \
		-v "$(PWD):/workspace" \
		--entrypoint /bin/bash \
		$(DOCKER_IMAGE):$(DOCKER_TAG)

docker-analyze: ## Run analysis with Docker (requires REPO, LOGS, TOKEN)
	@echo "Running Docker CLI analysis..."
	@if [ -z "$(REPO)" ] || [ -z "$(LOGS)" ] || [ -z "$(TOKEN)" ]; then \
		echo "Usage: make docker-analyze REPO=<repo-url> LOGS=<logs> TOKEN=<token>"; \
		echo "Example: make docker-analyze REPO=https://github.com/user/repo.git LOGS='ERROR: NPE at line 42' TOKEN=ghp_xxx"; \
		exit 1; \
	fi
	docker run --rm -it \
		-e GITHUB_TOKEN="$(TOKEN)" \
		-e LLM_PROVIDER="$(LLM_PROVIDER)" \
		-e LLM_MODEL_NAME="$(LLM_MODEL_NAME)" \
		-e LLM_PROVIDER_REGION="$(LLM_PROVIDER_REGION)" \
		-e LLM_PROVIDER_API_KEY="$(LLM_PROVIDER_API_KEY)" \
		-v "$(PWD):/workspace" \
		$(DOCKER_IMAGE):$(DOCKER_TAG) \
		code-analysis-cli --analyze --repo "$(REPO)" --logs "$(LOGS)" --token "$(TOKEN)"

##@ Cleanup

clean: ## Clean build artifacts
	@echo "Cleaning..."
	$(GOCLEAN)
	rm -rf $(BUILD_DIR)
	rm -f coverage.out coverage.html

##@ Development Tools

dev-setup: ## Setup development environment
	@echo "Setting up development environment..."
	$(GOMOD) download
	@if ! command -v golangci-lint >/dev/null 2>&1; then \
		echo "Installing golangci-lint..."; \
		go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest; \
	fi

check: fmt vet lint test ## Run all checks

##@ Examples

example-cli: ## Run CLI analysis example
	@echo "Running CLI analysis example..."
	@if [ -z "$(GITHUB_TOKEN)" ]; then \
		echo "Error: GITHUB_TOKEN environment variable is required"; \
		echo "Example: GITHUB_TOKEN=ghp_xxx make example-cli"; \
		exit 1; \
	fi
	./$(BUILD_DIR)/$(BINARY_NAME) --analyze \
		--repo "https://github.com/nudgebee/nudgebee.git" \
		--logs '{"asctime": "2025-07-08 08:04:00,563", "levelname": "ERROR", "filename": "message.py", "lineno": 136, "message": "TypeError: argument of type NoneType is not iterable"}' \
		--token "$(GITHUB_TOKEN)" \
		--prompt "Analyze the logs for errors"

run-cli: build ## Run custom CLI analysis (requires REPO, LOGS, TOKEN)
	@echo "Running custom CLI analysis..."
	@if [ -z "$(REPO)" ] || [ -z "$(LOGS)" ] || [ -z "$(TOKEN)" ]; then \
		echo "Usage: make run-cli REPO=<repo-url> LOGS=<logs> TOKEN=<token>"; \
		echo "Example: make run-cli REPO=https://github.com/user/repo.git LOGS='ERROR: NPE at line 42' TOKEN=ghp_xxx"; \
		exit 1; \
	fi
	./$(BUILD_DIR)/$(BINARY_NAME) --analyze \
		--repo "$(REPO)" \
		--logs "$(LOGS)" \
		--token "$(TOKEN)"

example-token: ## Run example with token authentication via API
	@echo "Running example with token authentication..."
	curl -X POST http://localhost:8081/analyze \
		-H "Content-Type: application/json" \
		-d '{ \
			"cloud_account_id": "acc-123", \
			"tenant": "tenant-456", \
			"workload_name": "example-app", \
			"workload_namespace": "production", \
			"workload_kind": "Deployment", \
			"logs": "ERROR: Database connection failed at line 42", \
			"git_repository": { \
				"url": "https://github.com/user/repo.git", \
				"branch": "main" \
			}, \
			"git_credentials": { \
				"type": "token", \
				"token": "your-github-token" \
			} \
		}'

health-check: ## Check service health
	@echo "Checking service health..."
	curl -s http://localhost:8081/health | jq .

service-info: ## Get service info
	@echo "Getting service info..."
	curl -s http://localhost:8081/info | jq .

##@ Default target
.DEFAULT_GOAL := help