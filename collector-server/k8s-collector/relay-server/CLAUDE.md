# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Service Overview

**Relay-Server** is a Kubernetes cluster gateway and API proxy service that bridges HTTP requests from the Nudgebee cloud platform to K8s cluster agents via RabbitMQ. It provides:

- **RPC Forwarding**: Publishes requests to agent-specific RabbitMQ queues and waits for responses with correlation ID matching
- **WebSocket Gateway**: Agent registration endpoint and interactive shell support
- **HTTP Proxies**: Grafana, Prometheus v2, and generic API proxy endpoints
- **Observability**: OpenTelemetry instrumentation for tracing and metrics
- **Resilience**: Auto-reconnect to RabbitMQ with exponential backoff, fallback HTTP routing for disabled agents

**Environment**: Kubernetes (deployed across main/test/prod via Helm), communicates with K8s agents via RabbitMQ and direct HTTP fallback.

## Build and Development Commands

### Local Development
```bash
make run          # Start relay-server locally (go run ./cmd)
make fmt          # Format code (go fmt ./...)
make lint         # Lint with golangci-lint (10m timeout)
make test         # Run tests with coverage + race detector, outputs to testresults/
make benchmark    # Run benchmarks (5 iterations, outputs testperf.txt)
make validate     # Run lint + test (MUST pass before build)
make build        # Build binary (after validate passes)
make install      # Copy binary to ~/go/bin/nudgebee-relay-server
```

### Running Tests
```bash
# Full test suite with coverage
make test

# Run specific test file
go test ./pkg/server/handlers -v

# Run single test
go test ./pkg/server/handlers -run TestNewRequestHandler -v

# Run with coverage profile
go test -coverprofile=coverage.txt ./...
go tool cover -html=coverage.txt

# Run with race detector
go test -race ./...

# Run benchmarks
go test -bench=. -benchmem ./...
```

### Code Quality
- **Formatting**: Code must pass `go fmt` before commit
- **Linting**: All code must pass `golangci-lint run` (10m timeout)
- **Testing**: All tests must pass with race detector enabled
- **Validation**: Always run `make validate` before `make build`

## Architecture

### Request Flow
```
Client Request (HTTP)
    ↓
Gin Router + Middleware Stack:
  1. Recovery (panic handler)
  2. Structured JSON logging (slog, skip /status)
  3. OpenTelemetry tracing (skip /status for noise reduction)
  4. CORS (allow all)
  5. Response trace headers (W3C)
    ↓
Auth Middleware (route-specific: Basic, Bearer, or Secret-Key)
    ↓
Handler (route logic):
  - Parse request (zero-copy gjson for performance)
  - Validate agent via cached DB lookup (30min TTL)
  - Check WS enabled + agent connected
  - If enabled: Publish to RabbitMQ queue, await response with timeout
  - If disabled: Fallback to direct HTTP to agent
    ↓
Response (HTTP or RPC)
```

### Key Components

#### **RabbitMQ Integration** (`pkg/mq/`)
- **rabbitmq.go**: Connection manager with auto-reconnect (exponential backoff on failure)
- **topology.go**: Per-tenant queue setup (idempotent `EnsureTenant()`) with DLX and TTL
- **rpc_client.go**: Publish requests and match responses via correlation ID using `sync.Map`

Each request publishes to exchange `nudgebee-relay` with routing key `relay_requests_{accountID}`, receives response from auto-deleted reply queue.

#### **HTTP Server** (`pkg/server/`)
- **router.go**: Gin setup, middleware chain, route registration
- **handlers/**: 8 endpoint handlers (register, ws, request, grafana, prometheus-v2, prometheus-legacy, api-proxy, status)
- **middleware/auth.go**: Auth validation (Basic: accessKey+secret, Bearer: token, Secret-Key: header)
- **metrics/**: OTel metric definitions (histograms, counters, up-down counters)

#### **Request Processing** (`pkg/utils/`)
- **utils.go**: Serialization functions, fallback routing, secret decryption, Prometheus enrichment
- **prometheus_step.go**: Intelligent step calculation based on time range

#### **Data Access** (`pkg/db/`)
- **db_store.go**: Agent validation and WS permission checking with dual caching (30min TTL)
- Uses PostgreSQL `sqlx` with connection pooling (25 max open, 25 idle)
- Supports two secret types: bcrypt (v2) and AES-256 encrypted (v1)

#### **Configuration** (`pkg/config/`)
- **config.go**: Viper-based config (env vars with RELAY_*, COLLECTOR_DB_URL, NUDGEBEE_ENCRYPTION_KEY prefixes)
- Sections: HTTP (port, timeouts), Postgres, RabbitMQ, Security, OpenTelemetry
- Sensible defaults for local development

#### **Observability** (`pkg/otel/`)
- OpenTelemetry v1.35.0 with console or OTLP exporters
- Custom trace sampler (skips /status endpoint)
- Async metric buffering (10K buffer, 100 batch, 1s flush)

### Design Patterns

**Zero-Copy Request Processing**: Uses `gjson` for fast JSON parsing without unmarshaling, reduces memory allocations.

**Dual Caching**: Agent validation cache (accountID from accessKey) + WS permission cache (account permissions), both 30min TTL with `sync.Map` for lock-free reads.

**Correlation ID Matching**: RPC client stores pending requests in `sync.Map`, consumer goroutine dispatches responses by ID without blocking request handlers.

**Multi-Level Fallback**: If WS disabled → direct HTTP to fallback URL; if agent not connected → 400 error; if RPC timeout → 504 error.

**Graceful Shutdown**: 2s grace period for in-flight requests before terminating on SIGINT/SIGTERM.

## Routes and Endpoints

| Route | Method | Auth | Handler | Purpose |
|-------|--------|------|---------|---------|
| `/status` | GET | None | StatusHandler | Health check, returns "OK" |
| `/register` | GET | Basic | RegisterHandler | Agent WebSocket registration, parallel RPC forwarding |
| `/ws` | POST | Secret-Key | WSHandler | Interactive shell upgrade (POST → WebSocket) |
| `/request` | POST | Secret-Key | NewRequestHandler | Main RPC dispatcher for actions, supports Prometheus enrichment |
| `/grafana/*` | Any | Secret-Key | NewGrafanaHandler | HTTP proxy to Grafana (strips `/grafana` prefix) |
| `/prometheus-v2/*` | Any | Secret-Key | NewPrometheusHandler | HTTP proxy to Prometheus v2 (adds X-NB-Request-Type header) |
| `/prometheus/api/v1/*` | GET/POST | Bearer + X-Scope-OrgID | HandlePrometheusApis | Legacy Prometheus endpoints (query, query_range, series, labels) |
| `/api/proxy/*` | Any | Secret-Key | NewAPIProxyHandler | Generic API proxy (uses X-API-Base-URL header) |

## Configuration

### Environment Variables
```bash
# HTTP Server
RELAY_HTTP_PORT=8080                    # Server port
RELAY_HTTP_READ_TIMEOUT=180s            # Request read timeout
RELAY_HTTP_WRITE_TIMEOUT=180s           # Response write timeout

# Database
COLLECTOR_DB_URL=postgres://user:pass@host:5432/dbname

# RabbitMQ
RABBIT_MQ_HOST=rabbitmq                 # RabbitMQ hostname
RABBIT_MQ_PORT=5672                     # RabbitMQ port
RABBIT_MQ_USERNAME=guest                # RabbitMQ user
RABBIT_MQ_PASSWORD=guest                # RabbitMQ password
RABBITMQ_EXCHANGE_NAME=nudgebee-relay   # Exchange name for requests
RABBITMQ_REQUEST_QUEUE=nudgebee_relay_request
RABBITMQ_MESSAGE_TTL=1m                 # Message TTL in queue

# Security
RELAY_SERVER_SECRET_KEY=<32-char-string> # Static key for X-SECRET-KEY auth header
NUDGEBEE_ENCRYPTION_KEY=<base64>         # AES key for decrypting agent secrets

# OpenTelemetry
OTEL_EXPORTER=noop|console|otlp          # Exporter type
OTEL_EXPORTER_OTLP_ENDPOINT=127.0.0.1:4317
OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=...
OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=...
OTEL_TRACES_EXPORTER=otlp|console|noop
OTEL_METRICS_EXPORTER=otlp|console|noop
```

### Local Development Setup
1. Create `.env` file in service root with above variables
2. Ensure PostgreSQL is accessible at COLLECTOR_DB_URL
3. Ensure RabbitMQ is accessible at RABBIT_MQ_HOST:RABBIT_MQ_PORT
4. Run `make run` to start service on port 8080

## Testing Patterns

### Unit Tests
- Located alongside source files: `*_test.go`
- Use `stretchr/testify` assertions
- Create fake implementations of interfaces (`fakeStore` for `AgentStore`, `fakeRPCClient` for `RPCClient`)
- Use `httptest.NewRequest`/`NewRecorder` for HTTP handler tests
- Use `noop.Meter`/`noop.Tracer` for OTEL mocks

### Example Test Structure
```go
func TestHandler(t *testing.T) {
    // Arrange
    fakeStore := &fakeStore{...}
    fakeRPC := &fakeRPCClient{...}

    // Act
    req := httptest.NewRequest("POST", "/endpoint", body)
    rec := httptest.NewRecorder()
    handler(fakeStore, fakeRPC).ServeHTTP(rec, req)

    // Assert
    assert.Equal(t, http.StatusOK, rec.Code)
}
```

### Test Commands
```bash
make test                           # Full test suite with coverage
go test ./pkg/server/handlers -v   # Verbose output
go test -race ./...                 # Race detector
go test -bench=. -benchmem ./...    # Benchmarks
```

## Code Organization

```
relay-server/
├── cmd/main.go              # Service initialization, OTEL setup, graceful shutdown
├── pkg/
│   ├── config/config.go     # Viper configuration management
│   ├── db/db_store.go       # PostgreSQL agent store with caching
│   ├── models/models.go     # Request/response data structures
│   ├── mq/                  # RabbitMQ layer
│   │   ├── rabbitmq.go      # Connection manager
│   │   ├── topology.go      # Exchange/queue setup
│   │   └── rpc_client.go    # RPC publish/consume
│   ├── otel/otel.go         # OpenTelemetry setup
│   ├── server/
│   │   ├── router.go        # Gin router + middleware
│   │   ├── handlers/        # 8 endpoint handlers
│   │   ├── middleware/      # Auth validation
│   │   └── metrics/         # OTel metric definitions
│   └── utils/               # Serialization, fallback logic
├── Makefile                 # Build automation
├── Dockerfile               # Multi-stage Docker build
└── go.mod / go.sum          # Dependencies
```

## Common Workflows

### Adding a New Handler
1. Create `pkg/server/handlers/newhandler.go` with handler function
2. Add route in `pkg/server/router.go` with appropriate auth middleware
3. Create `*_test.go` with unit tests using fake objects
4. Run `make validate` before commit

### Debugging Request Flow
1. Enable verbose logging by checking `slog` output
2. Check OTEL trace context (trace ID in logs and response headers)
3. Look at RabbitMQ message queue to verify publish
4. Check database for agent validation (ps aux, sqlx queries)
5. Use `make benchmark` for performance profiling

### Local Testing
```bash
# Terminal 1: Start relay-server
make run

# Terminal 2: Send test request
curl -X POST http://localhost:8080/status
curl -X POST http://localhost:8080/request \
  -H "X-SECRET-KEY: your-secret-key" \
  -H "Content-Type: application/json" \
  -d '{"account_id":"...", "cluster_name":"...", ...}'
```

## Key Decisions and Trade-offs

- **Zero-copy parsing**: Uses `gjson` instead of unmarshal for performance, slightly less type-safe
- **Async metrics**: Non-blocking metric recording with buffering, may lose metrics if process crashes
- **30min cache TTL**: Balances staleness (agent changes) vs database load
- **Fallback routing**: Allows graceful degradation if RabbitMQ is slow, but may miss updates
- **Per-tenant RabbitMQ queues**: Provides isolation and per-tenant monitoring, adds topology setup overhead

## Dependencies

Core dependencies:
- `gin-gonic/gin`: HTTP framework
- `gorilla/websocket`: WebSocket support
- `rabbitmq/amqp091-go`: RabbitMQ client
- `jmoiron/sqlx`: Database access
- `go.opentelemetry.io/otel`: Distributed tracing and metrics
- `spf13/viper`: Configuration management
- `tidwall/gjson`: Zero-copy JSON parsing

See `go.mod` for complete list and versions.

## Docker Build

Multi-stage Dockerfile: golang:1.26-alpine (build) → alpine:3.19 (runtime). Exposes port 8080. Sets `GIN_MODE=release` for production.

```bash
docker build -t nudgebee-relay-server:latest .
docker run -e COLLECTOR_DB_URL=... -e RABBIT_MQ_HOST=... -p 8080:8080 nudgebee-relay-server:latest
```
