# Agent Architecture Research — Status Report

**Date:** 2026-10-04
**Scope:** llm-server agent architecture improvements identified in the Claude Code source pattern research

---

## Summary

Of the 18 improvement areas identified in the original research, **14 are fully implemented**, **1 is partially done**, and **3 remain open gaps**.

---

## Fully Implemented (14)

### 1. Context-Window-Gated Scratchpad Compression
**Files:** `agents/core/scratchpad.go`

Compression activates at 0.75 of model context window (configurable via `compressionActivationFraction()`), hard cap at 0.90. The 10 most recent steps keep full observations; older steps get byte-truncated to 500-byte previews. Per-observation hard cap at 65KB (middle-truncated). Legacy fallback `LlmServerAgentMaxScratchpadChars` = 200000 chars.

### 2. LLM-Based Observation Summarization
**Files:** `agents/core/scratchpad_summarizer.go`

Gated by `LlmServerScratchpadSummarizationEnabled` (off by default). When on, uses tiered approach: skip if ≤500B, byte-truncate if <1024B, otherwise LLM call with 10s timeout on `ModelTierSummary`. Falls back to byte truncation on any LLM error. Production-ready but opt-in.

### 3. Tool-Level Truncation Before Scratchpad
**Files:** `agents/core/executor_planner.go` (~line 1926)

`truncateToolResponse()` applies `SmartTruncateToolOutput` to every tool response **before** scratchpad entry, governed by `LlmServerMaxToolOutputLen` (success) and `LlmServerMaxToolErrorOutputLen` (failure). This was a gap in the first review — now implemented as a pre-scratchpad truncation layer.

### 4. Failure Pruning in Scratchpad
**Files:** `agents/core/scratchpad.go` (~line 302)

Failed steps that are later retried successfully have their observation truncated to 200 chars + `[failure log minimized -- retried successfully]`. Exceptions preserve full text for RCA-relevant strings: "permission denied", "access denied", "not found", "does not exist".

### 5. Data Quality Summary Injection
**Files:** `agents/core/scratchpad.go`

`ConstructScratchPad` appends a `<data_quality>` XML block after observations with `failed`, `empty`, `success`, and `total` counts. Includes guidance distinguishing genuine failures from successful-but-empty mutations, instructs LLM to add "Recommended Next Steps" when critical data is missing.

### 6. Anthropic Multi-Breakpoint Caching
**Files:** `agents/core/llm_cache.go`

Up to 3 `cache_control: {type: "ephemeral"}` breakpoints placed at: (1) last text part of last System message, (2-3) last text parts of eligible Human/System messages walking in reverse. Content-addressed inline caching — no server-side CachedContent resource. Was a gap in first review — now done.

### 7. Singleflight + Cross-Replica Lock (Google AI)
**Files:** `agents/core/llm_cache.go`

`GoogleAICacheProvider` embeds a `singleflight.Group` to collapse concurrent in-process cache-miss creations. Cross-replica Redis lock (`CacheTryLock`, 2m TTL) prevents duplicate CachedContent creation across pods. Non-holders poll up to 30s via `waitForSharedCacheInfo` before falling back.

### 8. Three-Scope Cache Architecture
**Files:** `agents/core/llm_cache.go`, `docs/caching.md`

Global (12h), Account (12h), Conversation (Flash 30m, Pro 10m). Cache key format: `{scope}:{accountId}:{conversationId}:{agent}:{model}:{credsFp}`. Self-healing on cache invalidation (403/404 retry with fresh prompt). Documented.

### 9. Turn-Level Dedup Cache
**Files:** `agents/core/tool_call_cache.go`

Mutex-guarded map deduplicates tool calls within a single turn. Keys normalized: tool names lowercased, inputs whitespace-collapsed, JSON objects sorted by key. On hit, duplicate/no-progress notices injected into scratchpad. Cache hits/misses logged at executor termination.

### 10. Egress Filter
**Files:** `security/egressfilter/` (full package)

Five baseline credential patterns: AWS access key, PEM private key headers, GitHub PATs (`gh[pousr]_`), OpenAI API keys (`sk-`/`sk-proj-`), Anthropic API keys (`sk-ant-`). Three modes: `ModeDetect` (default, log only), `ModeEnforce` (block), `ModeRedact` (replace with placeholders). Off by default — opt-in via `LlmServerEgressFilterEnabled`. Custom rules extensible via `custom_rules.go`. `WrapModel` intercepts all LLM calls at the factory chokepoint. E2E test coverage: 5+ tests for enforce/audit/disabled/clean scenarios. **Docs exist** at `docs/llm-egress-filter.md`.

### 11. Write Confirmation Workflow
**Files:** `agents/core/executor_planner.go`

`writeConfirmationRequired` gates create/update/delete tool calls. `doAction` pauses execution, sets `ConversationStatusWaiting`, returns confirmation question. User "ok"/"yes"/"true" proceeds; rejection stops. Per-action or per-tool scoping via `ToolConfirmationScope`. Parallel batches with write-eligible tools fall back to sequential.

### 12. DelegateAgentTool
**Files:** `agents/agent_delegate.go`

Spawns `dynamicReActAgent` with: custom prompt, explicit tool subset (resolved via `resolveToolsForDelegate`), configurable budget (default 5, min 2, max 15 iterations), recursion prevention (filters `delegate_agent` from sub-agent tools). Runs on `ModelTierRetrieval` (cheap tier). Rejects notebook-misuse prompts via regex. Skill inheritance propagated: `InheritSkillsFromAgents`, `OriginalQuery`, `SelectedSkillIds`.

### 13. ReAct4 Planner (NEW — Not in Original Research)
**Files:** `agents/core/planner_react_4.go` (1733 lines), `docs/planner_react_4.md`

Provider-native tool calling engine, now the DEFAULT planner for all Orchestrating and ReAct agents when the model supports native tools. Uses `llms.WithTools()` instead of XML grammar. ReAct3 retained as rollback/capability fallback (`LLM_SERVER_REACT4_ENABLED=false`). Includes `orchestratorDeepThinking()` for elevated thinking on first top-level call and post-critique refinements.

### 14. ReWoo/ReAct2 Deletion + Prompt Migration
**Files:** `agents/core/executor.go`, `prompts/default/v1/`, `prompts/loader.go`

Both legacy planners deleted entirely. Shared symbols extracted to `planner_react_shared.go`. Prompts migrated from `.txt` in `agents/prompts_repo/` to YAML under `prompts/default/v1/` with embedded loading. Orchestrators renamed `*_debug` → `*_orchestrator` with back-compat aliases.

---

## Partially Implemented (1)

### 15. Dynamic Thinking Budget
**Files:** `agents/core/llm_tokencount.go`, `agents/core/llm_common.go`

**What exists:** Tier-based thinking — `ModelTier` (Reasoning, Summary, Retrieval) maps to token caps (minimal=512, low=2048, medium=8192, high=16384). `IsInvestigationRequestTask` drives model tier selection (investigations keep Reasoning tier). `orchestratorDeepThinking()` applies elevated thinking for first planning call.

**What's missing:** No per-query complexity scoring that adjusts thinking tokens dynamically. All allocations are static/categorical, not query-adaptive. A lightweight pre-flight classifier that scores query difficulty (simple lookup vs. multi-step investigation vs. deep RCA) and sets thinking budget accordingly would close this gap.

---

## Open Gaps (3)

### 16. On-Demand Conversation Compaction
No user-triggered `/compact` equivalent exists. `applyPreflightContextWindowCap` trims largest messages before each LLM call automatically. `handleTokenLimitError` triggers summarization reactively on 4xx token-limit errors. Both are automatic — there's no way for a user or orchestrator to trigger mid-conversation compaction on demand (e.g., to reclaim context budget before a complex next step).

### 17. Delegate Agent Resume from Incomplete State
`DelegateAgentTool` creates ephemeral `dynamicReActAgent` instances. If a delegate hits its iteration budget or times out, the partial investigation cannot be resumed — the sub-agent's scratchpad and intermediate steps are not reconstructable from persisted state. Contrast with the main planner's `Marshal`/`Unmarshal` for conversation resumption.

### 18. Delta Hydrator for Conversation Context
No incremental context update mechanism exists. Each turn rebuilds the full conversation context from scratch. A delta hydrator would let the system inject only changed context (new tool results, updated metrics, new events) rather than re-serializing the entire conversation history, reducing token waste on long conversations.

---

## Changes Since First Review

| Area | First Review | Latest |
|------|-------------|--------|
| Tool-level truncation | Gap | **Implemented** (`truncateToolResponse()`) |
| Anthropic caching | Single breakpoint | **Multi-breakpoint** (up to 3) |
| Egress filter docs | Missing | **Exist** (`docs/llm-egress-filter.md`) |
| Egress filter modes | Detect + Enforce | **+ ModeRedact** added |
| Default planner | ReAct3 | **ReAct4** (native tools, ReAct3 as fallback) |
| Orchestrator thinking | Not present | **orchestratorDeepThinking()** in ReAct4 |
| Prompt system | `.txt` templates | **YAML** under `prompts/default/v1/` |
| Orchestrator naming | `*_debug` | **`*_orchestrator`** with aliases |
| ReWoo/ReAct2 | Existed as fallback | **Deleted entirely** |
