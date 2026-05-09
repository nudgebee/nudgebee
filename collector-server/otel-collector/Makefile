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
	golangci-lint run --timeout=10m

validate: fmt lint test

build: validate
	go build -o otel-server ./cmd/*.go

install: build
	mkdir ~/go/bin || true
	cp otel-server ~/go/bin/nudgebee-otel-server
