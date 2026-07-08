# Architecture

Nudgebee is a Kubernetes-native monorepo of Go, Python, and TypeScript services for observability-driven AI assistance. The high-level system diagram, the per-service responsibilities, and how requests flow from the dashboard through the backend services to Postgres / RabbitMQ / Qdrant / Temporal all live in the root README:

➜ **[README → Architecture](../README.md#architecture)**

## Per-service deep-dives

For implementation detail on a specific service, read its own `README.md` or `CLAUDE.md`:

- **`app/`** — Next.js dashboard. See [`app/CLAUDE.md`](../app/CLAUDE.md) for component conventions, API layer (`@lib/rpcGateway`), and design-system rules.
- **`api-server/services/`** — Core Go backend (Gin). The Knowledge Graph subsystem is documented at [`api-server/services/knowledge_graph/CLAUDE.md`](../api-server/services/knowledge_graph/CLAUDE.md).
- **`llm/llm-server/`** — LLM agents (Orchestrating + ReAct, both running the ReAct3 planner). See [`llm/llm-server/CLAUDE.md`](../llm/llm-server/CLAUDE.md).
- **`llm/code-analysis/`** — Log-to-code RCA engine. See [`llm/code-analysis/CLAUDE.md`](../llm/code-analysis/CLAUDE.md).
- **`collector-server/k8s-collector/relay-server/`** — K8s relay gateway. See [`collector-server/k8s-collector/relay-server/CLAUDE.md`](../collector-server/k8s-collector/relay-server/CLAUDE.md).
- **`runbook-server/`** — Temporal-backed runbook orchestration. See [`runbook-server/README.md`](../runbook-server/README.md).

## How a signal flows

1. **Collector** (`k8s-collector`, `cloud-collector`) ingests metrics/events.
2. **`api-server/services`** persists state in Postgres and emits cross-service events via RabbitMQ.
3. **`llm-server` + `rag-server` + `code-analysis`** build investigation context from the event + retrieved evidence.
4. **`runbook-server`** orchestrates remediation workflows in Temporal.
5. **`app`** renders the result via the RPC gateway.

For a richer walkthrough, see the LLM execution-flow section in [`llm/llm-server/CLAUDE.md`](../llm/llm-server/CLAUDE.md#ai-agent-execution-flow).
