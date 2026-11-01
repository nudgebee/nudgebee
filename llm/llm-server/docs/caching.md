# Planner Prompt Structure & LLM Caching

The ReAct3 planner splits its prompt into **system messages** (stable, cacheable) and a final **human message** (dynamic, per-request). This is critical for LLM prompt caching — providers cache the message prefix by byte-matching, so all stable content must come first as system messages, and all dynamic content must be in the final human message.

## Cache Scopes

Defined in `agents/core/llm_cache.go`. Agents declare their scope by implementing the `NBAgentCacheScopeProvider` interface.

| Scope | TTL | Shared across | Cache key format | Used by |
|-------|-----|---------------|-----------------|---------|
| `Global` | 12h | All accounts and conversations | `global:{agent}:{model}:{credsFp}` | Utility calls (acknowledgment, suggestions, memory extraction) |
| `Account` | 12h | All conversations within an account | `account:{accountId}:{agent}:{model}:{credsFp}` | Most agents (k8s_debug, aws_debug, etc.) |
| `Conversation` | Flash: 30m, Pro: 10m (Configurable) | Single conversation only | `conv:{accountId}:{conversationId}:{agent}:{model}:{credsFp}` | Default if no scope declared |

For Global/Account scopes, only **system messages** are included in the cached prefix. For Conversation scope, everything up to the last human message is cached.

## Caching Architecture & Performance Optimizations

### 1. Zero-Latency Cache Hit
Cached pointers stored in shared memory/Redis are trusted for the duration of their TTL. On a cache hit, the cached content name (`CachedContentName`) is attached to call options immediately with **0ms overhead**, bypassing redundant remote verification API calls.

### 2. Non-Blocking Async Cache Creation
When a cache miss occurs and `LLM_SERVER_ASYNC_CACHE_CREATION=true` (default), the LLM request generates immediately with full un-cached messages. Cache creation runs asynchronously in a detached background goroutine, ensuring the current request never suffers a 1–5s latency penalty during cache instantiation. Subsequent requests will immediately hit the created cache.

### 3. Anthropic Multi-Breakpoint Caching
For the `anthropic` provider, the cache provider places up to 3 `cache_control: {type: "ephemeral"}` breakpoints, in every cache scope (Anthropic's cache is content-addressed):
- Breakpoint 1: the system prompt — caches tools + system, shared across runs of the agent.
- Breakpoint 2: the tail — the last non-assistant message (a tool result or human turn). The next step of the tool loop resends this prompt plus its new turn, so it reads the whole previous prompt from cache.
- Breakpoint 3: the previous step's tail — the last non-assistant message before the last assistant turn, i.e. where the previous request wrote. Keeps the read working when one step adds more than Anthropic's 20-block lookback (a wide parallel batch).

TTL: the system-prompt marker of Global/Account-scope agents uses Anthropic's 1-hour TTL (`llm_server_anthropic_system_cache_ttl`, default `1h`, rollback `5m`) so the shared system prompt + tools survive gaps between runs; 1-hour writes bill at 2x input (tracked from `usage.cache_creation.ephemeral_1h_input_tokens` and priced into `cost_usd`). Conversation scope and all tool-loop markers keep the 5-minute default, which every read within a run refreshes.

Assistant messages are never marked. This relies on the tool loop being append-only between steps; window-gated observation compression rewrites older tool results and costs one cache miss when it kicks in.

### 4. Per-Model TTL Profiles
- **Flash models** (`gemini-2.5-flash`, `gemini-2.0-flash`): Low storage cost per token hour. Configured with a default TTL of **30 minutes** (`LLM_SERVER_CACHE_FLASH_TTL_MINUTES`) to maximize multi-turn break-even cache hits.
- **Pro models** (`gemini-2.5-pro`, `gemini-1.5-pro`): Higher storage cost per token hour. Configured with a default TTL of **10 minutes** (`LLM_SERVER_CACHE_PRO_TTL_MINUTES`) to prevent storage overhead during inactive sessions.
- **Static Scopes** (`Global`, `Account`): Fixed **12-hour TTL** for cross-session instruction reuse.

### 5. In-Engine Self-Healing on Cache Invalidation
If a cache reference fails at generation time (e.g. 403/404 `CachedContent not found` or model mismatch), `retryLLMCall` invalidates the stale cache entry from Redis, disables caching for the call, and retries in-place with the full prompt messages transparently.

### 6. Observability & Metrics
Prometheus metrics provide visibility into cache efficiency:
- `nb_llm_cache`: Incremented on every cache operation with labels `provider`, `model`, `status` (`hit`/`miss`/`error`), `account_id`, `agent`, `scope`.
- `nb_llm_cache_skip`: Tracks cache skips with labels `reason` (`no_cacheable_messages`, `model_no_cache_support`, `insufficient_tokens`, `exceeds_context_window`, `disabled`, `conversation_scope_implicit`, `hybrid_low_coverage`), `provider`, `model`, `account_id`, `agent`, `scope`.
- `nb_llm_cached_tokens`: Total token savings from cached queries.
- `nb_llm_cache_invalidations`: Churn tracking for caches invalidated before planned TTL.

## Google AI Provider: Conversation Scope Uses Implicit Caching (#39633)

Gemini 2.5+ caches repeated prompt prefixes automatically ("implicit" caching). A request that references one of our `CachedContent` entries gets **no** implicit caching on the rest of the prompt. On Gemini, **Conversation-scope calls never create or reference `CachedContent`**: those entries were created after a conversation's first call, read 1.4–3.9 times before expiring, and for react_3/react_4 held only the system prompt — so they capped every tool step at the system prompt. The calls are left to implicit caching, which covers the whole growing tool history. Global and Account scope are unchanged.

Implicit caching needs a byte-stable prefix: anything that changes near the top of the prompt between steps (a per-second timestamp, a notebook rewritten in the opening message, compression rewriting older steps, tool definitions in a different order) cuts the reusable prefix at that point. In react_4 the notebook and timestamp in the opening message are fixed for the turn, so the prompt only grows at the end; one known exception is a clarification continuation, which rewrites the question framing in that message once. Measured on dev: implicit entries are best-effort (a hit ~6 in 10 times after a single call, never after 5 minutes), and inside a tool loop ~83% of follow-up calls hit. Explicit and implicit hits look the same in `llm_conversation_token_usage` (`is_cache_hit` is true whenever cached tokens > 0); the `cacheMode` field on the `LLM cache tokens used` log line tells them apart. Dashboards built on `is_cache_hit` or cached-token share therefore count implicit hits too since this change, so rates before and after 2026-09-30 are not like-for-like.

## Google AI Provider: Hybrid Use of Global/Account Caches (#39633)

Global/Account-scope `CachedContent` is the only cache that survives a long idle gap (12h), so a cold first call relies on it. But every later step of a tool loop that references it gets no implicit caching on the tool history. `LLM_SERVER_GEMINI_CACHE_STRATEGY` chooses:

| Value | Behavior |
|---|---|
| `explicit` | Reference the live cache on every call (rollback value) |
| `hybrid` (default) | In a tool loop, reference it only while it covers at least `LLM_SERVER_GEMINI_CACHE_HYBRID_MIN_COVERAGE` (default 0.65) of the prompt's tokens; otherwise send the full prompt so implicit caching covers the longer prefix. A request with no tool calls or results (a one-shot helper) always keeps the cache: it never repeats, so implicit caching cannot help it |

Coverage = cached tokens / (cached tokens + rest of the prompt). The cached side is the entry's exact size, as reported by Gemini when it was created (tools included); the rest is estimated from its characters at ~3 characters per token. Because the prompt only grows within a run, a run normally switches from explicit to implicit once (on dev, 1 of 93 runs switched back, e.g. after compression shrank the prompt). Skipped calls log `hybrid_low_coverage` with the coverage value.

## Google AI Provider: System Message Handling

Google AI's API accepts a single `SystemInstruction` field, not multiple system messages in the `contents` array. When we send multiple system messages (base prompt, stable account context, agent prompt, etc.), they are **merged into one `SystemInstruction`** by concatenating all their parts. This happens in `llms/googleai/caching.go` and `llms/googleai/googleai.go`.

This means:
- `CountTokens`, `CreateCachedContent`, and `GenerateContent` all merge system messages the same way
- The order of system messages is preserved (parts are appended in order)
- Non-system messages (human, AI) go into `contents` as separate entries

## ReAct3 Planner Message Layout

Built in `planner_react_3.go`. Base template: `planner_react_3_base.txt`.

```
┌─────────────────────────────────────────────────────────────┐
│ SYSTEM MESSAGES (stable — cached at Account/Global scope)   │
├─────────────────────────────────────────────────────────────┤
│ 1. Base react_3 prompt (planner_react_3_base.txt)           │
│    Template vars: tool_names, tool_descriptions, today,     │
│    workspace_enabled, shell_tool_enabled,                   │
│    context_management_rules, time_handling_rules,           │
│    data_protection_rules, code_analysis_rules               │
│    Includes: hypothesis notebook discipline, parallel       │
│    action rules, SKILL LISTS instruction                    │
├─────────────────────────────────────────────────────────────┤
│ 2. Client tools priority instruction (if ClientTools exist) │
├─────────────────────────────────────────────────────────────┤
│ 3. <account_context>{AccountContext}</account_context>      │
│    (optional, stable account-wide GlobalContext)            │
├─────────────────────────────────────────────────────────────┤
│ 4. <additional_agent_prompt>{additionalPrompt}              │
│    </additional_agent_prompt> (optional, from DB config)    │
├─────────────────────────────────────────────────────────────┤
│ 5. Agent prompt (agentPrompt — full agent system prompt,    │
│    e.g., k8s_debug orchestrator instructions)               │
├─────────────────────────────────────────────────────────────┤
│ HUMAN MESSAGE (dynamic — changes every iteration)           │
├─────────────────────────────────────────────────────────────┤
│ 6. <task_context>                                           │
│      request-specific AccountPrompt, context, history       │
│    </task_context>                                          │
│    <notebook_content>{notebook}</notebook_content>          │
│    <question>{input}</question>                             │
│    {scratchpad}  <- grows each ReAct3 iteration             │
└─────────────────────────────────────────────────────────────┘
```

### Critique Message Layout (Top-Level Investigation Answers Only)

Fires when the agent is top-level (not a sub-agent) and the query is an investigation task. Uses `planner_react_critiquer.txt`.

```
┌─────────────────────────────────────────────────────────────┐
│ SYSTEM MESSAGE (cacheable)                                  │
├─────────────────────────────────────────────────────────────┤
│ Critiquer rules (planner_react_critiquer.txt)               │
│  Template vars: tool_names, time_handling_rules             │
│  Enforces: 5-Whys causality, evidence-based findings,       │
│  no status-only / manual-CLI answers                        │
├─────────────────────────────────────────────────────────────┤
│ HUMAN MESSAGE (per attempt)                                 │
├─────────────────────────────────────────────────────────────┤
│  <task_input>, <question_type>, <tools_invoked>,            │
│  <final_answer>, <scratchpad>                               │
└─────────────────────────────────────────────────────────────┘
```

## Rules for Adding New Prompt Content

| Content type | Where to place | Why |
|---|---|---|
| Static rules, instructions, tool usage guidelines | System message | Stable across requests = cacheable |
| Agent domain expertise (investigation methodology) | System message (via `GetSystemPrompt()`) | Stable per agent = cacheable at Account scope |
| Account-wide GlobalContext | System message (`AccountContext`) | Stable per account = cacheable |
| Entry-point/request additional instructions | Human message (`AccountPrompt`) | May differ between event analysis and ordinary chat |
| User query, conversation history, scratchpad | Human message | Changes every request = must not pollute cache |
| RAG examples retrieved for the query (`NBAgentPrompt.Rag`) | Human message (`rag_examples`, ReAct planners) | Retrieved per query; other planner types still get them in the system prompt |
| Date/time (`today`) | System message is OK | Rotates daily; acceptable for 12h TTL |
| Previous tool observations, iteration state | Human message (`scratchpad`) | Changes every ReAct3 iteration |

> **Key rule:** Never add dynamic per-request content (history, user input, scratchpad) to system messages. This breaks cache byte-matching and forces cache misses on every request, wasting the entire cached prefix.

## Declaring Cache Scope for a New Agent

Implement `NBAgentCacheScopeProvider` (defined in `agents/core/interface.go`):

```go
func (a *MyAgent) GetCacheScope() core.CacheScope {
    return core.CacheScopeAccount // or CacheScopeGlobal
}
```

If not implemented, the agent defaults to `CacheScopeConversation` (10m TTL, no cross-conversation sharing).
