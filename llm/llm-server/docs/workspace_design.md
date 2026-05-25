# Workspace Shell Environment — Architecture Design

## Overview

The workspace feature provides dedicated, per-account shell environments for LLM conversations. Each workspace is a Kubernetes pod running the `code-analysis-agent` in server mode, enabling LLM agents to execute shell commands, manage files, and interact with infrastructure tools (kubectl, helm, psql, etc.) in an isolated environment.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                        LLM Server                           │
│                                                             │
│  ┌──────────┐   ┌────────────────┐   ┌──────────────────┐  │
│  │ ShellTool │──▶│WorkspaceManager│──▶│ Workspace Proxy  │  │
│  │(tool_shell)│  │  (workspace.go)│   │  API (files,     │  │
│  └──────────┘   └───────┬────────┘   │  execute)        │  │
│                         │            └──────────────────┘  │
│                         │                                   │
│  ┌──────────────────────┴──────────────────────────┐       │
│  │              K8s API / Pod Management            │       │
│  │  - Create/Delete pods                           │       │
│  │  - Direct IP execution (fast path)              │       │
│  │  - K8s proxy fallback (local dev)               │       │
│  └──────────────────────┬──────────────────────────┘       │
└─────────────────────────┼───────────────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────────────┐
│              Workspace Pod (per account)                     │
│              workspace-{accountId}                           │
│                                                             │
│  ┌──────────────────────────────────────────────────────┐  │
│  │           code-analysis-agent --server                │  │
│  │                                                       │  │
│  │  /execute  ─▶  ExecutionHandler  ─▶  sh -c {cmd}     │  │
│  │  /api/v1/files/* ─▶ FileHandler                      │  │
│  │  /analyze  ─▶  AgenticAnalyzeHandler (ReAct planner) │  │
│  │  /health   ─▶  readiness probe                       │  │
│  │                                                       │  │
│  │  ┌─────────────────────────────────────────────┐     │  │
│  │  │  Shim Binaries (symlinks → shim binary)     │     │  │
│  │  │  kubectl, helm, psql, mysql, redis-cli,     │     │  │
│  │  │  argocd, clickhouse-client, rabbitmqadmin   │     │  │
│  │  │       │                                     │     │  │
│  │  │       ▼                                     │     │  │
│  │  │  POST /api/v1/workspace/execute             │     │  │
│  │  │  (proxied back to llm-server)               │     │  │
│  │  └──────────────┬──────────────────────────────┘     │  │
│  └─────────────────┼────────────────────────────────────┘  │
└────────────────────┼────────────────────────────────────────┘
                     │
                     ▼
┌─────────────────────────────────────────────────────────────┐
│                    Relay Server                              │
│  /workspace/execute ─▶ JWT validation ─▶ RabbitMQ RPC      │
│                                           ─▶ K8s Agent      │
│                                              (runs in       │
│                                               customer      │
│                                               cluster)      │
└─────────────────────────────────────────────────────────────┘
```

## Components

### 1. Workspace Manager (`workspace/workspace.go`)

Central orchestrator for workspace pod lifecycle.

| Method | Purpose |
|--------|---------|
| `CreateWorkspace` | Creates K8s pod with JWT token, LLM credentials, resource limits |
| `TerminateWorkspace` | Deletes pod, revokes JWT token |
| `WaitForReady` | Polls pod readiness (2s interval, 5min timeout) |
| `ExecuteCommand` | Sends command to pod via direct IP or K8s proxy |
| `ExecuteOrLazyCreate` | Optimistic execution with auto-recovery on infrastructure errors |
| `CallAPI` / `CallAPIOrLazyCreate` | Generic HTTP calls to workspace pod endpoints |
| `ListFiles` / `ReadFile` / `SaveFile` / `DeleteFile` | File operations via workspace pod |
| `CleanupStaleWorkspaces` | Startup cleanup of pods running outdated images |

**Pod naming**: `workspace-{lowercase(accountId)}`
**Namespace**: Configured via `LlmServerCodeAgentNamespace`
**Labels**: `app=workspace`, `account_id={id}`

### 2. Shell Tool (`tools/tool_shell.go`)

LLM-facing tool registered as `shell_execute`. Provides shell access to agents.

**Key behaviors:**
- Auto-sources `.nb_profile` for environment persistence across stateless commands
- Injects cloud credentials (AWS/GCP/Azure) based on account type
- Scrubs credential values from command output
- Feature-gated via `LlmServerShellToolEnabled`
- Auto-injected into all agent tool lists when enabled

### 3. Workspace Proxy API (`api/workspace_proxy.go`)

REST API exposing workspace operations to the frontend (via RPC actions through `@lib/rpcGateway`) and workspace pods (via shims).

**Routes:**
- `POST /api/v1/workspace/files` — Unified file operations (list, get, save, delete, batch-read)
- `POST /api/v1/workspace/execute` — Tool execution (kubectl, helm, psql, mysql, redis, argocd, clickhouse, rabbitmq, mssql, oracle, ssh)

**Auth:** JWT workspace token (`X-Workspace-Token`) or global action token (`X-ACTION-TOKEN`)

### 4. Shim Binary (`llm/code-analysis/cmd/shim/main.go`)

Lightweight proxy binary symlinked as `kubectl`, `helm`, `psql`, etc. inside workspace pods. When an LLM agent or code-analysis tool runs `kubectl get pods`, the shim:
1. Detects it was invoked as `kubectl`
2. Constructs the full command with proper shell quoting
3. POSTs to llm-server's `/api/v1/workspace/execute` with the JWT token
4. llm-server routes to relay-server, which dispatches to the customer's K8s agent

This pattern ensures the workspace pod never holds database/infrastructure credentials directly.

### 5. Execution Handler (`llm/code-analysis/api/handlers/execution_handler.go`)

Runs inside the workspace pod. Receives commands from the workspace manager.

**Key behaviors:**
- Validates conversation ID format (alphanumeric + `_-` only)
- Creates per-conversation working directories (`baseDir/{conversationId}`)
- Path traversal prevention (ensures workDir stays within baseDir)
- Clean environment (does not inherit host env, provides explicit PATH)
- Command validation blocklist
- Output capped at 1MB to prevent OOM
- Timeout from server config minus 2s buffer

### 6. Relay Server Workspace Handler (`collector-server/k8s-collector/relay-server/pkg/server/handlers/workspace.go`)

Handles `POST /workspace/execute` from workspace pod shims. Validates JWT, resolves integration configs from DB, builds relay action, dispatches via RabbitMQ to the customer's K8s agent.

## Execution Flows

### Shell Command (LLM Agent → Workspace Pod)

```
Agent calls shell_execute tool
  → ShellTool.Call()
    → Inject cloud credentials into env
    → Prepend ".nb_profile" sourcing
    → wm.ExecuteOrLazyCreate(accountId, conversationId, command, env)
      → Try ExecuteCommand (direct HTTP to pod IP)
        → POST http://{podIP}:8080/execute
        → ExecutionHandler validates and runs: sh -c {command}
      → On recoverable error:
        → CreateWorkspace → WaitForReady → Retry ExecuteCommand
    → ScrubCredentials from output
    → Return result to agent
```

### Infrastructure Tool (Workspace Pod → Customer Cluster)

```
Code-analysis agent runs "kubectl get pods"
  → Shim binary detects invocation as "kubectl"
  → POST to llm-server /api/v1/workspace/execute
    → JWT validation
    → Maps "kubectl" → RelayJobKubectl
    → tools.ExecuteContainerJob via relay server
      → RabbitMQ RPC to customer K8s agent
      → Agent runs kubectl in customer cluster
      → Response returned through chain
```

## Security Model

### Authentication
- **Workspace JWT**: HS256-signed, contains `account_id` and `tenant_id`
- **Validation**: Signature, expiry, revocation cache, account match
- **Two auth paths**: Workspace token (`X-Workspace-Token`) or global server token

### Pod Isolation
- **Per-account pods**: Each account gets a dedicated pod
- **Non-root execution**: UID 1000, GID 3000, `runAsNonRoot: true`
- **Conversation directory isolation**: Per-conversation working directories
- **Network isolation**: Helm-managed `NetworkPolicy` (`deploy/kubernetes/llm-server/templates/workspace-networkpolicy.yaml`) restricts workspace pod traffic to the minimum required set — ingress only from llm-server, egress only to DNS, llm-server, relay-server, and the public internet with link-local (IMDS), RFC1918, CGNAT, and loopback CIDRs blocked. Configurable via `workspaceNetworkPolicy.*` in `values.yaml`. Requires NetworkPolicy enforcement on the target cluster (GKE: `--enable-network-policy` or Dataplane V2).

### Command Safety
- Blocklist-based command validation (see [#27731](https://github.com/nudgebee/nudgebee/issues/27731) for limitations)
- Output truncation (1MB cap in execution handler, 4KB in CLI tool)
- Credential scrubbing on shell output

### Credential Management
- Infrastructure credentials injected via shim pattern (never stored in workspace pod)
- Cloud credentials injected as env vars per command (scrubbed from output)
- LLM credentials injected from K8s secrets

## Configuration

| Variable | Type | Purpose |
|----------|------|---------|
| `LLM_SERVER_WORKSPACE_ENABLED` | bool | Feature toggle |
| `LLM_SERVER_CODE_AGENT_IMAGE` | string | Container image for workspace pods |
| `LLM_SERVER_CODE_AGENT_NAMESPACE` | string | K8s namespace for workspace pods |
| `LLM_SERVER_CODE_AGENT_SECRET` | string | K8s secret name for LLM credentials |
| `LLM_SERVER_CODE_AGENT_IMAGE_PULL_SECRET` | string | K8s imagePullSecret name |
| `LLM_SERVER_WORKSPACE_PORT` | int | Workspace pod HTTP port (default: 8080) |
| `LLM_SERVER_WORKSPACE_LOCAL_URL` | string | Local workspace URL (dev mode bypass) |
| `LLM_SERVER_WORKSPACE_RESOURCE_REQUEST_CPU` | string | K8s CPU request |
| `LLM_SERVER_WORKSPACE_RESOURCE_REQUEST_MEMORY` | string | K8s memory request |
| `LLM_SERVER_WORKSPACE_RESOURCE_LIMIT_CPU` | string | K8s CPU limit (optional) |
| `LLM_SERVER_WORKSPACE_RESOURCE_LIMIT_MEMORY` | string | K8s memory limit (optional) |
| `LLM_SERVER_SHELL_TOOL_ENABLED` | bool | Enable shell tool for agents |
| `LLM_SERVER_JWT_SECRET` | string | JWT signing key for workspace tokens |

**Helm values (NetworkPolicy):** the workspace pod `NetworkPolicy` is configured in `deploy/kubernetes/llm-server/values.yaml` under `workspaceNetworkPolicy.*` — see that file for `enabled`, `workspacePort`, `llmServerPort`, `relayServerName`/`Port`, `allowExternalEgress`, `externalEgressExcept`, `dnsNamespaceSelector`/`dnsPodSelector`, and `extraEgress`.

## Known Limitations & Tracked Issues

### Security
- [#27731](https://github.com/nudgebee/nudgebee/issues/27731) — Command validation blocklist is bypassable
- [#27733](https://github.com/nudgebee/nudgebee/issues/27733) — JWT token lifetime (365d) and in-memory-only revocation
- [#27734](https://github.com/nudgebee/nudgebee/issues/27734) — Missing seccomp, capability drops, read-only rootfs
- [#27735](https://github.com/nudgebee/nudgebee/issues/27735) — Credential exposure via env vars and .nb_profile
- [#27736](https://github.com/nudgebee/nudgebee/issues/27736) — Auth bypass when workspace token env var is empty

### Enhancements
- [#27737](https://github.com/nudgebee/nudgebee/issues/27737) — Per-conversation workspace isolation
- [#27738](https://github.com/nudgebee/nudgebee/issues/27738) — Auto-cleanup idle workspace pods with TTL
- [#27739](https://github.com/nudgebee/nudgebee/issues/27739) — Rate limiting and concurrent execution controls
- [#27740](https://github.com/nudgebee/nudgebee/issues/27740) — Structured audit logging for all shell commands
- [#27741](https://github.com/nudgebee/nudgebee/issues/27741) — Workspace status API and persistent storage option
