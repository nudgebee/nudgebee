run:
	go run ./cmd


fmt:
	go fmt ./...

test:
	go mod download
	mkdir testresults || true
	go test -coverprofile=testresults/testcoverage.txt  -race ./...
	go tool cover -html=testresults/testcoverage.txt -o testresults/testcoverage.html

# Run the full integration / e2e suite. Requires port-forwards and TEST_* env
# vars; see agents/TESTING.md for details. Files behind `//go:build e2e` only
# compile in this mode.
test-e2e:
	go mod download
	mkdir testresults || true
	go test -tags=e2e -race ./...

# Run a single e2e test (or a regex of tests). Example:
#   make test-e2e-one TEST=TestGCPCommandTool
#   make test-e2e-one TEST=TestEventAnalyzer_   PKG=./api/
# TEST is required, PKG defaults to ./... (whole module).
test-e2e-one:
	@if [ -z "$(TEST)" ]; then echo "usage: make test-e2e-one TEST=<name> [PKG=./path/]"; exit 1; fi
	go test -tags=e2e -v -run '$(TEST)' $(if $(PKG),$(PKG),./...)

benchmark:
	go test -bench=. -count=5 -benchmem -run=^$  ./... | tee testresults/testperf.txt

lint:
	golangci-lint run

# Regenerate OpenAPI spec + docs.go from godoc-style annotations on handlers.
# Requires: go install github.com/swaggo/swag/cmd/swag@latest
swag:
	swag init -g cmd/main.go -o docs/swagger --parseDependency --parseInternal

validate: lint test

build: validate
	go build -o llm-server ./cmd/*.go

install: build
	mkdir ~/go/bin || true
	cp llm-server ~/go/bin/nudgebee-llm-server
