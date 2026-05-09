.PHONY: all build run test lint fmt validate clean deps

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOFMT=gofmt
BINARY_NAME=e2e-dashboard
BINARY_PATH=./bin/$(BINARY_NAME)

# Build flags
CGO_ENABLED=1
LDFLAGS=-ldflags "-s -w"

all: validate build

# Download dependencies
deps:
	$(GOMOD) download
	$(GOMOD) tidy

# Format code
fmt:
	$(GOFMT) -s -w .

# Lint code
lint:
	golangci-lint run --timeout 5m

# Run tests
test:
	$(GOTEST) -v -race -cover ./...

# Validate (fmt + lint + test)
validate: fmt lint test

# Build the binary
build:
	CGO_ENABLED=$(CGO_ENABLED) $(GOBUILD) $(LDFLAGS) -o $(BINARY_PATH) ./cmd/server

# Build for Linux (for Docker)
build-linux:
	CGO_ENABLED=$(CGO_ENABLED) GOOS=linux GOARCH=amd64 $(GOBUILD) $(LDFLAGS) -o $(BINARY_PATH) ./cmd/server

# Run the application
run:
	$(GOCMD) run ./cmd/server

# Clean build artifacts
clean:
	rm -rf ./bin
	$(GOCMD) clean

# Docker build
docker-build:
	docker build -t e2e-dashboard:latest .

# Docker run
docker-run:
	docker run -p 8080:8080 e2e-dashboard:latest
