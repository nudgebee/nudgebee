# Tech Stack

## Core Development
*   **Language:** Go (v1.25.1) - The primary language for backend service development.
*   **Web Framework:** Gin - Used for building the RESTful API endpoints.

## LLM & AI Orchestration
*   **LLM Providers:**
    *   AWS Bedrock (Primary)
    *   Azure OpenAI
    *   Google Vertex AI
    *   HuggingFace
    *   AWS Sagemaker
*   **Client Integration:** Bidirectional tool execution protocol for remote/client-side tool orchestration.
*   **Agent Framework:** Custom hierarchical framework supporting ReWOO and ReAct planners.

## Data & Messaging
*   **Databases:** 
    *   PostgreSQL (Primary application state)
    *   ClickHouse (Analytical data/Traces)
    *   Redis (Caching)
*   **Messaging:** RabbitMQ - Used for asynchronous event processing and agent task distribution.

## Observability & Infrastructure
*   **Monitoring/Logging:** 
    *   OpenTelemetry (Instrumentation)
    *   Datadog (Metrics, Logs, Traces)
    *   Prometheus (Metrics)
    *   Loki (Logs)
    *   Elasticsearch (Logs/Search)
*   **Infrastructure & Tools:**
    *   Kubernetes (via `kubectl`)
    *   AWS SDK & CLI
    *   GCP SDK & CLI
    *   GitHub API
    *   Helm
