# LLM Server

## Overview

The LLM Server is a comprehensive Go-based backend service that orchestrates Large Language Model (LLM) operations, manages autonomous agents, and provides a rich toolkit for interacting with a wide array of external systems. It serves as the central intelligence hub for LLM-powered applications, enabling complex task automation, data analysis, and conversational AI capabilities.

The server supports multiple LLM providers, offers a framework for building specialized agents, and integrates a diverse set of tools for interacting with cloud services, monitoring platforms, databases, version control systems, and more. It exposes APIs for managing conversations, invoking agents, utilizing tools, and performing Retrieval Augmented Generation (RAG).

## Features

*   **Multi-LLM Provider Support:** Integrates with various LLM providers including:
    *   AWS Bedrock
    *   Azure OpenAI
    *   OpenAI
    *   Google Vertex AI (implied via GCP tools, to be confirmed if direct LLM client exists)
    *   HuggingFace (direct inference and potentially via Sagemaker)
    *   AWS Sagemaker
    *   Support for specialized Smaller Language Models (SLMs) for tasks like PromQL and LogQL query generation.
*   **Agent Framework:** Provides a robust framework for defining and running autonomous agents that can leverage LLMs and tools to achieve specific goals.
*   **Extensive Toolkit:** Offers a wide range of tools for agents, enabling interactions with:
    *   **Cloud Providers:** AWS, GCP
    *   **Monitoring & Observability:** Datadog (metrics, logs, traces, events), Prometheus, Loki, Elasticsearch
    *   **Databases:** ClickHouse, MySQL, PostgreSQL, Redis
    *   **Version Control & CI/CD:** GitHub, Helm
    *   **Container Orchestration:** Kubernetes (kubectl)
    *   **Messaging:** RabbitMQ
    *   **Web Interaction:** Web crawling, interacting with HTTP APIs
    *   **Internal Services:** Nudgebee services like Cloud Collector, Workflows/Autopilot.
*   **Conversational AI:** APIs for managing and participating in conversations powered by LLMs.
*   **Retrieval Augmented Generation (RAG):** Endpoints and capabilities for RAG, enhancing LLM responses with external knowledge.
*   **Event Processing:** Asynchronous event analysis and troubleshooting capabilities, often utilizing RabbitMQ.
*   **Secure and Configurable:** Token-based authentication for APIs and extensive configuration options via environment variables or a `.env` file.
*   **Observability:** Instrumented with OpenTelemetry for tracing and metrics.

## Agent Architecture

The server employs a sophisticated hierarchical agent architecture, allowing for complex, multi-layered reasoning and task execution.

*   **ReAct3 Planner:** A single planner drives every non-tool/non-custom/non-classification agent — the iterative think → act → observe → think loop, with parallel action batches when steps are independent. Lives in `agents/core/planner_react_3.go`.

*   **Agent-Declared Intent, Not Implementation:** Each agent declares a planner *type* — `Orchestrating` for top-level "manager" agents, `ReAct` for task executors. The type expresses intent (does this agent orchestrate multi-tool work?); the executor picks the implementation (ReAct3). This mirrors the `ModelTier` pattern where an agent asks for `reasoning` or `retrieval` and config decides the model.

*   **Hierarchical Structure:** Agents can be used as tools by other agents:
    1.  A top-level **Orchestrating agent** (e.g. `k8s_debug`, `aws_debug`) receives a user query.
    2.  Its ReAct3 loop calls specialized **sub-agents** (also ReAct3) as tools.
    3.  Sub-agents execute primitive tools (shell commands, cloud APIs).

*   **Accuracy & Reliability Features:**
    *   **Notebook Discipline:** ReAct3 maintains a per-message notebook of hypotheses with resolved status (SUPPORTED / REFUTED / INCONCLUSIVE) and evidence chain. Drives hypothesis-first RCA over guess-first pattern-matching.
    *   **Answer Critique:** For top-level investigation queries, a critiquer LLM reviews the final answer before returning. Rejects shallow / status-only / manual-instruction responses. Sub-agents are exempt (avoid slowing them down).
    *   **Failure Reflection:** On tool failure the agent reflects on the error and picks a different approach, not a blind retry.
    *   **Dynamic Few-Shot Prompting:** RAG pulls relevant examples from a memory store to seed the planner.


### Agent Types

*   **Orchestrating Agents** (`AgentPlannerTypeOrchestrating`):
    *   Top-level coordinators — `k8s_debug`, `aws_debug`, `gcp_debug`, `azure_debug`, `datadog_debug`.
    *   Delegate to specialized sub-agents via ReAct3.
    *   Persisted as `"orchestrating"` in `llm_agents.executor_type` (migration V777). The legacy `"rewoo"` value is still dual-read via `ParseExecutorType` for rows written before the migration.

*   **ReAct Agents** (`AgentPlannerTypeReAct`):
    *   Task-focused executors — `prometheus`, `postgres`, `promql`, `traces`, `elastic`, etc.
    *   Same ReAct3 runtime as Orchestrating agents.

*   **Tool Agents** (`AgentPlannerTypeTool`): single-shot tool-call agents; no iteration loop.

*   **Custom Agents** (`AgentPlannerTypeCustom`): implement their own `Execute()` and never touch a planner. User-defined agents fall here.

*   **Classification / Conversational Agents:** specialized loops for classification and open-ended dialogue.


## Planner Prompt Structure & LLM Caching

See [docs/caching.md](docs/caching.md) for the full documentation on:
- Cache scopes (Global, Account, Conversation) and TTLs
- ReAct3 planner message layout (system vs human messages)
- Rules for where to place new prompt content
- How to declare cache scope for new agents
- Google AI provider system message handling

## Core Components

*   **`cmd/`**: Contains the main application entry point (`main.go`).
*   **`api/`**: Defines the external HTTP API endpoints for interacting with the server (e.g., conversations, agents, tools).
*   **`config/`**: Manages service configuration, including LLM provider details, service tokens, database connections, and tool settings.
*   **`llms/`**: Houses the client integrations for various LLM providers (e.g., Bedrock, Azure, OpenAI, Sagemaker, HuggingFace).
*   **`agents/`**: Implements different specialized agents designed to perform specific tasks or roles using LLMs and tools.
*   **`tools/`**: Provides the implementations for the diverse toolkit available to agents.
*   **`relay/`**: Likely manages communication or proxying to a "Relay Server" for certain operations.
*   **`workflows/`**: Contains logic related to interacting with an external workflow or automation service.
*   **`common/`**: Shared utilities, including MQ handling and schedulers for background tasks.

## Getting Started

### Prerequisites

*   Go (version specified in `go.mod`).
*   Access to a PostgreSQL database (for storing server data).
*   Access to a RabbitMQ instance (for event processing).
*   API keys and credentials for any LLM providers you intend to use (e.g., AWS credentials for Bedrock, Azure API key for Azure OpenAI).
*   Credentials for any external services the tools will interact with (e.g., Datadog API keys, GitHub tokens).

### Installation

1.  **Clone the repository:**
    ```bash
    git clone <repository-url>
    cd <path-to-llm/llm-server>
    ```

2.  **Install dependencies:**
    The `Makefile` likely provides a way to install dependencies (e.g., `make install` or `make deps`). Typically, `go mod download` or `go mod tidy` will fetch necessary packages.

### Configuration

The service is configured using a `.env` file in the `llm/llm-server` directory and/or environment variables. Refer to `config/config.go` for a comprehensive list of all available configuration options.

Key configuration areas to pay attention to:

*   **Server Authentication:**
    *   `LLM_SERVER_TOKEN_HEADER`: HTTP header for the server's auth token.
    *   `LLM_SERVER_TOKEN`: The secret token for accessing the LLM server API.
*   **Database:**
    *   `LLM_SERVER_DB_URL`: Connection string for the PostgreSQL database.
*   **LLM Provider(s):**
    *   `LLM_PROVIDER`: Primary LLM provider (e.g., `bedrock`, `azure`, `openai`).
    *   `LLM_MODEL_NAME`: Default model to use.
    *   `LLM_PROVIDER_API_KEY`, `LLM_PROVIDER_API_ENDPOINT`, `LLM_PROVIDER_REGION`, etc., for your chosen provider(s).
    *   Specific configurations for PromQL/LogQL SLMs if used.
*   **RabbitMQ:**
    *   `RABBIT_MQ_USERNAME`, `RABBIT_MQ_PASSWORD`, `RABBIT_MQ_HOST`, `RABBIT_MQ_PORT`.
    *   Queue and exchange names like `RABBIT_MQ_TROUBLESHOOT_EXCHANGE`.
*   **External Service Endpoints:**
    *   `SERVICE_API_SERVER_URL` (Nudgebee services)
    *   `RAG_SERVER_URL`
    *   `CLOUD_COLLECTOR_SERVER_URL`
*   **Agent & Tool Behavior:**
    *   `LLM_SERVER_AGENT_REACT_MAX_ITERATIONS`, `LLM_SERVER_AGENT_MAX_LOGLINES`, etc.

Create a `.env` file with your specific settings:
```env
# Example .env file
LLM_SERVER_TOKEN=your_llm_server_secret_token
LLM_SERVER_DB_URL=postgresql://user:pass@host:port/dbname?sslmode=disable

LLM_PROVIDER=bedrock
LLM_MODEL_NAME=anthropic.claude-3-sonnet-20240229-v1:0
AWS_REGION=us-east-1 # Ensure this or LLM_PROVIDER_REGION is set for Bedrock

RABBIT_MQ_HOST=localhost
RABBIT_MQ_USERNAME=guest
RABBIT_MQ_PASSWORD=guest

# ... other essential configurations based on your setup
```

### Running the Service

The `Makefile` provides targets to run the service:
```bash
make run
```
This will typically build and start the LLM server, listening on the port specified in the configuration (default: 8000).

## API Endpoints

The LLM Server exposes a rich set of API endpoints, all requiring token authentication (via the header specified in `LLM_SERVER_TOKEN_HEADER`). Refer to the handler functions in the `api/` directory for detailed request/response formats.

High-level API groups include:

*   `GET /health`: Public health check endpoint.
*   Conversation APIs (e.g., creating conversations, posting messages).
*   Agent APIs (e.g., listing agents, invoking agents).
*   Tool APIs (e.g., listing tools, executing tools directly).
*   Analysis APIs (likely for triggering or retrieving results from event analysis).
*   RAG APIs (for interacting with the Retrieval Augmented Generation capabilities).
*   Internal sync APIs (e.g., `handleSyncConversationStatusApi`).

## Makefile Targets

The `Makefile` in `llm/llm-server/` provides several useful commands:

*   `make run`: Runs the service using `go run ./cmd`.
*   `make fmt`: Formats source code using `go fmt ./...`.
*   `make test`: Runs unit tests (including `go mod download`) and generates a coverage report.
*   `make benchmark`: Runs Go benchmarks.
*   `make lint`: Runs static code analysis using `golangci-lint run`.
*   `make validate`: Runs `lint` and `test` targets.
*   `make build`: Builds the service binary (`llm-server`), ensuring `validate` passes first.
*   `make install`: Builds the service and copies the binary to `~/go/bin/nudgebee-llm-server`.

## Contributing

Contributions to the LLM Server are welcome! Please follow these general guidelines:

1.  **Fork the repository.**
2.  **Create a new branch** for your feature or bug fix.
3.  **Make your changes.**
    *   Adhere to existing coding standards and patterns.
    *   Write unit tests for new functionality or bug fixes.
    *   Ensure all tests and linters pass (e.g., by running `make validate`).
4.  **Commit your changes** with clear, descriptive messages.
5.  **Push to your fork.**
6.  **Create a Pull Request** against the main repository, detailing your changes.

---

## Development Context

### Project Overview
The LLM Server uses a modular design for APIs (`api/`), agents (`agents/`), tools (`tools/`), and LLM clients (`llms/`).

### Development Conventions
- **Hierarchical Agent Architecture**:
  1. **Orchestrating Agents**: Top-level coordinators (`k8s_debug`, `aws_debug`, etc.). Declare `AgentPlannerTypeOrchestrating`; run under ReAct3 at runtime.
  2. **ReAct Agents**: Task-focused executors (`prometheus`, `postgres`, etc.). Declare `AgentPlannerTypeReAct`; also run under ReAct3.
  A single planner (`planner_react_3.go`) drives both. The declared type expresses intent, not implementation.
- **Accuracy & Reliability Features**:
  - **Notebook Discipline**: ReAct3 maintains a per-message hypothesis notebook with resolved status; drives hypothesis-first RCA.
  - **Answer Critique**: Top-level investigation queries get an LLM critique of the final answer before it's returned. Sub-agents and non-investigation queries are exempt.
  - **Failure Reflection**: Agents reflect on tool errors and try alternative approaches instead of blind retry.
  - **RAG-based Prompting**: Dynamically retrieves context-aware examples for planners.
- **Agent Development**: 
  1. Create a file in `agents/` (e.g., `agent_my_agent.go`).
  2. Implement the `NBAgent` interface.
  3. Register via `core.RegisterNBAgentFactoryAndTool` in `init()`.
- **Tool Development**: 
  1. Create a file in `tools/` (e.g., `tool_my_tool.go`).
  2. Implement the `NBTool` interface.
  3. Register via `toolcore.RegisterNBTool` in `init()`.
- **Coding Style**: Follow standard Go formatting (`make fmt`).
- **Testing**: Use `testify/assert`. All new features and bug fixes require unit tests.
```
