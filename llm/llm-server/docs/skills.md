# Skills — Design Document

## Overview

Skills are user-authored knowledge base entries (rows in `llm_knowledgebases`) that users map to agents via the `llm_kb_agent_mappings` junction table. When an agent runs, the executor makes the mapped skills visible to the LLM so the user's expert guidance (runbooks, conventions, domain rules) actually shapes the agent's reasoning — without the user having to restate it in every prompt.

Two agent families consume skills via two different mechanisms, because they run with fundamentally different execution loops:

```
 User Query
   → Executor Entry (top-level stamps OriginalQuery + optional SelectedSkillIds)
   ├─ ReAct / ReWoo agent
   │    → injectKBContext:
   │         ├─ manual KBs: <skill-lists> (names+descriptions from DB)
   │         └─ integration KBs: parallel RAG search (module: "knowledge_base")
   │              → appends previews (title + source + first 2-3 lines) to <skill-lists>
   │              → 5s timeout
   │    → FilterAndInjectDefaultTools: auto-injects load_skills tool
   │    → LLM picks relevant names, calls load_skills(name)
   │         ├─ kb_type='manual' → body from DB (cached)
   │         ├─ kb_type='integration' (DB row, empty data) → RAG fallback via enrichIntegrationSkillsFromRAG
   │         │    → single QueryRAG(module: "knowledge_base") + optional metadata_filter
   │         │    → 10s timeout, result cached
   │         └─ not in DB (RAG-only) → parallel RAG search by name, 10s timeout
   │
   └─ Custom-planner agent (loganalysis, metrics, traces, logs, logs_default,
                            resource_search, websearch)
        → LoadActiveAgentSkillContents: eager bodies → request.SkillsContext
        → Execute() prepends the <skills>...</skills> block to its LLM prompt
```

## Storage model

### `llm_knowledgebases`

One row per skill: `id`, `account_id`, `tenant_id`, `name`, `description`, `data` (the full body — Markdown, text, YAML, etc.), `data_format`, `status` (`active` / inactive), `kb_type` (`manual` or `integration`), `kb_source` (nullable — `confluence`, `servicenow`, etc.), `integration_id` (nullable — FK to the integration that created the entry), plus audit columns.

- **`kb_type = 'manual'`** (default): content lives in the `data` column, authored by users directly.
- **`kb_type = 'integration'`**: content lives externally (indexed in Qdrant via the RAG server). The `data` column is empty; `kb_source` identifies the integration origin. These entries are auto-created by `knowledgebase_sync.go` when a Confluence or ServiceNow integration is connected.

### `llm_kb_agent_mappings`

Junction table mapping KBs to agents by **agent name**, not UUID. A single KB can be mapped to multiple agents; a single agent can have many mapped KBs. The UI (`app/src/components1/llm/ListAgents.jsx`) passes `agent.name` on both sides — system and custom — so there is no UUID/name ambiguity. The schema comment on `agent_id` confirms `"Agent name/ID"`.

```
llm_knowledgebases
 ├── id (uuid)
 ├── name (unique per account)
 ├── description
 ├── data            ← full body (empty for integration-type)
 ├── status          ← 'active' is a precondition for loading
 ├── kb_type         ← 'manual' (default) | 'integration'
 ├── kb_source       ← nullable: 'confluence', 'servicenow', …
 └── integration_id  ← nullable: FK to source integration
        ▲
        │ 1 : N
        │
llm_kb_agent_mappings
 ├── kb_id
 ├── agent_id    ← agent.Name (not uuid)
 └── account_id
```

## Execution paths

### Path A — Lazy `load_skills` (ReAct / ReWoo planners)

This is the existing, designed path for any agent whose planner runs a tool-execution loop.

1. **`injectKBContext`** (`agents/core/executor.go`) fetches active mapped KBs for the union of `agent.GetName()` + any inherited ancestor names. For **manual** KBs it renders **names and descriptions** from the DB. For **integration** KBs (detected via `kb.KBType == "integration"`), it runs a parallel RAG search with the user's query (`module: "knowledge_base"`, top 3 results) and appends **previews** — title, source, and first 2-3 lines of each RAG result — to the same `<skill-lists>` block. The RAG preview fetch has a 5-second timeout. The combined list is prepended as an `Instructions` item on `basePrompt`.
2. **`FilterAndInjectDefaultTools`** (`agents/core/utils.go`) sees the `<skill-lists>` marker in the rendered system message and auto-injects the `load_skills` tool into the agent's toolset. No per-agent configuration.
3. The planner (ReAct or ReWoo) executes normally. The LLM reads the skill list — which now contains both manual skill names and RAG-sourced previews — decides which entries look relevant, and calls `load_skills(name)` when it wants the full body.
4. **`LoadSkillsTool`** (`tools/skills.go`) resolves the requested name through a three-tier lookup:
   - **DB exact match** → fetches `kb.data` from `llm_knowledgebases` (cached in `CacheNamespaceLlmSkillContent`).
   - **DB fuzzy match** → ILIKE substring search if exact match fails.
   - **RAG fallback** → if still not found (e.g. RAG-only integration content the LLM saw in previews), searches RAG in parallel using each missing name as the query. All RAG lookups run concurrently with a 10-second timeout per call.
   
   For **integration-type** skills found in DB but with empty `data`, `enrichIntegrationSkillsFromRAG` fires — a single `QueryRAG` call with `module: "knowledge_base"`. If all integration skills share the same `kb_source` (e.g. `"confluence"`), the call includes a `metadata_filter` (e.g. `{"source": "confluence"}`) to narrow results. Results are cached in `CacheNamespaceLlmSkillContent`.
   
   All RAG results are capped at `LlmServerMaxSkillContentLength` (default 5000 chars), split evenly across documents with a per-doc minimum of 500 chars.

**Why lazy is the right default**: the `<skill-lists>` marker is cheap (names + descriptions, a handful of lines per skill), the LLM has already read the user's question when it decides what to load, and only the bodies the LLM actually needs are paid for in tokens.

### Path B — Eager inline (custom-planner agents)

Agents whose `GetPlannerType()` is `AgentPlannerTypeCustom` implement their own `Execute()` and never run a planner or the `load_skills` tool. If we did nothing, mapped skills would be invisible to them. For these agents the executor eagerly inlines the bodies.

1. At **top-level invocation** (detected by empty `request.OriginalQuery`) the executor stamps `request.OriginalQuery = request.Query` and — when `LlmServerSkillSelectionTopK > 0` — runs `SelectRelevantSkills` (see "Question-aware selection" below) to produce `request.SelectedSkillIds`.
2. For any agent with `AgentPlannerTypeCustom`, the executor calls `LoadActiveAgentSkillContents(accountId, agentNames, restrictToIds)`. It returns:
   - A rendered `<skills>...</skills>` block, each body wrapped in `<![CDATA[...]]>` so user-authored content that legitimately contains `</skill>` (e.g. a skill teaching XML) cannot break the framing.
   - One `NBToolResponseReference{Type: "skill"}` per loaded body, appended to the final agent response for UI "Skills used" rendering.
3. The result is stored on `request.SkillsContext`.
4. Each custom-planner agent's `Execute()` reads `request.SkillsContext` and prepends it to the relevant LLM call:
   - `agent_log_analysis.go`: prepended to `messageContent` and subtracted from the log-data token budget before truncation.
   - `agent_log_default.go::generateFinalResponse`: prepended to `systemPrompt`.
   - `agent_resource_search.go`: added as a system message.
   - `agent_unified_search.go::synthesizeAnswer`: added as a system message.
5. Delegators (`metrics`, `traces`, `logs`, `logs_default`) do **not** read `SkillsContext` themselves — they just propagate inheritance (see below). Their underlying provider sub-agents (Prometheus, Datadog, Clickhouse, query_generator, …) are ReAct agents and pick skills up via Path A.

### Path C — `search_skills` (semantic search across all sources)

`SearchSkillsTool` (`tools/skills.go`) is a standalone tool that searches across **all** skill sources — manual DB skills and external integration skills — by natural language query. It is registered but not wired to any agent yet (future use).

1. **Manual search**: per-word tokenized `ILIKE` on `name` and `description` of `kb_type = 'manual'` skills (account-wide, no agent mapping required). Query is tokenized via `TokenizeForSkillSelection` (lowercased, stop words removed), each token must match name or description. Returns the first 500 chars as a snippet (LIMIT 5).
2. **Integration search**: a single RAG call via `searchKBsViaRAG` with `module: "knowledge_base"`, which searches all KB collections for the account in one request. The RAG server's `/get_matching_doc` endpoint handles cross-collection search. An optional `metadata_filter` (e.g. `{"source": "confluence"}`) can narrow results by source.
3. Both searches run in parallel goroutines with an overall 10-second timeout. Manual results include skill references; RAG results complement them (content-based, not name-based — no dedup needed since manual KBs are not searched via RAG).

```
search_skills("kubernetes troubleshooting")
  ├─ goroutine 1: DB ILIKE on manual KBs (per-word tokenized) → snippets
  ├─ goroutine 2: single RAG call (module: "knowledge_base")
  │    → searches all KB collections for the account
  │    → optional metadata_filter narrows by source
  └─ select { manualCh, ragCh, time.After(10s) }
     → merged XML results
```

## Inheritance across delegation

Several custom-planner agents are *delegators* — they accept a user request, decide which underlying provider to use, and invoke a sub-agent via `ExecuteAgentToolCall`. The sub-agent is a normal ReAct agent scoped to its own name (e.g. `prometheus`, not `metrics`), so without extra plumbing, skills the user mapped to `metrics` would be invisible to the sub-agent.

The `InheritSkillsFromAgents []string` field on `NBAgentRequest` and `NbToolContext` carries the chain of ancestor agent names down through delegation:

```
User → metrics          InheritSkillsFromAgents = nil
         Execute() sets  InheritSkillsFromAgents = ["metrics"]   on NbToolContext
       → prometheus      executor unions prometheus's own KBs with KBs mapped to "metrics"
                         → <skill-lists> contains both sets
                         → lazy load_skills fetches bodies on demand
```

Longer chains accumulate:

```
User → logs              []
       → logs_default    ["logs"]
         → query_generator ["logs", "logs_default"]  (ReAct planner, lazy path)
         → resource_search ["logs", "logs_default"]  (custom planner, eager path)
```

### `injectKBContext` filter semantics

When `SelectedSkillIds` is non-nil, `injectKBContext` filters KBs fetched from **inherited** ancestor names against the selection set, but **always retains** KBs mapped directly to the sub-agent's own name. Rationale: a sub-agent's own-scope skills are authored for that agent's specific job and should not be hidden by an upstream parent's broad selection.

## Question-aware selection (BM25)

### Why

Eager loading every mapped skill body on every call is fine when users map 1–3 skills, but breaks down when a user maps 20+. Token cost and prompt dilution both hurt. The lazy path doesn't have this problem because bodies aren't loaded until the LLM asks.

### How

`tools/core/skill_selection.go` implements a pure-stdlib BM25 scorer (k1=1.5, b=0.75) over `name + " " + description`. Key design choices:

- **Candidate set = corpus.** IDF is computed over whatever is mapped to this agent chain, not a global corpus. This keeps the helper dependency-free and bounds memory to O(candidates).
- **Tokenization** lowercases, splits on non-alphanumeric runes, drops stopwords (a tiny handful — over-aggressive stopword removal hurts short technical descriptions), and drops single-character tokens.
- **Document frequency** is counted single-pass: build a `querySet` once, iterate each doc once, bump `df` at most once per `(term, doc)` via a per-doc seen set. O(N·L) rather than the naive O(Q·N·L), and correct even when the user query has duplicate tokens (`"error error panic"`).
- **Zero-overlap docs are dropped** even if top-K isn't reached. With `topK=10` and only 2 docs actually matching any query term, the result is still 2 — never pads with irrelevant skills.
- **`topK <= 0` disables selection.** The default is `0`.

### Triggering

The executor runs selection only at top-level invocation (`request.OriginalQuery == ""`) and only when `config.Config.LlmServerSkillSelectionTopK > 0`. Sub-agents reached via `ExecuteAgentToolCall` inherit `OriginalQuery` and `SelectedSkillIds` unchanged — they must trust the parent's selection because a mechanical sub-agent command (e.g. `"fetch CPU for pod foo"`) would destroy the relevance signal if re-scored.

### Config flag

```go
// config.Config
LlmServerSkillSelectionTopK int `mapstructure:"llm_server_skill_selection_top_k"`
```

Environment variable: `LLM_SERVER_SKILL_SELECTION_TOP_K`. Default `0` (disabled — legacy "show every mapped skill" behaviour). Set to `3` or `5` to enable.

### Behaviour table

| LlmServerSkillSelectionTopK | ReAct path (`<skill-lists>`) | Custom-planner path (`SkillsContext`) |
| :-- | :-- | :-- |
| `0` (default) | Every active mapped KB shown as name+description | Full body of every active mapped KB inlined |
| `>0` | Selected KBs shown; non-inherited own-scope KBs always retained | Full body of each selected KB inlined |

## Custom-planner agents currently consuming `SkillsContext`

| Agent | Planner | LLM call that reads `SkillsContext` |
| :-- | :-- | :-- |
| `loganalysis` | Custom | `Execute()` — prepended to `messageContent`, budgeted against `maxTokens` before log truncation |
| `logs_default` | Custom | `generateFinalResponse()` — prepended to `systemPrompt` |
| `resource_search` | Custom | `Execute()` — added as a system message before routing-tool selection |
| `websearch` | Custom | `synthesizeAnswer()` — added as a system message before final synthesis |

Custom-planner **delegators** (`metrics`, `traces`, `logs`, `logs_default`'s query_generator invocation) do not read `SkillsContext`. They propagate `InheritSkillsFromAgents` + `OriginalQuery` + `SelectedSkillIds` to their sub-agents and let the sub-agent's own executor entry load what it needs.

## Response references

Skills loaded via Path B produce `NBToolResponseReference` entries (`Type: "skill"`, `Text: kb.name`, `Url: kb.id`, `Description: kb.description`) that the executor appends to `agentResponse.References` at the end of `executeAgent`. The UI can render them alongside tool references as "Skills used".

References are emitted **only** for skills the executor actually loaded at that invocation. With selection enabled, references reflect what was loaded for this question — a better signal for end users than "skills mapped to this agent".

Duplicates in delegation chains are avoided by emitting references only at the boundary where the skill was actually loaded. The parent custom-planner agent loads and emits; sub-agents reached via delegation rebuild `SkillsContext` fresh at their own executor entry but emit their own references independently, so a parent skill and a sub-agent skill never collide.

## Operational knobs

| Knob | Default | What it does |
| :-- | :-- | :-- |
| `LlmServerSkillSelectionTopK` | `0` | Enables BM25 selection at top-level entry. `0` = disabled. |
| `CacheNamespaceLlmKbMapping` | 5 min TTL | Caches `ListAgentKBs` results per `(account, agent)` key; invalidated on map/unmap. |
| `CacheNamespaceLlmSkillContent` | — | Caches individual skill bodies fetched via `load_skills` (lazy path). Invalidated on KB update/delete. |

## Things to watch out for

1. **Unbounded skill size.** No per-skill or aggregate size cap exists today. A user mapping a 200KB runbook to `loganalysis` pays for it on every call. The BM25 selector narrows the *count* of skills inlined, not the *size* of each body. A future improvement is a per-skill byte cap with tail-truncation and an explicit `[truncated]` marker.
2. **Selection runs against the original user query, not sub-agent commands.** This is deliberate. `OriginalQuery` is stamped once at top-level entry and propagated verbatim. Any refactor that "rewrites" `OriginalQuery` deeper in the call tree would silently degrade relevance.
3. **Sub-agent own-scope skills bypass the selection filter.** `injectKBContext` intentionally retains them regardless of `SelectedSkillIds`. If you map a skill to `prometheus` specifically, it is always shown when `prometheus` runs, even if the user's question wouldn't have scored it highly against the aggregated parent-agent corpus.
4. **The eager path does not use `load_skills` under the hood.** Custom-planner agents inline bodies directly from `llm_knowledgebases.data` via `LoadActiveAgentSkillContents`. The `CacheNamespaceLlmSkillContent` cache only helps the lazy path. If this becomes a bottleneck, the helper could be taught to consult the same cache.
5. **Reference UI type is `"skill"`.** Existing UI may only know `"link" | "file" | "k8s_resource" | "citation"` (see the comment on `NBToolResponseReference.Type`). Verify `app/` renders `"skill"` gracefully.
6. **Integration skill RAG latency.** `enrichIntegrationSkillsFromRAG` caps the RAG call at 10 seconds, but this adds latency on cache miss for integration-type skills. Typically ~100-200ms. Results are cached in `CacheNamespaceLlmSkillContent` so subsequent calls for the same skill are fast. When all integration skills share the same `kb_source`, a `metadata_filter` is applied automatically to narrow results.
7. **KB indexing format.** Manual KB data is wrapped as a JSON array (`["content"]`) before sending to the RAG server, so it's indexed as a single Qdrant document. Very large KBs (50K+ chars) may exceed embedding model token limits — the tail would be ignored. A future improvement is paragraph/section-level chunking.

## Test coverage

### BM25 selection — `tools/core/skill_selection_test.go`

- Empty inputs; `topK <= 0`; `len(candidates) <= topK`; empty query.
- Query-overlap ranking; zero-overlap dropping; top-K capping.
- Stopword filtering.
- All-docs-empty-after-tokenization fallback.
- **Regression: duplicate query tokens (`"error error panic"`) must not inflate document frequency for the repeated term.**
- Tokenizer edge cases (punctuation, hyphens, digits, single-char drops).

### Skills tools — `tools/skills_test.go`

**Unit tests** (no external dependencies):
- `TestLoadSkillsTool_ParseSkillNames` — comma splitting, dedup, whitespace trimming, empty segments.
- `TestSkillData_IntegrationTypeRouting` — `kb_type`/`kb_source` routing logic: manual vs integration, empty/whitespace data detection.
- `TestSearchSkillsTool_Metadata` — tool name, type, description, input schema validation.
- `TestLoadSkillsTool_ArgumentParsing` — all input formats: standard args, unnamed args, command with colon/equals/quotes, slice args, filler phrase stripping.

**Integration tests** (gated by `TEST_ACCOUNT` env var):
- `TestLoadSkillsTool_Integration_EmptyName` — empty skill_name returns error.
- `TestLoadSkillsTool_Integration_NonExistentSkill` — missing skill returns "not found".
- `TestLoadSkillsTool_Integration_MultipleNonExistent` — multiple missing skills handled gracefully.
- `TestSearchSkillsTool_Integration_EmptyQuery` — empty query returns error.
- `TestSearchSkillsTool_Integration_NoResults` — nonsense query returns success without crash.
- `TestSearchSkillsTool_Integration_BasicQuery` — real query hits DB + RAG without errors.
- `TestSearchSkillsTool_Integration_CommandFallback` — `Command` field fallback when `Arguments` has no query.

## Related files

- `llm/llm-server/agents/core/executor.go` — top-level entry, selection gate, `injectKBContext` (manual + RAG preview injection), reference appending.
- `llm/llm-server/agents/core/interface.go` — `NBAgentRequest.SkillsContext`, `InheritSkillsFromAgents`, `OriginalQuery`, `SelectedSkillIds`.
- `llm/llm-server/agents/core/factory_agent.go` — `ExecuteAgentToolCall` propagation of all four skill-related fields.
- `llm/llm-server/agents/core/utils.go` — `FilterAndInjectDefaultTools` (lazy `load_skills` auto-injection).
- `llm/llm-server/tools/core/knowledgebase_service.go` — `ListAgentKBs`, `ListActiveAgentSkillCandidates`, `LoadActiveAgentSkillContents`, `MapKBToAgent`.
- `llm/llm-server/tools/core/skill_selection.go` — BM25 scorer.
- `llm/llm-server/tools/core/tool_context.go` — `NbToolContext` skill propagation fields.
- `llm/llm-server/tools/skills.go` — `load_skills` and `search_skills` tool implementations, RAG enrichment for integration-type skills.
- `llm/llm-server/tools/core/knowledgebase_sync.go` — background sync that creates integration KB entries for Confluence/ServiceNow with empty `data`.
- `llm/llm-server/tools/core/rag_service.go` — `QueryRAG()` and `QueryRAGCollection()` clients for Qdrant-backed RAG server. Both accept an optional `metadataFilter` parameter for filtering results by metadata fields (e.g. `{"source": "confluence"}`).
- `llm/llm-server/config/config.go` — `LlmServerSkillSelectionTopK`.
