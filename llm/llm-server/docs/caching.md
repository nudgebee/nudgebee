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
For Anthropic models (`claude-3-7-sonnet`, `claude-3-5-sonnet`), the provider places up to 3 strategic `cache_control: {type: "ephemeral"}` breakpoints:
- Breakpoint 1: Last text part of System instructions (independent caching of system prompt and tool definitions).
- Breakpoint 2 & 3: Last text parts of previous stable Human/System turns.

### 4. Per-Model TTL Profiles
- **Flash models** (`gemini-2.5-flash`, `gemini-2.0-flash`): Low storage cost per token hour. Configured with a default TTL of **30 minutes** (`LLM_SERVER_CACHE_FLASH_TTL_MINUTES`) to maximize multi-turn break-even cache hits.
- **Pro models** (`gemini-2.5-pro`, `gemini-1.5-pro`): Higher storage cost per token hour. Configured with a default TTL of **10 minutes** (`LLM_SERVER_CACHE_PRO_TTL_MINUTES`) to prevent storage overhead during inactive sessions.
- **Static Scopes** (`Global`, `Account`): Fixed **12-hour TTL** for cross-session instruction reuse.

### 5. In-Engine Self-Healing on Cache Invalidation
If a cache reference fails at generation time (e.g. 403/404 `CachedContent not found` or model mismatch), `retryLLMCall` invalidates the stale cache entry from Redis, disables caching for the call, and retries in-place with the full prompt messages transparently.

### 6. Observability & Metrics
Prometheus metrics provide visibility into cache efficiency:
- `nb_llm_cache`: Incremented on every cache operation with labels `provider`, `model`, `status` (`hit`/`miss`/`error`), `account_id`, `agent`, `scope`.
- `nb_llm_cache_skip`: Tracks cache skips with labels `reason` (`no_cacheable_messages`, `model_no_cache_support`, `insufficient_tokens`, `exceeds_context_window`, `disabled`), `provider`, `model`, `account_id`, `agent`, `scope`.
- `nb_llm_cached_tokens`: Total token savings from cached queries.
- `nb_llm_cache_invalidations`: Churn tracking for caches invalidated before planned TTL.

## Google AI Provider: System Message Handling

Google AI's API accepts a single `SystemInstruction` field, not multiple system messages in the `contents` array. When we send multiple system messages (base prompt, agent prompt, account prompt, etc.), they are **merged into one `SystemInstruction`** by concatenating all their parts. This happens in `llms/googleai/caching.go` and `llms/googleai/googleai.go`.

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
│ 3. <additional_system_prompt>{AccountPrompt}                │
│    </additional_system_prompt> (optional)                   │
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
│      conversation_context, history                          │
│    </task_context>                                          │
│    <notebook_content>{notebook}</notebook_content>          │
│    <question>{input}</question>                             │
│    {scratchpad}  <- grows each ReAct3 iteration             │
└─────────────────────────────────────────────────────────────┘
```

### Critique Message Layout (Top-Level Investigation Answers Only)

Fires when `LlmServerReActCritiqueEnabled=true` (default), the agent is top-level (not a sub-agent), and the query is an investigation task. Uses `planner_react_critiquer.txt`.

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
| Account-level customizations | System message (`AccountPrompt` / `additionalPrompt`) | Stable per account = cacheable |
| User query, conversation history, scratchpad | Human message | Changes every request = must not pollute cache |
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
