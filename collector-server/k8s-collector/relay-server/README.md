# Relay-Server

## Project Overview
**Relay-Server** is a Kubernetes cluster gateway and API proxy service that bridges HTTP requests from the Nudgebee cloud platform to K8s cluster agents via RabbitMQ. It provides RPC forwarding, WebSocket gateways, and HTTP proxies for Grafana and Prometheus.

## Building and Running

### Commands
*   `make run`: Start relay-server locally.
*   `make fmt`: Format code.
*   `make lint`: Lint with `golangci-lint`.
*   `make test`: Run tests with coverage and race detector.
*   `make validate`: Run lint + test (MUST pass before build).
*   `make build`: Build binary.

---

## Development Context

### Service Overview
**Relay-Server** is a Kubernetes cluster gateway and API proxy service that bridges HTTP requests from the Nudgebee cloud platform to K8s cluster agents via RabbitMQ. Key features include:
- **RPC Forwarding**: Publishes requests to agent-specific RabbitMQ queues and waits for responses with correlation ID matching.
- **WebSocket Gateway**: Agent registration endpoint and interactive shell support.
- **HTTP Proxies**: Grafana, Prometheus v2, and generic API proxy endpoints.
- **Observability**: OpenTelemetry instrumentation for tracing and metrics.
- **Resilience**: Auto-reconnect to RabbitMQ with exponential backoff.

### Architecture Patterns
- **Zero-Copy Request Processing**: Uses `gjson` for fast JSON parsing without unmarshaling, reducing memory allocations.
- **Dual Caching**: Agent validation cache + WS permission cache, both with 30min TTL using `sync.Map` for lock-free reads.
- **Correlation ID Matching**: RPC client stores pending requests in `sync.Map`, consumers dispatch responses by ID.
- **Request Flow**: Gin Router → Middleware (Recovery → structured JSON logging (slog, skip /status) → OpenTelemetry tracing (skip /status) → CORS (allow all) → W3C response trace headers) → Auth middleware → Handler (RabbitMQ RPC or direct HTTP fallback).

### Key Components
- **RabbitMQ Integration (`pkg/mq/`)**: Connection manager with auto-reconnect and per-tenant queue setup.
- **HTTP Server (`pkg/server/`)**: Gin setup, route registration, and OTel metric definitions.
- **Data Access (`pkg/db/`)**: PostgreSQL `sqlx` with connection pooling and dual caching.
