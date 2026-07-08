# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

The LLM Server is a Go-based orchestration service that manages Large Language Model operations, autonomous AI agents, and extensive integrations with cloud services, observability platforms, and databases. It serves as the central intelligence hub for LLM-powered troubleshooting, diagnostics, and automation.

## Build & Development Commands

```bash
make run          # Run the server locally
make lint         # Run linting (must pass before build)
make test         # Run tests with coverage + race detection
make benchmark    # Run benchmarks
make validate     # Lint + test (required before build)
make build        # Build binary (runs validate first)
make install      # Build and install to ~/go/bin
```

### Running Specific Tests

```bash
go test -v -run TestName ./path/to/package   # Single test
go test -v ./agents/...                       # Package tests
go test -race ./...                           # With race detection
```

## Local Development

### Required Services

The LLM Server depends on services running in Kubernetes that must be port-forwarded to localhost:

| Service | Local Port | Port-Forward Command |
|---------|-----------|---------------------|
| cloud-collector-server | 8000 | `kubectl port-forward -n nudgebee svc/cloud-collector-server 8000:8000` |
| services-server (api-server) | 8120 | `kubectl port-forward -n nudgebee svc/api-server 8120:8000` |
| rag-server | 8700 | `kubectl port-forward -n nudgebee svc/rag-server 8700:8700` |
| relay-server | 8110 | `kubectl port-forward -n nudgebee svc/relay-server 8110:8110` |

Verify with: `curl http://localhost:<port>/health` for each service.

### Environment Configuration

The `.env` file at the repository root configures local development. See `.env.example` for reference.

Required environment variables: `PORT` (default 9999), `LOG_LEVEL` (use `debug`), `SERVICE_API_SERVER_URL`, `RELAY_SERVER_ENDPOINT`, `RAG_SERVER_URL`, `CLOUD_COLLECTOR_SERVER_URL`, `LLM_SERVER_DB_URL` (PostgreSQL), `ACTION_API_SERVER_TOKEN`, `RELAY_SERVER_SECRET_KEY`, `LLM_PROVIDER` (bedrock/azure/openai/googleai), `LLM_MODEL_NAME`, `LLM_PROVIDER_API_KEY`.

### Testing API Endpoints Locally

```bash
curl http://localhost:9999/health                                          # Health check (no auth)
curl -H "Authorization: <token>" http://localhost:9999/agents              # List agents
curl -X POST http://localhost:9999/agent/invoke \
     -H "Authorization: <token>" -H "Content-Type: application/json" \
     -d '{"agent":"k8s_debug","query":"Check pod status","accountId":"<id>"}'
```

### Troubleshooting

- **"connection refused"** — ensure all dependent services are port-forwarded
- **Database errors** — verify `LLM_SERVER_DB_URL`; may need `kubectl port-forward svc/postgres 5433:5432`
- **"LLM provider not configured"** — set provider env vars in `.env`
- **Port 9999 in use** — change `PORT` or kill: `lsof -ti:9999 | xargs kill -9`

### VS Code Debugging

Debug config at `.vscode/launch.json`. Open VS Code at repo root, select "LLM Server" from Debug panel, press F5.

## Code Style & Conventions

### Error Handling

Always wrap errors with context using `fmt.Errorf` and `%w`. Never bare `return err`.

```go
// Correct
return "", fmt.Errorf("GetTenantIdFromAccountId: failed to get database manager: %w", err)

// Wrong
return "", err
```

Sentinel errors live in `agents/core/errors.go`. Combine via `errors.Join()` or `fmt.Errorf("%w: %s", sentinel, detail)`. Custom HTTP error types in `common/errors.go`.

### Logging

`log/slog` (Go stdlib) exclusively. JSON handler configured in `cmd/main.go`.

```go
slog.Info("worker: started", "pool", name, "num_workers", numWorkers)
slog.Error("budget: error checking tenant daily cost", "error", err, "tenant_id", tenantId)
```

In business logic, use `ctx.GetLogger()` from `*security.RequestContext` — it auto-attaches `trace_id`, `span_id`, `conversation_id`, `agent_id`, file, and line. Use `slog.With("account_id", id)` for enrichment.

### Naming

- Files: `agent_<descriptor>.go`, `tool_<descriptor>.go` — always lowercase snake_case
- Go code: standard Go conventions — PascalCase exports, camelCase private
- One exception: `agent_tickets_V2.go` (uppercase V)

### Import Ordering

Not strictly enforced (no `.golangci.yml` config). Follow stdlib → external → internal when adding new files.

### Context Propagation

- `*security.RequestContext` is always the first parameter (wraps `context.Context`, `*SecurityContext`, `*slog.Logger`, `trace.Tracer`, `metric.Meter`)
- Check `ctx.Done()` in worker pool submissions and `select` statements
- Feature flags via `context.WithValue(ctx, ContextKeyUseLiteModel, true)`
- Background tasks: `context.WithTimeout(context.Background(), ...)` with deferred cancel

## Project Architecture

### Package Layout

- **`cmd/`** — application entry point, server initialization
- **`api/`** — HTTP API handlers (conversations, agents, tools, RAG, events)
- **`agents/`** — autonomous agent implementations (190+ agent files)
- **`agents/core/`** — agent framework: planner, executor, critiquer logic
- **`agents/prompts_repo/`** — all system prompts (Go-embedded via `svc.go`)
- **`tools/`** — tool implementations for external system integrations
- **`llms/`** — LLM provider clients (Bedrock, Azure, OpenAI, Vertex AI, etc.)
- **`config/`** — service configuration management
- **`common/`** — shared utilities, MQ handling, schedulers, worker pools
- **`security/`** — authentication, authorization, RequestContext. Outbound LLM payload inspection (credential gate, future redact / tokenize) is the egressfilter package — see [docs/llm-egress-filter.md](docs/llm-egress-filter.md).
- **`workflows/`** — workflow/automation service integration
- **`relay/`** — relay server communication for Kubernetes operations

Import graph is clean: `agents/core/` → `tools/core/` (one-way). No circular dependencies.

### Agent Architecture

Two-tier system: **Orchestrating** agents (declared type `AgentPlannerTypeOrchestrating`) handle top-level orchestration; **ReAct** agents handle task execution. Both are executed by the ReAct3 planner (iterative reasoning with parallel action support) — the declared type is intent (does this agent orchestrate multi-tool work?), not implementation. See **Execution Flow** below.

### Agent Registration Pattern

```go
// In agents/agent_<n>.go
func init() {
    core.RegisterNBAgentFactory("<agent_name>", func(accountId string) (core.NBAgent, error) {
        return &MyAgent{accountId: accountId}, nil
    })
}

// Implement NBAgent interface
func (a *MyAgent) GetName() string                    { return "<agent_name>" }
func (a *MyAgent) GetDescription() string             { return "..." }
func (a *MyAgent) GetPlannerType() core.AgentPlannerType {
    return core.AgentPlannerTypeReAct // or AgentPlannerTypeOrchestrating
}
func (a *MyAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool { ... }
func (a *MyAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt { ... }
```

### Tool Registration Pattern

```go
// In tools/tool_<n>.go
func init() {
    toolcore.RegisterNBTool("<tool_name>", func(accountId string) toolcore.NBTool {
        return &MyTool{accountId: accountId}
    })
}

func (t *MyTool) Name() string        { return "<tool_name>" }
func (t *MyTool) Description() string { return "..." }
func (t *MyTool) Call(ctx context.Context, input string) (string, error) { ... }
```

## Database

### Access Pattern

SQLX with raw parameterized SQL (`$1`, `$2`). No ORM, no query builder. All queries are hand-written.

```go
query := `INSERT INTO llm_conversations (...) VALUES ($1, $2, ...)
ON CONFLICT (session_id, user_id, account_id) DO UPDATE SET ... RETURNING id`
err := dbManager.Db.QueryRow(query, id, sessionID, ...).Scan(&lastId)
```

Transaction pattern: `dbms.DoInTransaction(func(tx *sqlx.Tx) (any, error) {...})`. Use `sqlx.In()` for IN clauses.

### Schema (key tables)

```
llm_conversations (id PK)
  ├── llm_conversation_messages (conversation_id FK)
  │     ├── llm_conversation_agent (message_id FK, conversation_id FK)
  │     │     └── llm_conversation_tool_calls (agent_id FK, message_id FK, conversation_id FK)
  │     └── llm_conversation_references (message_id FK, agent_id FK)
  ├── llm_conversation_memory (conversation_id FK)
  └── llm_conversation_token_usage (conversation_id FK, message_id FK, agent_id FK)
```

Supporting tables: `llm_functions`, `llm_knowledgebases`, `llm_budget_config`, `llm_model_pricing`.

### Migrations

Migrations are managed by golang-migrate. Files live in `api-server/migrations/migrations/app/` and are applied by the migrations Helm job on deploy. The LLM server never runs migrations — it reads/writes to the schema produced by the migration tree.

## Prompt Engineering

### File Format & Loading

Plain `.txt` files in `agents/prompts_repo/`. Loaded via Go `//go:embed` in `svc.go` (39 files total). Access via `prompts_repo.GetPrompt()`.

### Template Syntax (5 systems in use)

1. **Go template variables:** `{{.tool_descriptions}}`, `{{.history}}`, `{{.notebook}}`
2. **Identity placeholders:** `{{@assistant_name}}`, `{{@assistant_company}}` — replaced at load time from config
3. **Time macros:** `[[Time:Now]]`, `[[Time:-1h]]`, `[[Time:-15m]]` — processed by `common/time_macros.go`
4. **Conditional blocks:** `{{if .remediation_enabled}}...{{end}}`
5. **Printf substitution:** `fmt.Sprintf(data, args...)` for positional params

### Shared vs Agent-Specific Prompts

Shared (injected into all planner prompts): `context_continuity.txt`, `shared_time_handling_rules.txt`, `shared_data_protection_rules.txt`, `shared_code_analysis_rules.txt`.

Agent-specific: `agent_aws.txt`, `agent_k8s_debug.txt`, etc. — each agent loads its own.

### Prompt Message Structure & Caching

See [docs/caching.md](docs/caching.md) for the ReAct3 planner message layout, cache scope definitions, and rules for where to place new prompt content (system vs human messages).

### Testing & Evaluation

- Eval framework in `agents/core/evaluator.go` produces numeric scores: Correctness, Relevance, Completeness, Helpfulness (0-1)
- A/B testing via `prompts/` package — versioned prompts with account-specific overrides and DB-backed config (1-hour TTL cache)
- Loading priority: experiment config → account override → global DB config → embedded file
- **Prompts must not contain literal "TODO"** — enforced by `TestPromptContent_NoTODOMarkers`

## The `_2` and `_3` Suffixes on Planners

The runtime has exactly one planner: **`planner_react_3.go`** (the ReAct3 planner — iterative reasoning with parallel action execution). The executor routes every non-tool/non-custom/non-classification agent to `NewReActAgent3()`. The older `planner_react_2.go` and `planner_rewoo_2.go` have been deleted; symbols they hosted that ReAct3 still needs live in `planner_react_shared.go`.

For agent files, `_2` / `V2` suffixes mark genuinely versioned agents where both versions still coexist:

| Component | v1 | v2 | Active? |
|-----------|----|----|---------|
| Planner runtime | — | `planner_react_3.go` | Only planner at runtime |
| Tickets | `agent_tickets.go` | `agent_tickets_V2.go` | Both; v2 opt-in via `TicketV2Enabled` |

The domain debug orchestrators — `agent_k8s_debug.go`, `agent_aws_debug.go`, `agent_gcp_debug.go`, `agent_azure_debug.go` — previously carried a vestigial `_2` suffix (they have no v1); it has been dropped. Note `agent_aws.go` is a *distinct* sub-agent (direct CLI), not a v1 of the `aws_debug` orchestrator.

**Rule: new code targets ReAct3. There is no v1/v2 planner choice to make.**

### Deprecated Patterns

- MCP executor type → use MCP integrations instead
- Workflow executor type → use workflow tools instead
- Both emit `slog.Warn("tools: ... executor type is deprecated")` at runtime

## Performance & Concurrency

### Worker Pools

`common/worker.go` — bounded channel-based pool with panic recovery, WaitGroup shutdown, context-aware submission:

```go
ExecutePlannerWorkerPool = common.NewWorkerPool("execute_planner", config.Config.AsyncPlanExecutionWorkerCount, 50)
```

### Parallel Plan Execution

Controlled by `PlannerParallelExecEnabled` + `LLMServerAgentMaxParallel` (they gate ReAct3's parallel action batches). Implementation in `executor_planner.go:737-1050`: builds dependency graph → semaphore limits concurrency → submits nodes with zero pending deps → results via channel → early termination on terminal responses.

### Memory Thresholds

- Per-observation hard cap: `LlmServerScratchpadMaxObservationChars` (default 65536, min 4096). This is a dedicated knob — do **not** confuse it with `LlmConfigAutoSelectionMaxObservationLen` (default 500), which governs the unrelated config auto-selection heuristic and previously double-served as the scratchpad cap (clamping it to the 4096 floor).
- **Compression activation is context-window-gated**, not step-count-gated. Older observations are only compressed once the scratchpad approaches the resolved model window: compression activates at `LlmServerScratchpadCompressionActivationFraction` (default 0.75) of the window, and the scratchpad is hard-capped at 0.90 of the window (`scratchpadBudget` in `scratchpad.go`). When the window can't be resolved, both fall back to the legacy `LlmServerAgentMaxScratchpadChars` char budget.
- Within an active compression pass: the last `recentStepsFullContext` (10) steps keep full observations; older steps get an LLM summary (or `compressObservation` byte truncation with an `[output truncated — N chars]` marker).
- UTF-8 safe truncation: `TruncateHead`, `TruncateMiddle` walk byte boundaries

## Debugging

### Tracing a Request

OpenTelemetry with named spans. Every `RequestContext` carries `trace_id` and `span_id`. Key spans: `Agent:Plan`, `Agent:ToolExecution:<tool_name>`, `Agent:Summarize`. Filter logs by: `trace_id`, `conversation_id`, `message_id`, `agent_id`.

### Key Log Lines to Grep

```
# Plan lifecycle
"plannerexecutor: generating plan"
"plannerexecutor: plan generation complete"
"plannerexecutor: iteration complete"

# Parallel execution
"plannerexecutor: executing actions in parallel"
"plannerexecutor: submitting tool for parallel execution"
"plannerexecutor: parallel tool result received"

# Failures
"plannerexecutor: unable to generate llm contents"
"plannerexecutor: breaking after 2 consecutive failed iterations"
"tool execution time"

# Conditions
"plannerexecutor: condition expression evaluated to false"
"plannerexecutor: LLM condition not met"
```

### Replaying a Failed Run

Re-send the same `conversation_id` to `POST /v2/chat`. The system checks the conversation exists and isn't `IN_PROGRESS`, appends a new `message_id`, runs fresh tool calls, and preserves previous execution history. A termination cache (TTL-based, namespace `message_termination`) prevents duplicate processing of the same `message_id`.

## AI Agent Execution Flow

How a user request flows through the system, from API entry to final response.

```
User (UI/API)
  → LLM Server (Go) — api/chains.go, POST /v1/completions/chat
  → Agent Router — selects agent (aws_debug, k8s_debug, etc.)
  → ReAct3 Planner (planner_react_3.go) — iterative think → act → observe loop,
    with parallel action batches when steps are independent
      → Sub-Agents — e.g. aws, aws_observability (also ReAct3)
          → Tool Execution — aws_execute, kubectl, etc.
              → Relay Server → Workspace Pod — runs actual CLI commands
  → Answer Critiquer (top-level investigation queries only) — quality gate,
    rejects shallow/incomplete answers
  → Response Formatter — markdown, 5-Whys, citations for UI
```

### 1. API Entry (`api/chains.go`)

Request arrives at `POST /v1/completions/chat` via an RPC action handler dispatched by the in-app gateway (`@lib/rpcGateway`). Auth validated via JWT. Conversation created/resumed in `llm_conversations`. If `async: true`, submitted to worker pool and returns HTTP 202 immediately.

### 2. Agent Selection (`api/chains.go` ~line 301)

Explicit (`@aws_debug` in query) or implicit (Router Agent infers via LLM). Agent lookup via `core.GetNBAgent(name, accountId)`. Each agent declares a planner type (`Orchestrating` / `ReAct` / `Tool` / `Custom` / `Classification` / `Conversational`), tools, and system prompt path. Everything except `Tool` / `Custom` / `Classification` runs via ReAct3.

### 3. System Prompt Assembly

Two parts combined: agent-specific prompt (domain expertise, investigation methodology) + ReAct3 base (`planner_react_3_base.txt`, iteration/notebook rules, tool list, time macros).

### 4. ReAct3 Loop

The planner iterates: think → emit one or more `<action>` calls → observe results → think again → … until it emits `<finish>` or hits the iteration cap. Independent actions in the same iteration execute in parallel (gated by `PlannerParallelExecEnabled`).

### 5. Notebook Discipline

ReAct3 maintains an in-conversation notebook (persisted per message) with the model's hypotheses, resolved status, and evidence chain. Kept fresh across turns; drives hypothesis-driven RCA.

### 6. Executor Loop (`agents/core/executor_planner.go`)

Runs each planner iteration: dispatches actions, applies dependency and condition gates, persists results to DB, feeds observations back to the planner for the next iteration.

### 7. Sub-Agent Execution (`agents/core/planner_react_3.go`)

Each action invokes a sub-agent, which runs its own ReAct3 loop with its own tool set. Failures are reflected on and retried with a different approach rather than blindly re-run.

### 8. Tool Execution on Workspace Pod

Security classification (LLM classifies as read/create/update/delete — writes require user confirmation) → workspace manager (reuses or creates pod with injected credentials) → HTTP POST to relay-server → pod runs CLI command → stdout/stderr returned.

### 9. Observation Aggregation (`executor_planner.go` ~line 521)

Each step appends structured observation (`#PlanId`, `#ToolName`, `#Question`, `#Answer`) to execution context. Structural markers are escaped with zero-width characters to prevent prompt injection.

### 10. Solver & Answer Critique

Solver compiles observations into `<final_answer>` or `<missing_information>` (triggers more planning). Critiquer enforces: no status-only updates, no manual CLI instructions, require 5-Whys causality chain, require evidence-based findings, reject symptom-only answers. Rejected → solver regenerates.

### 11. Response Formatting & Delivery

Raw data mode (JSON/YAML code block) or conversational mode (markdown with 5-Whys, citations as `[AWS - E1](#task-E1)`). Results persisted across all `llm_conversation_*` tables. Client polls via GraphQL subscription.

### 12. Background Tasks (post-response)

Title generation, memory extraction (patterns/facts for future context), follow-up suggestion generation.

## Configuration Reference

All configuration in `config/config.go` via environment variables.

**Authentication:** `LLM_SERVER_TOKEN_HEADER`, `LLM_SERVER_TOKEN`
**LLM Provider:** `LLM_PROVIDER`, `LLM_MODEL_NAME`, `LLM_PROVIDER_REGION`, `LLM_PROVIDER_API_KEY`, `LLM_PROVIDER_API_ENDPOINT`
**Database:** `LLM_SERVER_DB_URL` (PostgreSQL)
**RabbitMQ:** `RABBIT_MQ_HOST`, `RABBIT_MQ_USERNAME`, `RABBIT_MQ_PASSWORD`, `RABBIT_MQ_TROUBLESHOOT_EXCHANGE`
**Agent Behavior:** `LLM_SERVER_AGENT_REACT_MAX_ITERATIONS` (default 50), `LLM_SERVER_REACT_CRITIQUE_ENABLED` (default true), `LLM_SERVER_AGENT_MAX_LOGLINES`, `LLM_SERVER_PLANNER_PARALLEL_EXEC_ENABLED` (gates ReAct3 parallel action batches), `LLM_SERVER_AGENT_MAX_PARALLEL` (parallel action concurrency limit)
**External Services:** `SERVICE_API_SERVER_URL`, `RAG_SERVER_URL`, `CLOUD_COLLECTOR_SERVER_URL`, `RELAY_SERVER_ENDPOINT`

## Key Integrations

**Cloud:** AWS, GCP, Azure
**Observability:** Datadog, Prometheus, Loki, Elasticsearch, Chronosphere
**Databases:** ClickHouse, MySQL, PostgreSQL, Redis
**Container Orchestration:** Kubernetes (kubectl), Helm, ArgoCD
**Other:** GitHub, RabbitMQ, Playwright (browser automation)

## Testing Patterns

- Tests co-located with source (`*_test.go`), `testify/assert` for assertions, `go-sqlmock` for DB, hand-written mock structs (no gomock)
- Table-driven tests preferred
- Integration tests gated by env vars (`TEST_ACCOUNT`, `TEST_USER`) with `t.Skip()`
- `agents/core/` is well-tested; `cmd/`, `config/`, `security/`, `audit/` have no tests
- `make test` runs with `-race` flag and generates HTML coverage reports

## Commit Format

Conventional commits with PR references:

```
fix(llm-server): fix Loki OR-clause escaping and improve app label prompt (#27383)
feat(ui): add SolarWinds webhook integration UI (#27380)
chore(deps): bump github.com/gin-contrib/pprof (#27311)
```

## Common Development Tasks

**Adding a new agent:**
1. Create `agents/agent_<n>.go`, implement `NBAgent` interface, register in `init()`
2. Add system prompt to `agents/prompts_repo/`
3. Write tests in `agents/agent_<n>_test.go`

**Adding a new tool:**
1. Create `tools/tool_<n>.go`, implement `NBTool` interface, register in `init()`
2. Handle errors and timeouts
3. Write tests in `tools/tool_<n>_test.go`

**Modifying planner logic:**
- Executor loop: `agents/core/executor_planner.go`
- ReAct3 loop (all runtime planning): `agents/core/planner_react_3.go`

**Adding an LLM provider:**
1. Create client in `llms/<provider>/`, implement provider interface
2. Add config to `config/config.go`, update provider selection logic

## Rules & Guardrails

- **Always run `make lint` after code changes.** CI will reject unlinted code.
- **Always wrap errors** with `fmt.Errorf("context: %w", err)`. Never bare `return err`.
- **Use `ctx.GetLogger()`** for logging in business logic, not raw `slog` calls.
- **Do not modify files in `agents/prompts_repo/` without explicit instruction.** Prompt changes affect all agents and require careful testing.
- **Do not change core planner logic** (`executor_planner.go`, `planner_react_3.go`) for agent-specific bugs. Fix at the agent level first.
- **Never hardcode credentials, account IDs, or API keys.** Use `config/config.go` and environment variables.
- **Never log sensitive data** (tokens, credentials, PII).
- **Agents must be stateless** between invocations. No shared mutable state.
- **Tools must be idempotent** where possible. Error messages should be actionable for the LLM.
- **Always use `*security.RequestContext`** as the first parameter in business logic functions.
- **Use structured output (JSON)** for complex tool responses.
- **New agents/planners must use v2 patterns.** Never create v1 variants.
- **Prompts must not contain literal "TODO"** — enforced by test.
- **Write raw parameterized SQL** (`$1`, `$2`) — no ORM exists. Follow existing SQLX patterns.
- **Use conventional commits** with scope: `fix(llm-server):`, `feat(llm-server):`, `chore(deps):`.

## CI/CD

- GitHub Actions (workflows in parent repo)
- Docker multi-stage builds (see `Dockerfile`)
- Images pushed to AWS ECR, deployed to Kubernetes via Helm charts

## Key Dependencies

`gin` (HTTP), `langchaingo` (LLM), `aws-sdk-go-v2` (AWS), `client-go` (K8s), `otel` (OpenTelemetry), `playwright-go` (browser), `go-rabbitmq` (MQ), `sqlx` (database).