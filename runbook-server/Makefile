.PHONY: run test lint build install validate integration-test-up integration-test-down integration-test

fmt:
	go fmt ./...

run:
	go run cmd/main.go

test:
	go test ./...

lint:
	golangci-lint run ./...

# Regenerate OpenAPI spec + docs.go from godoc-style annotations on handlers.
# Requires: go install github.com/swaggo/swag/cmd/swag@latest
swag:
	swag init -g cmd/main.go -o docs/swagger --parseDependency --parseInternal

build:
	go build -o dist/runbook-server ./cmd

install: build
	go install ./cmd/main.go

validate: lint test


start-local-env:
	@echo "Starting Docker Compose services (PostgreSQL, Temporal, Temporal UI, Temporal Setup Job)..."
	docker compose -f docker-compose.test.yml up -d postgresql temporal temporal-ui temporal-setup-job

stop-local-env:
	@echo "Stopping Docker Compose services..."
	docker compose -f docker-compose.test.yml down --remove-orphans -v
