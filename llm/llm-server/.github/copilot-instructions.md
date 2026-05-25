# Copilot Instructions for LLM Server

## Project Architecture
- **Monorepo Structure:** Major components are organized by domain: `agents/` (specialized LLM agents), `llms/` (LLM provider integrations), `tools/` (agent tools), `api/` (HTTP endpoints), `config/` (configuration), `workflows/`, `relay/`, and `common/` (shared utilities).
- **Entry Point:** Main application starts from `cmd/main.go`.
- **Agent Pattern:** Each file in `agents/` implements a specialized agent (e.g., `agent_k8s_debug.go`, `agent_log_es.go`). Agents use LLMs and tools to automate tasks across cloud, observability, and database domains.
- **LLM Providers:** Integrations for OpenAI, Bedrock, Azure, Google Vertex AI, HuggingFace, and SLMs are in `llms/`. GoogleAI and Vertex code is code-generated for consistency (see `llms/googleai/README.md`).
- **Toolkit:** `tools/` provides implementations for cloud, monitoring, database, and CI/CD integrations. Agents invoke these tools to perform actions.
- **Event Processing:** Uses RabbitMQ for async event analysis and troubleshooting.
- **Observability:** OpenTelemetry is used for tracing and metrics.

## Developer Workflows
- **Build:** `make build` (runs lint, tests, then builds binary)
- **Run:** `make run` (runs server on configured port, default 8000)
- **Test:** `make test` (runs all unit tests, generates coverage)
- **Lint:** `make lint` (uses `golangci-lint`)
- **Format:** `make fmt`
- **Validate:** `make validate` (runs lint + test)
- **Install:** `make install` (builds and copies binary to `~/go/bin/nudgebee-llm-server`)
- **Benchmark:** `make benchmark`
- **Config:** Service config is via `.env` or environment variables. See `config/config.go` for all options.

## Key Conventions & Patterns
- **Agent Naming:** Each agent file is named for its domain (e.g., `agent_aws_debug_2.go`, `agent_log_loki.go`).
- **Provider Abstraction:** LLM and tool providers are abstracted for easy extension. New providers should follow the structure in `llms/` and `tools/`.
- **Shared Test Code:** For LLM providers, shared tests live in `llms/googleai/shared_test/` and are reused across providers.
- **API Auth:** All endpoints require a token in the header specified by `LLM_SERVER_TOKEN_HEADER`.
- **Event Queues:** RabbitMQ config is required for event-driven features.
- **RAG:** Retrieval Augmented Generation is supported via dedicated endpoints and config.

## Integration Points
- **External Services:** Integrates with AWS, GCP, Azure, Datadog, Prometheus, Loki, Elasticsearch, ClickHouse, MySQL, PostgreSQL, Redis, GitHub, Helm, Kubernetes, RabbitMQ, and Nudgebee internal services.
- **LLM Credentials:** Set via environment variables or `.env` (see `README.md` for examples).
- **Workflows:** Integrates with external workflows automation via `workflows/` and config.

## Examples
- To add a new agent: copy an existing file in `agents/`, follow the naming and structure, and register it in the agent registry.
- To add a new LLM provider: follow the pattern in `llms/`, provide config options, and add tests in `shared_test/` if applicable.
- To run the server locally: `make run` after configuring `.env`.

## References
- See `README.md` for setup, configuration, and workflow details.
- See `llms/googleai/README.md` for LLM provider codegen and test sharing.
- See `config/config.go` for all configuration options.
- See `GEMINI.md` for additional readme.
---

If any section is unclear or missing, please provide feedback for further refinement.
