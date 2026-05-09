# OpenTelemetry (OTel) Collector Service

## Overview

The OTel Collector service is a Go-based application designed to receive, process, and forward OpenTelemetry (OTLP) data, including traces, metrics, and logs. It serves as a crucial component in a telemetry pipeline, enabling multi-tenancy by authenticating incoming data streams and injecting tenant-specific attributes.

Additionally, this service provides authenticated proxy endpoints for querying downstream Prometheus (for metrics) and Loki (for logs) instances, ensuring that queries are appropriately scoped to the authenticated tenant.

## Features

*   **OTLP Data Ingestion:**
    *   Receives traces, metrics, and logs via OTLP/HTTP (JSON and Protobuf formats).
    *   Supports `gzip` and `deflate` content encoding.
*   **Multi-Tenant Authentication:**
    *   Authenticates incoming OTLP data and proxy requests using Basic Authentication.
    *   Injects `nb.account_id` and `nb.tenant_id` resource attributes into OTLP data for tenant isolation.
*   **Data Forwarding:**
    *   Forwards processed OTLP data to configurable downstream backends (e.g., another OTel collector, specific telemetry systems like Jaeger, Prometheus, Loki).
*   **Prometheus Query Proxy:**
    *   Provides authenticated proxy endpoints for querying a Prometheus backend.
    *   Ensures queries are scoped by tenant/account.
*   **Loki Query Proxy:**
    *   Provides authenticated proxy endpoints for querying a Loki backend.
    *   Rewrites LogQL queries to include tenant/account label matchers for data isolation.
*   **Configuration:** Highly configurable via environment variables and a `.env` file.
*   **Observability:** Integrated with OpenTelemetry for its own distributed tracing and metrics.

## Getting Started

### Prerequisites

*   Go (version specified in `go.mod`)
*   Access to downstream telemetry backends (e.g., Prometheus, Loki, OTel-compatible systems) where data will be forwarded or queried.
*   (Optional) PostgreSQL database if specific persistence features are utilized (details in config).
*   (Optional) Redis instance if used for caching (details in config).

### Installation

1.  **Clone the repository:**
    ```bash
    git clone <repository-url>
    cd <path-to-collector-server/otel-collector>
    ```

2.  **Install dependencies:**
    The `Makefile` provides a convenient way to install dependencies:
    ```bash
    make install
    ```
    This typically runs `go mod tidy` or `go get`.

### Configuration

The service is configured using a `.env` file in the `collector-server/otel-collector` directory and/or environment variables. Refer to `config/config.go` for a comprehensive list of configuration options.

Key configuration variables include:

*   **OTLP Data Forwarding:**
    *   `OTEL_SERVER_TRACE_PROVIDER`, `OTEL_SERVER_TRACE_{PROVIDER}_ENDPOINT`, `OTEL_SERVER_TRACE_{PROVIDER}_FORMAT`: Configuration for trace forwarding (e.g., `OTEL_SERVER_TRACE_OTLP_ENDPOINT`).
    *   `OTEL_SERVER_METRICS_PROVIDER`, `OTEL_SERVER_METRICS_{PROVIDER}_ENDPOINT`, `OTEL_SERVER_METRICS_{PROVIDER}_FORMAT`: Configuration for metrics forwarding.
    *   `OTEL_SERVER_LOG_PROVIDER`, `OTEL_SERVER_LOG_{PROVIDER}_ENDPOINT`, `OTEL_SERVER_LOG_{PROVIDER}_FORMAT`: Configuration for log forwarding.
    *   If an endpoint is empty or set to "console", data will be logged to standard output.
*   **Query Proxy Endpoints:**
    *   `OTEL_SERVER_METRICS_QUERY_ENDPOINT`: The URL of the backend Prometheus instance (e.g., `http://localhost:9090`).
    *   `OTEL_SERVER_LOGS_QUERY_ENDPOINT`: The URL of the backend Loki instance (e.g., `http://localhost:3100`).
*   **Authentication & Security:**
    *   `NUDGEBEE_ENCRYPTION_KEY`: Secret key used for security operations (e.g., validating agent tokens).
*   **Database (Optional):**
    *   `OTEL_SERVER_DB_URL`: PostgreSQL connection string.
*   **Cache (Optional):**
    *   `CACHE_PROVIDER`: (`in_memory` or `redis`).
    *   `CACHE_REDIS_SERVER_HOST`, `CACHE_REDIS_SERVER_PORT`, etc.: Redis connection details if `CACHE_PROVIDER=redis`.
*   **Self-Observability:**
    *   `OTEL_EXPORTER_OTLP_ENDPOINT`: Endpoint for this service's own OpenTelemetry data.

Create a `.env` file in `collector-server/otel-collector` with your configurations:
```env
# Example .env file
NUDGEBEE_ENCRYPTION_KEY=your_very_secret_encryption_key

# OTLP Forwarding (example to another OTLP endpoint)
OTEL_SERVER_TRACE_PROVIDER=otlp
OTEL_SERVER_TRACE_OTLP_ENDPOINT=http://downstream-otel-collector:4318/v1/traces
OTEL_SERVER_TRACE_OTLP_FORMAT=application/x-protobuf

OTEL_SERVER_METRICS_PROVIDER=otlp
OTEL_SERVER_METRICS_OTLP_ENDPOINT=http://downstream-otel-collector:4318/v1/metrics
OTEL_SERVER_METRICS_OTLP_FORMAT=application/x-protobuf

OTEL_SERVER_LOG_PROVIDER=otlp
OTEL_SERVER_LOG_OTLP_ENDPOINT=http://downstream-otel-collector:4318/v1/logs
OTEL_SERVER_LOG_OTLP_FORMAT=application/x-protobuf

# Query Proxy
OTEL_SERVER_METRICS_QUERY_ENDPOINT=http://prometheus:9090
OTEL_SERVER_LOGS_QUERY_ENDPOINT=http://loki:3100

# ... other configurations
```

### Running the Service

The `Makefile` provides a convenient way to run the service:
```bash
make run
```
This will build and run the `cmd/main.go` application. The service typically starts an HTTP server on port 8000 (configurable).

## API Endpoints

All API endpoints require `Basic` authentication. The username part of the Basic Auth is typically the agent token, and the password part can be empty or a predefined string, depending on the `security.GetAccountFromAgentToken` implementation.

*   **Health Check:**
    *   `GET /health`: Returns the health status of the service. This endpoint is public.
*   **OTLP Data Ingestion:**
    *   `POST /otlp/v1/traces`: Accepts OTLP trace data (JSON or Protobuf).
    *   `POST /otlp/v1/metrics`: Accepts OTLP metric data (JSON or Protobuf).
    *   `POST /otlp/v1/logs`: Accepts OTLP log data (JSON or Protobuf).
*   **Prometheus Query Proxy:**
    *   `GET /prometheus/api/v1/query`
    *   `POST /prometheus/api/v1/query`
    *   `GET /prometheus/api/v1/query_range`
    *   `POST /prometheus/api/v1/query_range`
    *   `GET /prometheus/api/v1/series`
    *   `POST /prometheus/api/v1/series`
    *   `GET /prometheus/api/v1/labels`
    *   `POST /prometheus/api/v1/labels`
    *   `GET /prometheus/api/v1/label/:label_name/values`
*   **Loki Query Proxy:**
    *   `GET /loki/api/v1/query`
    *   `GET /loki/api/v1/query_range`
    *   `GET /loki/api/v1/labels`
    *   `GET /loki/api/v1/label/:label_name/values`
    *   `GET /loki/api/v1/series`

## Makefile Targets

The `Makefile` in `collector-server/otel-collector` provides several useful targets:

*   `make install`: Builds the service and copies the binary to `~/go/bin/nudgebee-otel-server`. Dependencies are typically downloaded via `go mod download` (e.g., during `make test`).
*   `make run`: Runs the service using `go run`.
*   `make build`: Builds the service binary.
*   `make test`: Runs unit tests and generates a coverage report.
*   `make fmt`: Formats the source code using `go fmt`.
*   `make benchmark`: Runs Go benchmarks.
*   `make lint`: Runs static analysis using `golangci-lint`.
*   `make validate`: Formats, lints, and tests the code.

## Contributing

Contributions to the OTel Collector service are welcome! Please follow these guidelines:

1.  **Fork the repository.**
2.  **Create a new branch** for your feature or bug fix.
3.  **Make your changes.**
    *   Adhere to existing coding standards.
    *   Write unit tests for new functionality.
    *   Run available `make` targets for formatting and validation.
4.  **Commit your changes** with a clear commit message.
5.  **Push to your fork.**
6.  **Create a Pull Request** against the main repository.

---

## Development Context

### Project Overview
The **OTel Collector** is a Go-based service responsible for ingesting, processing, and forwarding OpenTelemetry data (traces, metrics, logs) while ensuring multi-tenant isolation.

### Key Technologies
- **Language:** Go
- **Framework:** Gin (for proxy endpoints)
- **Protocol:** OTLP/HTTP (JSON & Protobuf)
- **Security:** Basic Auth for data ingestion; tenant-specific attribute injection.
- **Proxying:** Authenticated proxy for Prometheus and Loki backends.

### Development Conventions
- **Code Style:** Standard Go formatting (`go fmt`).
- **Validation:** Run `make validate` to execute linters (`golangci-lint`) and tests.
- **Testing:** Unit tests with coverage reports; uses `stretchr/testify` for assertions.
- **Multi-Tenancy:** Injects `nb.account_id` and `nb.tenant_id` into all resource attributes.
