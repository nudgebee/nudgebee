run:
	go run .

fmt:
	go fmt ./...

test:
	go mod download
	mkdir testresults || true
	go test -coverprofile=testresults/testcoverage.txt -race ./...
	go tool cover -html=testresults/testcoverage.txt -o testresults/testcoverage.html

validate: lint test
	gofmt -l .
	go vet ./...

build: validate
	go build -o vuln-matcher-server .

lint:
	golangci-lint run
