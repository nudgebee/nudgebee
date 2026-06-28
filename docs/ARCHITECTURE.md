# Architecture

Nudgebee is a Kubernetes-native monorepo of Go, Python, and TypeScript services for observability-driven AI assistance. The high-level system diagram, the per-service responsibilities, and how requests flow from the dashboard through the backend services to Postgres / RabbitMQ / Qdrant / Temporal all live in the root README:

➜ **[README → Architecture](../README.md#architecture)**

## Per-service deep-dives

For implementation detail on a specific service, read its own `README.md` or `CLAUDE.md`:

- **`app/`** — Next.js dashboard. See [`app/CLAUDE.md`](../app/CLAUDE.md) for component conventions, API layer (`@lib/rpcGateway`), and design-system rules.
- **`api-server/services/`** — Core Go backend (Gin). The Knowledge Graph subsystem is documented at [`api-server/services/knowledge_graph/CLAUDE.md`](../api-server/services/knowledge_graph/CLAUDE.md).
- **`llm/llm-server/`** — LLM agents (ReWOO + ReAct). See [`llm/llm-server/CLAUDE.md`](../llm/llm-server/CLAUDE.md).
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


---



## Notification System Alert flow

### Overview
Nudgebee uses an event-driven architechture for handling notifications trigerred by alerts.

When an alert is created or updated it does not directly send notifications. Instead it emits an event that is consumed by the notification system

---

### Architechture Flow

Alert Service
    ↓
Event Bus
   ↓
Notification Service
   ↓
Delivery Channels (Email/ Webhook/Push)


---

### Event Flow Description

1. An alert is created or updated in the Alert service
2. The system emits an ALERT_TRIGERRED event
3. The Event Bus routes the event to subscribed services.
4. The Notification Service listens for this event
5. Notifications are dispatched through configured channels.

---

### Common Issues

### 1. Missing Event Emission
if notifications are not trigerred verify that alert service emits ALERT TRIGERRED event

### 2. Event Subscription Misconfiguration
Ensure that notification service is properly subscribed to event bus

### 3. Event Name mismatch
The event name must match exactly between emitter and listener


---

Debugging steps

- Check logs for ALERT TRIGERRED event emission
- Verify notification service is running
- Confirm event bus connectivity
- Validate subscription registration in notification module