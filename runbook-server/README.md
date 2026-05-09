# Runbook Server

## Project Description
The Runbook Server is a backend service designed to automate operational workflows. It allows users to define workflows using YAML/JSON, execute tasks, and manage workflow lifecycles. The server integrates with various cloud providers, LLMs, and other services to orchestrate complex runbooks.

For detailed documentation on Trigger Configurations (Schedule, Webhook), see [docs/TRIGGERS.md](docs/TRIGGERS.md).

## Technologies Used
- Go
- Temporal.io (for workflow orchestration)
- PostgreSQL (for metadata storage)
- Gin (for HTTP API)

## Setup Instructions

### Prerequisites
- Go (version 1.26 or higher)
- Docker and Docker Compose (for local PostgreSQL and Temporal setup)
- `golangci-lint` (for linting)

### 1. Clone the Repository
```bash
git clone <repository_url>
cd runbook-server
```

### 2. Environment Variables
Create a `.env` file in the project root based on `.env.example` (if available) or the following structure:
```
# Database
DB_HOST=localhost
DB_PORT=5432
DB_USER=user
DB_PASSWORD=password
DB_NAME=runbook_db

# Temporal
TEMPORAL_GRPC_ENDPOINT=localhost:7233
TEMPORAL_NAMESPACE=default
```

### 3. Start Local Dependencies (PostgreSQL + Temporal)

Start all local services (PostgreSQL, Temporal, Temporal UI) via Docker Compose:
```bash
make start-local-env
```

To stop them:
```bash
make stop-local-env
```

> This uses `docker-compose.test.yml` which sets up PostgreSQL, Temporal, the Temporal UI, and the Temporal setup job.

### 4. Install Go Modules
```bash
go mod download
```

### 5. Build Commands

```bash
make run        # Start the server locally (go run cmd/main.go)
make fmt        # Format code (go fmt)
make lint       # Lint with golangci-lint
make test       # Run tests
make validate   # Run lint + test
make build      # Build binary to dist/runbook-server
make install    # Install binary via go install
```

### 6. Run the Application
```bash
make run
```
The server will start on `http://localhost:8080` (or the port configured in `api/server.go`).

## API Endpoints (Overview)

### Workflows (`/workflows`)
- `POST /workflows`: Create a new workflow definition.
- `GET /workflows`: List all workflow definitions.
- `GET /workflows/:id`: Get a specific workflow definition.
- `PUT /workflows/:id`: Update an existing workflow definition.
- `DELETE /workflows/:id`: Delete a workflow definition.
- `POST /workflows/:id/trigger`: Manually trigger a workflow execution.
- `GET /workflows/:id/runs`: List all executions for a workflow.
- `GET /workflows/:id/runs/:run_id`: Get details of a specific workflow execution.
- `PUT /workflows/:id/runs/:run_id`: Update a running workflow execution (e.g., signal inputs).
- `POST /workflows/:id/runs/:run_id/cancel`: Cancel a running workflow execution.
- `POST /workflows/:id/pause`: Pause a scheduled workflow.
- `POST /workflows/:id/resume`: Resume a paused scheduled workflow.
- `POST /workflows/validate`: Validate a workflow definition without saving it.

### Tasks (`/tasks`)
- `GET /tasks`: List all registered task types.
- `POST /tasks/:task_type/execute`: Execute a single task directly.

### Approvals (`/approvals`)
- `POST /approvals/:token`: Handle external approval signals for workflow tasks.

### Webhooks (`/webhook/:workflowId`)
- `POST /webhook/:workflowId`: Generic webhook endpoint to trigger workflows.

## Development

### Linting
```bash
make lint
```

### Testing
```bash
make test
```
For integration tests, ensure local services are running (`make start-local-env`) and environment variables are set.

## Contributing
(Add guidelines for contributing if applicable)

## License
(Add license information)

---

## Development Context

### Project Overview
The Runbook Server orchestrates operational workflows using Temporal.io. It persists workflow definitions and metadata in PostgreSQL.

### Development Conventions
*   **Task Framework:** Pluggable framework. Tasks must implement `internal/tasks/types.Task`. Register tasks in `internal/tasks/registry.go`.
*   **Structured Output:** `Execute` methods return `map[string]any`. Wrap strings as `{"data": "cli_output"}`.
*   **Workflow Auditing:** Definitions include `CreatedBy`, `UpdatedBy`, `CreatedAt`, and `UpdatedAt`. Executions include `ParentWorkflowID` and `TriggeredBy`.
*   **API Design:** Handled in `api/handlers.go`. User/tenant info passed via `X-User-ID`, `X-Tenant-ID`, and `X-Account-ID` headers.
*   **Special Considerations:** Always run `make lint` for Go projects.
