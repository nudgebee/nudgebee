# Ticket Server

The `ticket-server` module is a Go-based microservice that provides an HTTP API for managing tickets. It integrates with various third-party services to create, update, and manage tickets.

## Features

-   Create, update, and manage tickets
-   Integrates with PagerDuty, Jira, and ServiceNow
-   Provides a RESTful API for interacting with the service

## Getting Started

To get started with the ticket server, you will need to:

1.  Install the dependencies
2.  Configure the module
3.  Run the server

### Dependencies

The `ticket-server` module uses Go modules to manage its dependencies. To install the dependencies, run the following command:

```bash
go mod download
```

### Build Commands

```bash
make run        # Start the server locally (go run ./cmd)
make fmt        # Format code (go fmt)
make lint       # Lint with golangci-lint
make test       # Run tests with coverage and race detector
make validate   # Run lint + test (must pass before build)
make build      # Build binary (runs validate first)
make install    # Install binary to ~/go/bin
```

### Configuration

The `ticket-server` module is configured using environment variables. The following environment variables are available:

-   `PAGERDUTY_API_KEY`: The API key for the PagerDuty service
-   `JIRA_API_KEY`: The API key for the Jira service
-   `SERVICENOW_API_KEY`: The API key for the ServiceNow service

For database and RabbitMQ connectivity (see Key Technologies below), additional environment variables are required per your deployment configuration.

### Running the Server

To run the server, use the following command:

```bash
make run
```

---

## Development Context

### Project Overview
The **Ticket Server** is a Go-based microservice responsible for lifecycle management of operational tickets. It acts as a bridge between the Nudgebee platform and external ticketing systems.

### Key Technologies
- **Language:** Go
- **Framework:** Gin (HTTP API)
- **Database:** PostgreSQL (via `sqlx`)
- **Messaging:** RabbitMQ (for event-driven ticket updates)
- **Integrations:** PagerDuty, Jira, ServiceNow

### Development Conventions
- **Code Style:** Standard Go formatting (`go fmt`).
- **Validation:** Run `make validate` to execute linters (`golangci-lint`) and tests.
- **Testing:** Unit tests are located alongside source code; use the standard Go testing library.
