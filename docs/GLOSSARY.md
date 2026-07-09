# Glossary

Domain terms used across the Nudgebee codebase and docs. If you see a word in code, an issue, or a PR description and don't know what it refers to, start here.

## Product

- **Nudgebee** — The platform: an open-source SRE copilot that watches Kubernetes clusters and cloud accounts (AWS / Azure / GCP), turns raw signals into ranked findings, and walks operators through investigation and remediation.
- **Nubi** — The AI assistant persona. When you see `nubi`, `nubiIcon`, or `assistantName`, it's referring to the user-facing AI helper. Configurable per tenant via branding.
- **ChatOps** — Slack / Microsoft Teams chatbot surface for SRE workflows: query state, run runbooks, acknowledge alerts, drive investigations.
- **Investigation** — An end-to-end troubleshooting session — a conversation between a user (or autopilot) and the AI, scoped to a specific event / recommendation / topic. Stored in `llm_conversations` table.
- **Triage** — Automated event classification and routing — deciding which signals deserve attention vs. noise.

## Signals & resources

- **Signal** — A raw input the platform ingests: an alert, an event, a metric anomaly, a Kubernetes object change, a cloud-account discovery. Collectors produce signals; the rest of the stack reasons over them.
- **Event** — A discrete actionable signal (typically alert-shaped: a Datadog/PagerDuty/ServiceNow webhook, a Kubernetes warning event, an anomaly detection). Drives investigations and runbooks.
- **Account** — A cloud account (AWS account id / Azure subscription / GCP project) connected to Nudgebee. One tenant can have many accounts.
- **Tenant** — A logical customer / organization boundary. Owns accounts, users, integrations, and conversations. Multi-tenancy is enforced via `tenant_id` columns and the security context.
- **Resource** — A Kubernetes object or cloud asset (pod, deployment, EC2 instance, S3 bucket, etc.) the platform tracks and reasons about.

## Architecture

- **Knowledge Graph (KG)** — The Nudgebee subsystem that models relationships between resources, events, and findings. Lives in `api-server/services/knowledge_graph/`. See its [CLAUDE.md](../api-server/services/knowledge_graph/CLAUDE.md) for deep-dive.
- **Service Map** — Topology graph of service-to-service calls reconstructed from APM / trace data (Datadog, New Relic, OTel). Different artifact than the Knowledge Graph: service-map is dataflow, KG is broader resource state.
- **Relay (Relay Server)** — The K8s gateway service that bridges in-cluster collectors and remediation actions back to the central control plane. Lives in `collector-server/k8s-collector/relay-server/`.
- **Collector** — Any service that ingests signals from an external source. The two main families: `k8s-collector` (in-cluster, watches kube API + metrics) and `cloud-collector` (out-of-cluster, scans AWS/Azure/GCP APIs).
- **Workspace** — A per-tenant Kubernetes pod that the LLM uses to execute tools (`kubectl`, `aws`, etc.) with the tenant's credentials. Spun up on demand by the workspace manager, recycled across requests.

## AI / LLM stack

- **Agent** — An LLM-driven worker that handles a domain (e.g., `k8s_orchestrator`, `aws_orchestrator`, `tickets`). Each agent has a system prompt, a planner type, and a tool set. Lives in `llm/llm-server/agents/`.
- **Tool** — A capability an agent can invoke (e.g., `kubectl`, `aws_execute`, `prometheus_query`, `search_logs`). Implements the `NBTool` interface. Lives in `llm/llm-server/tools/`.
- **Orchestrating agent** — An agent that declares `AgentPlannerTypeOrchestrating` — a top-level "manager" agent (e.g. `k8s_orchestrator`, `aws_orchestrator`) whose job is to coordinate sub-agents across multi-tool investigations. Runs under the ReAct3 planner at runtime. Persisted as `"orchestrating"` in `llm_agents.executor_type` (migration V777); the legacy `"rewoo"` value is still dual-read for pre-migration rows.
- **ReAct / ReAct3** — *Reason-Act-Observe.* The iterative planner: think → call one or more tools (in parallel when independent) → observe → think again → … The single runtime planner for every non-tool / non-custom / non-classification agent, in `llm/llm-server/agents/core/planner_react_3.go`.
- **Tier (LLM tier)** — A category of model use: `reasoning` / `retrieval` / `summary` (and a "blanket" default). Lets the platform route the heavyweight reasoning model to hard reasoning steps while using a cheaper one for summarization.
- **RAG (Retrieval-Augmented Generation)** — Pulling relevant context from a vector store (Qdrant) into the LLM prompt at runtime. Implemented in `llm/rag-server/`.
- **Embedding** — A numeric vector representation of text used by RAG to find semantically similar prior context.
- **Critiquer** — A quality gate that reviews an agent's final answer for top-level investigation queries and rejects shallow / status-only / evidence-free responses, forcing a regenerate.

## Operations & automation

- **Runbook** — An orchestrated remediation procedure — a workflow that runs across services, executes actions, and updates state. Backed by Temporal in `runbook-server/`.
- **Autopilot** — The automation engine. When `auto-pilot` triggers a runbook in response to an event (no human in the loop), the resulting investigation is labelled with `ConversationSource = "Autopilot"`.
- **Recommendation** — A ranked output from analysis: workload right-sizing, cost saving, security fix, etc. Persisted in `recommendations_v2`. Two related verbs: `generate` (produce one) and `apply` (execute the change).
- **Anomaly** — An ML-detected pattern in metrics — typically a deviation from learned baselines. Produces an event when severity crosses a threshold.
- **Rightsizing** — Recommending CPU / memory adjustments for K8s workloads based on observed utilization. ML pipeline in `ml-k8s-server/`.
- **Optimize page** — UI surface showing recommendations across cost / right-sizing / SLO. The `ConversationSource = "Optimize"` tag identifies chats started from there.

## RPC + control plane

- **RPC action** — A single backend operation, defined in `app/src/lib/actions.yaml` and dispatched by `@lib/rpcGateway`. Naming convention is `<module>_<verb>_<description>` — see [CLAUDE.md → RPC action naming convention](../CLAUDE.md#rpc-action-naming-convention).
- **Action token** — `ACTION_API_SERVER_TOKEN` — the shared secret that gates server-to-server RPC traffic. Every backend service that exposes `/rpc/*` enforces it; the edge / Next.js stamps it onto forwarded webhook calls.
- **Security context** — The `RequestContext` object carried through every business call. Holds `tenant_id`, `user_id`, `roles`, `accountIds`, the per-request logger, and the tracing handle. First parameter on most Go functions.

## Branches & environments

- **`main`** — The single development branch in this repo. All contributions target it.

## Where to find more

- **Architecture deep-dive:** [`docs/ARCHITECTURE.md`](./ARCHITECTURE.md) → links to per-service CLAUDE.md files.
- **AI execution flow (agent → ReAct3 → tools → critique → response):** [`llm/llm-server/CLAUDE.md` → AI Agent Execution Flow](../llm/llm-server/CLAUDE.md#ai-agent-execution-flow).
- **RPC actions reference:** [`docs/API.md`](./API.md) and the source-of-truth file [`app/src/lib/actions.yaml`](../app/src/lib/actions.yaml).
