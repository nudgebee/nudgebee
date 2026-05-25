# Setting up a Golang Project

This README provides instructions on how to set up a Golang project using the Gin web framework.

## Prerequisites

Before you begin, make sure you have the following installed:

- Go: Golang needs to be installed on your system. You can download it from [here](https://golang.org/dl/).
- For database set environment variable `export APP_DATABASE_URL=postgresql://<username>:<password>@localhost:5432/nudgebee?sslmode=disable` and port forward `kubectl --namespace nudgebee port-forward svc/pg-main-proxy-service 5432:5432`
- To use Relay server set environment variable `export RELAY_SERVER_SECRET_KEY=<>` and port forward `kubectl -n nudgebee port-forward svc/relay-server 52832:8080`

## Setup Steps
1. Navigate to `nudgebee/api-server/services` folder.
2. Execute `make install` to install dependencies.
3. Execute `make run` to run the project.
4. Execute `make test` to run unit test cases.
5. Execute `make fmt` to format the source code files.
6. Execute `make benchmark` this target executes Go benchmarks with memory profiling and generates performance results.
7. Execute `make validate` the command runs tests and performs static analysis using golangci-lint.

---

## Development Context

### Project Overview
This project is a collection of microservices written in Golang using the Gin web framework. Key services include:
* **account:** User accounts
* **alerts:** Alert management
* **anomaly:** Anomaly management
* **api:** Main API
* **application:** Application management
* **audit:** Audit logs
* **autopilot:** Autopilot system
* **billing:** Billing management
* **event:** Event management
* **eventrule:** Event rules
* **feedback:** Feedback management
* **insight:** Insight management
* **integrations:** Third-party integrations
* **marketplace:** Marketplace management
* **ml:** Machine learning models
* **notification:** Notifications
* **observability:** Observability
* **query:** Queries
* **recommendation:** Recommendations
* **relay:** Relay server
* **security:** Security management
* **slo:** SLOs
* **spend:** Spend management
* **tenant:** Tenant management
* **user:** User management

### Development Conventions
* **Coding Style:** Follow standard Golang conventions. Format code with `go fmt`.
* **Linting:** Enforced via `golangci-lint`.
* **Testing:** All new code must include unit tests using the standard Go testing framework.
