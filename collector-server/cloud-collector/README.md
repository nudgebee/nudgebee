# Cloud Collector Service

## Overview

The Cloud Collector service is a Go-based application responsible for gathering data and events from various cloud providers. It acts as a central hub for collecting cloud resource information, metrics, logs, and events, which can then be processed, stored, and analyzed. The service is designed to be extensible, allowing new cloud providers and data collection capabilities to be added over time.

It uses a Gin-based HTTP server to expose APIs for managing cloud accounts and triggering data collection tasks. It also includes background workers for asynchronous event processing, such as consuming events from AWS EventBridge via SQS.

## Features

*   **Multi-Cloud Support:** Designed to support multiple cloud providers. (Currently, AWS is the primary supported provider, with a structure for others like GCP).
*   **Data Collection:**
    *   Collects cloud resource information.
    *   Gathers metrics and logs.
    *   Processes events from cloud provider services (e.g., AWS EventBridge).
*   **API Endpoints:** Provides RESTful APIs for:
    *   Health checks.
    *   Managing cloud provider connections.
    *   Triggering data collection tasks.
    *   Handling RPC actions/events dispatched by the in-app gateway.
*   **Asynchronous Event Processing:** Utilizes message queues (e.g., AWS SQS) for reliable and scalable event handling.
*   **Configuration:** Highly configurable via environment variables and a `.env` file.
*   **Observability:** Integrated with OpenTelemetry for distributed tracing and metrics.
*   **Authentication:** API endpoints are protected by token-based authentication.

## Getting Started

### Prerequisites

*   Go (version specified in `go.mod`)
*   Access to a PostgreSQL database (for storing configuration and collected data, if applicable).
*   Access to a RabbitMQ instance (for notifications).
*   Access to a ClickHouse instance (for storing collected data, if enabled).
*   (Optional) AWS account and credentials if using the AWS provider.

### Installation

1.  **Clone the repository:**
    ```bash
    git clone <repository-url>
    cd <path-to-collector-server/cloud-collector>
    ```

2.  **Install dependencies:**
    The existing `Makefile` provides a convenient way to install dependencies:
    ```bash
    make install
    ```
    This typically runs `go mod tidy` or `go get`.

### Configuration

The service is configured using a `.env` file in the `collector-server/cloud-collector` directory and/or environment variables. Refer to `config/config.go` for a comprehensive list of configuration options.

Key configuration variables include:

*   `CLOUD_COLLECTOR_SERVER_TOKEN_HEADER`: The HTTP header name for the authentication token. (Default: `X-ACTION-TOKEN`)
*   `CLOUD_COLLECTOR_SERVER_TOKEN`: The secret token required to access protected API endpoints.
*   `CLOUD_COLLECTOR_SERVER_DB_URL`: PostgreSQL database connection string. (e.g., `postgresql://user:password@host:port/dbname?sslmode=disable`)
*   `NUDGEBEE_ENCRYPTION_KEY`: A secret key for data encryption.
*   `OTEL_EXPORTER_OTLP_ENDPOINT`: Endpoint for OpenTelemetry collector.
*   `RABBIT_MQ_USERNAME`, `RABBIT_MQ_PASSWORD`, `RABBIT_MQ_HOST`, `RABBIT_MQ_PORT`: RabbitMQ connection details.
*   `CLICKHOUSE_HOST`, `CLICKHOUSE_USER`, `CLICKHOUSE_PASSWORD`, `CLICKHOUSE_DATABASE`, `CLICKHOUSE_ENABLED`: ClickHouse connection details.
*   `CLOUD_COLLECTOR_AWS_EVENTBRIDGE_SQS`: The SQS queue URL for AWS EventBridge events (if using AWS provider).

Create a `.env` file in the `collector-server/cloud-collector` directory with your desired configurations:

```env
# Example .env file
CLOUD_COLLECTOR_SERVER_TOKEN=your_secret_token
CLOUD_COLLECTOR_SERVER_DB_URL=postgresql://postgres:postgres@localhost:5432/nudgebee?sslmode=disable
NUDGEBEE_ENCRYPTION_KEY=your_strong_encryption_key
# ... other configurations
```

### Running the Service

The existing `Makefile` provides a convenient way to run the service:

```bash
make run
```

This will typically build and run the `cmd/main.go` application. The service will start an HTTP server, usually on port 8000 (configurable).

## API Endpoints

The service exposes several API endpoints. For detailed information on request/response formats, please refer to the API handler functions in the `api/` directory (e.g., `api/cloud.go`, `api/actions.go`, `api/health.go`).

*   **Health Check:**
    *   `GET /health`: Returns the health status of the service. This endpoint is public and does not require authentication.
*   **Cloud Provider APIs:**
    *   Endpoints for specific cloud providers are typically prefixed (e.g., `/aws/...`, `/gcp/...`). These require authentication.
*   **RPC Action Handlers:**
    *   Endpoints invoked by the in-app gateway (`@lib/rpcGateway`) when the frontend issues an action whose handler routes here. These require authentication.

Authentication is performed using a token provided in the HTTP header specified by `CLOUD_COLLECTOR_SERVER_TOKEN_HEADER`.

## Makefile Targets

The `Makefile` in `collector-server/cloud-collector` provides several useful targets:

*   `make install`: Installs project dependencies.
*   `make run`: Builds and runs the service.
*   `make test`: Runs unit tests.
*   `make fmt`: Formats the source code.
*   `make benchmark`: Runs Go benchmarks with memory profiling.
*   `make validate`: Runs tests and performs static analysis using `golangci-lint`.

## Pod IAM Permissions (AWS)

The cloud-collector pod requires its own IAM permissions (via node instance profile or IRSA) for operations that use the pod's own credentials — distinct from customer account credentials obtained via STS AssumeRole.

### Minimum Required Permissions

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "AssumeCustomerRoles",
      "Effect": "Allow",
      "Action": "sts:AssumeRole",
      "Resource": "*",
      "Comment": "Assume customer IAM roles to collect their cloud data"
    },
    {
      "Sid": "SQSEventProcessing",
      "Effect": "Allow",
      "Action": [
        "sqs:GetQueueUrl",
        "sqs:GetQueueAttributes",
        "sqs:ReceiveMessage",
        "sqs:DeleteMessage"
      ],
      "Resource": [
        "arn:aws:sqs:*:<ACCOUNT_ID>:*-eventbridge-queue",
        "arn:aws:sqs:*:<ACCOUNT_ID>:*-org-registration-queue"
      ],
      "Comment": "Consume EventBridge events and org registration messages from nudgebee SQS queues"
    },
    {
      "Sid": "PricingAPIAccess",
      "Effect": "Allow",
      "Action": [
        "pricing:GetProducts",
        "pricing:GetAttributeValues",
        "pricing:DescribeServices"
      ],
      "Resource": "*",
      "Comment": "Fetch EC2/ElastiCache/RDS on-demand pricing for cost recommendations"
    }
  ]
}
```

### Permission Details

| AWS Service | Actions | Purpose | Used In |
|---|---|---|---|
| STS | `AssumeRole` | Assume customer IAM roles for data collection | `event_org_registration.go` |
| SQS | `GetQueueUrl`, `GetQueueAttributes`, `ReceiveMessage`, `DeleteMessage` | Consume events from nudgebee-owned SQS queues (EventBridge events, org registration) | `event_eventbridge.go`, `event_org_registration.go` |
| Pricing | `GetProducts`, `GetAttributeValues`, `DescribeServices` | Fetch on-demand instance pricing for cost recommendations (EC2, RDS, ElastiCache) | `aws_ec2.go`, `aws_cache.go`, `aws_pricing_common.go` |

### What Does NOT Use Pod Credentials

These operations use **customer-assumed STS sessions**, not pod credentials:
- Cost Explorer recommendations
- Cost Optimization Hub recommendations
- Trusted Advisor checks
- CloudWatch alarms/metrics
- Resource discovery (EC2, RDS, S3, etc.)
- All resource-level API calls

### Notes

- The Pricing API is only available in `us-east-1` — the pod loads its own credentials with that region override.
- If the pod IAM role lacks `pricing:GetProducts`, EC2 pricing lookups fail with `AccessDeniedException`. The code has an in-memory failure cache (1h TTL) to suppress repeated failures, but pricing data will be missing from recommendations.
- SQS queue ARNs are configured via `CLOUD_COLLECTOR_AWS_EVENTBRIDGE_SQS` and `CLOUD_COLLECTOR_AWS_ORG_REGISTRATION_SQS` environment variables.

## Contributing

Contributions to the Cloud Collector service are welcome! Please follow these guidelines:

1.  **Fork the repository.**
2.  **Create a new branch** for your feature or bug fix: `git checkout -b feature/your-feature-name` or `bugfix/your-bug-fix`.
3.  **Make your changes.**
    *   Ensure your code adheres to existing coding standards.
    *   Write unit tests for new functionality.
    *   Run `make fmt` and `make validate` to ensure code quality and tests pass.
4.  **Commit your changes:** `git commit -m "feat: Describe your feature"` or `fix: Describe your fix`.
5.  **Push to your fork:** `git push origin feature/your-feature-name`.
6.  **Create a Pull Request** against the main repository.

Please provide a clear description of your changes in the Pull Request.

---

## Development Context

### Project Overview
The Cloud Collector service is built using the Gin web framework and uses the AWS SDK for Go. It features background workers for asynchronous event processing.

### Development Conventions
*   **API Structure:** Defined in `api/` directory. Main routes in `api/api.go`. Cloud-specific handlers in `api/cloud.go`. Action handlers in `api/actions.go`.
*   **Providers:** Implementation for different cloud providers is in `providers/`. AWS implementation is in `providers/aws/`.
*   **Configuration:** Uses `.env` and Viper. Base configuration structure in `config/config.go`.
*   **Authentication:** Middleware in `cmd/main.go`.
*   **Observability:** Integrated with OpenTelemetry. Initialization in `cmd/otel.go`.
