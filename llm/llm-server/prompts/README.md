# Prompt Versioning System

This directory contains all LLM prompts for the system, organized by provider and version.

## Directory Structure

```
prompts/
├── default/                  # Provider-agnostic prompts (active content lives here)
│   └── v1/
│       ├── agents/           # Agent system prompts
│       │   └── k8s_debug.txt
│       ├── planners/         # Reserved for planner prompts
│       ├── tools/            # Reserved for tool prompts
│       └── utilities/        # Reserved for utility prompts
│
├── bedrock/                  # Reserved: AWS Bedrock (Llama) optimizations
├── azure/                    # Reserved: Azure OpenAI (GPT-4) optimizations
├── openai/                   # Reserved: Direct OpenAI optimizations
├── googleai/                 # Reserved: Google Gemini optimizations
├── anthropic/                # Reserved: Direct Anthropic optimizations
│
├── loader.go                 # Embedded FS loader with version + experiment resolution
├── registry.go               # Prompt name constants and promptMapping
├── types.go                  # DB types (experiments, config, metrics)
├── db.go                     # Database queries for config and experiments
├── cache.go                  # In-memory cache layer
└── metrics.go                # OpenTelemetry metrics
```

> Provider directories (bedrock/, azure/, etc.) currently contain only `.gitkeep` files.
> Add prompt files there when provider-specific overrides are needed.

---

## Go Files

| File | Responsibility |
|------|---------------|
| `registry.go` | Prompt name constants (`PromptAgent*`) and `promptMapping` that maps each constant to its filename + category. Entry point for callers: `GetPrompt()`, `RenderPrompt()`, `GetProviderFromConfig()`. |
| `loader.go` | Core loading engine. Embeds the `default/` directory tree via `//go:embed`. Owns the `PromptLoader` struct which chains: cache check → experiment lookup → DB config lookup → hardcoded default → `loadPromptFile()`. Also owns `InitializeGlobalLoader()`, `GetLoader()`, and cache management helpers (`ClearCache`, `ClearCacheForPrompt`, `ClearCacheForAccount`). |
| `types.go` | All shared types: `PromptCategory`, `ConfigSource`, `PromptRequest/Response/Metadata`, `ResolvedConfig`, DB structs (`DBConfig`, `DBExperiment`, `DBAuditLog`, `DBMetrics`), and admin API request/response structs. |
| `db.go` | All PostgreSQL queries. `PromptDB` wraps `common.DatabaseManager` and provides: `GetConfig` (version override lookup with account+provider priority), `GetActiveExperiments` (A/B test targeting), `CreateExperiment` (with overlap detection), `UpsertConfig`, `DisableExperiment`, `UpdateExperimentAccounts`, `CreateAuditLog`, `GetAuditLogs`, `RecordMetrics`, `GetExperimentMetrics`, `IsAvailable` (ping + table existence check). Gracefully degrades — DB errors fall through to embedded defaults. |
| `cache.go` | Thread-safe in-memory TTL cache (`PromptCache`). Cache key is `name:category:provider:accountID`. Supports targeted invalidation by prompt name, by account, or full clear. Background goroutine cleans up expired entries every 5 minutes. Default TTL is 1 hour. |
| `metrics.go` | OpenTelemetry metrics. Registers six counters/histograms under the `nb_llm_*` namespace: total loads, load latency, cache hit/miss, config source distribution, experiment participation, and error count. Recording happens asynchronously (goroutine) so it never blocks prompt loading. |
| `loader_test.go` | Tests for `PromptLoader`: embedded file resolution, fallback path ordering, cache behaviour, and graceful DB-absent operation. |
| `registry_test.go` | Tests for `GetPrompt` and `RenderPrompt`: mapping lookup, template rendering, missing module handling. |

---

## How It Works

### Resolution Priority

When loading a prompt the system tries four paths in order:

1. **Active Experiment** — account-targeted A/B test version (DB)
2. **Database Configuration** — account/provider-specific override (DB)
3. **Hardcoded Default** — falls back to `v1`

### File Path Resolution

For each resolution step the loader tries paths in order:

```
{provider}/{version}/{category}/{name}.txt
default/{version}/{category}/{name}.txt
{provider}/v1/{category}/{name}.txt
default/v1/{category}/{name}.txt      ← always the final fallback
```

### Fallback to Legacy prompts_repo

Agents that have been migrated call `prompts.GetPrompt()` first. If the versioned
loader returns empty (file missing, DB error, etc.) the agent falls back to
`prompts_repo.GetPrompt()` automatically. This makes migration safe and incremental.

---

## Prompt File Format

Files are plain text (`.txt`) with UTF-8 encoding and Unix line endings.
Sections are parsed by `agents/core/prompt_parser.go` into `NBAgentPrompt` fields.

```
# Agent Title

## Role
One-line role description.

## Instructions
- Instruction 1
- Instruction 2

## Constraints
- Constraint 1

## Output Format
Describe expected output format.

## Examples
**Question:** <example question>
**Answer Steps:**
Step 1: ...
Step 2: ...
---
```

All sections are optional except `## Role`. Examples can use any format (XML, JSON,
plain text) — the parser captures everything between the `## Examples` header and EOF.

### Tool Usage (two approaches)

**Option A — Dynamic (from `GetSupportedTools()`):**
Tools are not listed in the txt file. The agent builds `ToolUsage` at runtime from
its registered tools. This is what `k8s_debug` uses.

**Option B — Declared in the txt file (via `## Tool Usage` section):**
Add a `## Tool Usage` section to the txt file. Each tool is a `### tool_name` header;
optional description lines below it override the registered tool description.

```
## Tool Usage

### kubectl
Use for Kubernetes resource inspection and management.

### logs
Use for fetching and analyzing application logs.

### resource_search
```

The parser (`agents/core/prompt_parser.go` → `parseToolUsage`) reads these entries.
If a description line is present it overrides the registered tool description, allowing
agents to give model-specific guidance per tool. If no description is given, the
registered tool description is used as fallback.

Which approach to use is a per-agent decision made in `GetSystemPrompt()`.

---

## Currently Migrated Agents

| Agent | Prompt File | Constant |
|-------|-------------|----------|
| k8s_debug | `default/v1/agents/k8s_debug.txt` | `prompts.PromptAgentK8sDebug` |

---

## Adding a New Agent

### Step 1 — Create the prompt file

```bash
# Strip the "agent_" prefix in the filename (convention)
cp agents/prompts_repo/agent_aws.txt \
   prompts/default/v1/agents/aws.txt
```

### Step 2 — Register in `registry.go`

```go
// Add constant
const PromptAgentAws = "agent_aws"

// Add mapping entry (name must match the .txt filename without extension)
var promptMapping = map[string]struct{ ... }{
    ...
    PromptAgentAws: {"aws", CategoryAgents},
}
```

### Step 3 — Update `GetSystemPrompt()` in the agent file

```go
import (
    "nudgebee/llm/agents/prompts_repo"
    "nudgebee/llm/prompts"
)

func (a *AwsAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
    promptText := prompts.GetPrompt(ctx.GetContext(), prompts.PromptAgentAws, query.AccountId)
    if promptText == "" {
        promptText = prompts_repo.GetPrompt(prompts_repo.PromptAgentAws)
    }

    prompt := core.ParsePromptToNBAgentPrompt(promptText)

    // Option A: load tools dynamically (ignore any ## Tool Usage in the txt file)
    toolUsage := map[string][]string{}
    for _, t := range a.GetSupportedTools(ctx) {
        toolUsage[t.Name()] = []string{t.Description()}
    }
    prompt.ToolUsage = toolUsage

    // Option B: use tools declared in the txt file (prompt.ToolUsage already populated
    // by ParsePromptToNBAgentPrompt — just don't overwrite it here)

    return prompt
}
```

### Step 4 — Build

```bash
cd llm/llm-server
make build
```

---

## Creating a New Prompt Version

### Step 1 — Create the new version file

```bash
cp prompts/default/v1/agents/k8s_debug.txt \
   prompts/default/v2/agents/k8s_debug.txt

# Edit with improvements
vim prompts/default/v2/agents/k8s_debug.txt
git add prompts/default/v2/
git commit -m "feat(prompts): add k8s_debug v2"
```

### Step 2 — Test with an experiment (admin API)

```bash
curl -X POST http://localhost:9999/api/admin/prompts/experiments \
  -H "Authorization: $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "k8s_debug_v2_pilot",
    "prompt_name": "k8s_debug",
    "category": "agents",
    "test_version": "v2",
    "control_version": "v1",
    "target_accounts": ["test-account-id"]
  }'
```

### Step 3 — Promote by setting DB config

```bash
curl -X POST http://localhost:9999/api/admin/prompts/config/version \
  -H "Authorization: $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "prompt_name": "k8s_debug",
    "category": "agents",
    "provider": "default",
    "new_version": "v2",
    "reason": "v2 validated and promoted"
  }'
```

---

## Provider-Specific Overrides

Create a provider-specific file when a model needs different instruction formatting
(e.g., Llama instruction tags for Bedrock, GPT-4 JSON mode for Azure):

```bash
mkdir -p prompts/bedrock/v1/agents
cp prompts/default/v1/agents/k8s_debug.txt \
   prompts/bedrock/v1/agents/k8s_debug.txt

# Add Llama-specific formatting (e.g., [INST] / [/INST] tags)
vim prompts/bedrock/v1/agents/k8s_debug.txt
```

The loader automatically picks up the provider-specific file when the runtime
provider resolves to `bedrock`.

---

## Emergency Rollback

### Disable an experiment
```bash
curl -X POST http://localhost:9999/api/admin/prompts/experiments/{name}/disable \
  -H "Authorization: $ADMIN_TOKEN"
```

### Roll back DB config to v1
```bash
curl -X POST http://localhost:9999/api/admin/prompts/config/version \
  -H "Authorization: $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "prompt_name": "k8s_debug",
    "category": "agents",
    "provider": "default",
    "new_version": "v1",
    "reason": "emergency rollback"
  }'
```

---

## Database Tables

| Table | Purpose |
|-------|---------|
| `llm_prompt_configuration` | Per-prompt/account/provider version overrides |
| `llm_prompt_experiments` | A/B test experiment definitions |
| `llm_prompt_config_audit` | Audit log of all configuration changes |
| `llm_prompt_usage_metrics` | Per-load latency and cache metrics |

All tables are created by Hasura migration `V658_create_prompt_versioning_tables`.

---

## Naming Conventions

| What | Convention |
|------|------------|
| Prompt files | `{name}.txt` — lowercase, underscores, no `agent_` prefix |
| Versions | `v{major}` only — `v1`, `v2`, `v3` (no `v1.1`, `v2-beta`) |
| Constants | `PromptAgent{Name}` for agents, `PromptPlanner{Name}` for planners |
| Categories | `agents`, `planners`, `tools`, `utilities` |
