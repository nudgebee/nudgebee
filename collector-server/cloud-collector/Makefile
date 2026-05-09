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
	golangci-lint run --timeout 20m --fast-only --concurrency=4

validate: lint test

build: validate
	go build -o cloud-collector-server ./cmd/*.go

install: build
	mkdir ~/go/bin || true
	cp cloud-collector-server ~/go/bin/nudgebee-cloud-collector-server
