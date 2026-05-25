run:
	go run ./cmd


fmt:
	go fmt ./...

test:
	go mod download
	mkdir testresults || true
	go test -coverprofile=testresults/testcoverage.txt  -race ./...
	go tool cover -html=testresults/testcoverage.txt -o testresults/testcoverage.html

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
