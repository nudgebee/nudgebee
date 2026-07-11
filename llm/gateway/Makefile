# NB AI Gateway — build automation. Mirrors the llm-server convention
# (fmt / lint / test / validate / build / run / install), so `make validate`
# is the gate before commit, same as the other Go modules.
#
# Optional build-tag selector for the OSS/enterprise split (task 7). Empty by
# default; set externally to pull in files behind a specific //go:build tag:
#   make validate BUILD_TAGS=enterprise
BUILD_TAGS ?=
TAG_FLAG := $(if $(BUILD_TAGS),-tags=$(BUILD_TAGS))
LINT_TAG_FLAG := $(if $(BUILD_TAGS),--build-tags=$(BUILD_TAGS))

CMD := ./cmd/gateway
BINARY := llm-gateway

run:
	go run $(TAG_FLAG) $(CMD)

fmt:
	go fmt ./...

tidy:
	go mod tidy

test:
	go mod download
	mkdir testresults || true
	go test -coverprofile=testresults/testcoverage.txt -race $(TAG_FLAG) ./...
	go tool cover -html=testresults/testcoverage.txt -o testresults/testcoverage.html

benchmark:
	mkdir testresults || true
	go test -bench=. -count=5 -benchmem -run=^$$ ./... | tee testresults/testperf.txt

lint:
	golangci-lint run $(LINT_TAG_FLAG)

vet:
	go vet $(TAG_FLAG) ./...

validate: lint test

build: validate
	go build $(TAG_FLAG) -o $(BINARY) $(CMD)

install: build
	mkdir ~/go/bin || true
	cp $(BINARY) ~/go/bin/nudgebee-$(BINARY)

.PHONY: run fmt tidy test benchmark lint vet validate build install
