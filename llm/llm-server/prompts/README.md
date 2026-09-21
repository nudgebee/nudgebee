# Prompt Versioning System

This directory contains all LLM prompts for the system, organized by model and version.

## Directory Structure

```
prompts/
├── default/                  # Model-agnostic prompts (active content lives here)
│   └── v1/
│       ├── agents/           # Agent system prompts
│       ├── planners/         # Planner prompts
│       ├── tools/            # Tool prompts
│       ├── utilities/        # Utility prompts
│       └── _fragments/       # Include-only partials shared across prompts
│
├── models/                    # Per-model and per-family overrides, one subdirectory per key
│   ├── .gitkeep                # keeps //go:embed all:models valid with no overrides yet
│   └── <model-or-family>/v1/agents/...   # e.g. models/qwen/v1/agents/k8s_lean.yaml
│
├── loader.go                 # Embedded FS loader with version + experiment resolution
├── registry.go               # Prompt name constants and promptCategories
├── types.go                  # DB types (experiments, config, metrics)
├── db.go                     # Database queries for config and experiments
├── cache.go                  # In-memory cache layer
└── metrics.go                # OpenTelemetry metrics
```

> Add a `models/<model>/v1/...` file only when that exact model needs different
> instructions than `default/`. New model directories need no Go/embed change —
> `//go:embed all:default all:models` already covers the whole subtree.

---

## Go Files

| File | Responsibility |
|------|---------------|
| `registry.go` | Prompt name constants (`Prompt*`) and `promptCategories` that maps each constant to its category directory. Entry point for callers: `GetPrompt()`, `RenderPrompt()`, `GetModelFromConfig()`. |
| `loader.go` | Core loading engine. Embeds the `default/` and `models/` directory trees via `//go:embed`. Owns the `PromptLoader` struct which chains: cache check → experiment lookup → DB config lookup → hardcoded default → `loadPromptFile()`. Also owns `InitializeGlobalLoader()`, `GetLoader()`, and cache management helpers (`ClearCache`, `ClearCacheForPrompt`, `ClearCacheForAccount`). |
| `types.go` | All shared types: `PromptCategory`, `ConfigSource`, `PromptRequest/Response/Metadata`, `ResolvedConfig`, DB structs (`DBConfig`, `DBExperiment`, `DBAuditLog`, `DBMetrics`), and admin API request/response structs. |
| `db.go` | All PostgreSQL queries. `PromptDB` wraps `common.DatabaseManager` and provides: `GetConfig` (version override lookup with account+model priority, exact model tried before its canonicalized form), `GetActiveExperiments` (A/B test targeting), `CreateExperiment` (with overlap detection), `UpsertConfig`, `DisableExperiment`, `UpdateExperimentAccounts`, `CreateAuditLog`, `GetAuditLogs`, `RecordMetrics`, `GetExperimentMetrics`, `IsAvailable` (ping + table existence check). Gracefully degrades — DB errors fall through to embedded defaults. |
| `cache.go` | Thread-safe in-memory TTL cache (`PromptCache`). Cache key is `name:category:model:accountID`. Supports targeted invalidation by prompt name, by account, or full clear. Background goroutine cleans up expired entries every 5 minutes. Default TTL is 1 hour. |
| `metrics.go` | OpenTelemetry metrics. Registers six counters/histograms under the `nb_llm_*` namespace: total loads, load latency, cache hit/miss, config source distribution, experiment participation, and error count. Recording happens asynchronously (goroutine) so it never blocks prompt loading. |
| `loader_test.go` | Tests for `PromptLoader`: embedded file resolution, fallback path ordering, cache behaviour, and graceful DB-absent operation. |
| `registry_test.go` | Tests for `GetPrompt` and `RenderPrompt`: mapping lookup, template rendering, missing module handling. |

---

## How It Works

### Resolution Priority

When loading a prompt the system tries four paths in order:

1. **Active Experiment** — account-targeted A/B test version (DB)
2. **Database Configuration** — account/model-specific override (DB)
3. **Hardcoded Default** — falls back to `v1`

### File Path Resolution

For each resolution step the loader tries paths in order, where `{model}` is the
resolved model exactly as configured (e.g. `qwen3-235b-vertex`), `{canonicalModel}`
is its canonicalized form (only tried when it differs from `{model}`), and
`{family}` is its shared model-line override, if any (only tried when it differs
from both — see `modelFamily` in `loader.go`; e.g. any model containing `qwen`
resolves its family tier to `models/qwen/`):

```
models/{model}/{version}/{category}/{name}.yaml
models/{canonicalModel}/{version}/{category}/{name}.yaml
models/{family}/{version}/{category}/{name}.yaml
default/{version}/{category}/{name}.yaml
models/{model}/v1/{category}/{name}.yaml
models/{canonicalModel}/v1/{category}/{name}.yaml
models/{family}/v1/{category}/{name}.yaml
default/v1/{category}/{name}.yaml      ← always the final fallback
```

If `model` resolves to `"default"` (no model-specific config anywhere), the
`models/` tier is skipped entirely and resolution goes straight to `default/`.

Family matching is deliberately coarse — it exists for guardrails that apply to
a whole model line regardless of the exact deployment string (self-hosted vLLM,
a specific gateway integration, a vendor's hosted endpoint all resolve the same
way). Add an exact or canonical override alongside a family one when a specific
deployment needs to diverge from what the rest of its family gets — exact and
canonical are tried first and always win.

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
    "model": "default",
    "new_version": "v2",
    "reason": "v2 validated and promoted"
  }'
```

---

## Model-Specific and Family Overrides

Create a **model-specific** file when one exact deployment string needs different
instructions or formatting than every other model sharing the same base prompt
(vendor-specific formatting quirks, a fix scoped to one gateway integration):

```bash
mkdir -p prompts/models/<exact-model-string>/v1/agents
cp prompts/default/v1/agents/k8s_lean.yaml \
   prompts/models/<exact-model-string>/v1/agents/k8s_lean.yaml
```

Create a **family** file instead when a whole model line needs the same tuning
regardless of which exact deployment string it's configured under (e.g. every
Qwen deployment — self-hosted vLLM, an AI-gateway integration, a vendor-hosted
endpoint — needs the same extra guardrails a large frontier model doesn't):

```bash
mkdir -p prompts/models/qwen/v1/agents
cp prompts/default/v1/agents/k8s_lean.yaml \
   prompts/models/qwen/v1/agents/k8s_lean.yaml

# Edit with the family-specific instructions
vim prompts/models/qwen/v1/agents/k8s_lean.yaml
```

The loader automatically picks up the family file for any request whose resolved
model *contains* the family key (case-insensitive) — `qwen3-235b-vertex`,
`Qwen/Qwen3.6-35B-A3B-FP8`, and `qwen/qwen3-vl-235b-a22b-instruct` all resolve to
`models/qwen/`. The known families live in `modelFamilyPatterns` in `loader.go`
— add an entry there for a new family key. No code change is needed for a new
*exact* or *canonical* override, only for a new *family*.

Resolution always tries exact, then canonical, then family, in that order — a
model-specific file next to a family one always wins for that one deployment.

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
    "model": "default",
    "new_version": "v1",
    "reason": "emergency rollback"
  }'
```

---

## Database Tables

| Table | Purpose |
|-------|---------|
| `llm_prompt_configuration` | Per-prompt/account/model version overrides |
| `llm_prompt_experiments` | A/B test experiment definitions |
| `llm_prompt_config_audit` | Audit log of all configuration changes |
| `llm_prompt_usage_metrics` | Per-load latency and cache metrics |

Tables were created by migration `V658_create_prompt_versioning_tables` (provider-scoped)
and converted to model-scoping by `V916_convert_prompt_versioning_to_model_scoping`
(Atlas, `api-server/migrations/migrations/app/`) — the `provider` column on each table
was dropped and replaced with `model`.

---

## Naming Conventions

| What | Convention |
|------|------------|
| Prompt files | `{name}.txt` — lowercase, underscores, no `agent_` prefix |
| Versions | `v{major}` only — `v1`, `v2`, `v3` (no `v1.1`, `v2-beta`) |
| Constants | `PromptAgent{Name}` for agents, `PromptPlanner{Name}` for planners |
| Categories | `agents`, `planners`, `tools`, `utilities` |
