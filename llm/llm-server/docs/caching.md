# Planner Prompt Structure & LLM Caching

Both planners split their prompts into **system messages** (stable, cacheable) and a final **human message** (dynamic, per-request). This is critical for LLM prompt caching — providers cache the message prefix by byte-matching, so all stable content must come first as system messages, and all dynamic content must be in the final human message.

## Cache Scopes

Defined in `agents/core/llm_cache.go`. Agents declare their scope by implementing the `NBAgentCacheScopeProvider` interface.

| Scope | TTL | Shared across | Cache key format | Used by |
|-------|-----|---------------|-----------------|---------|
| `Global` | 12h | All accounts and conversations | `global:{agent}:{model}` | Utility calls (acknowledgment, suggestions, memory extraction) |
| `Account` | 12h | All conversations within an account | `account:{accountId}:{agent}:{model}` | Most agents (k8s_debug, aws_debug, etc.) |
| `Conversation` | Configurable (default 10m) | Single conversation only | `conv:{accountId}:{conversationId}:{agent}:{model}` | Default if no scope declared |

For Global/Account scopes, only **system messages** are included in the cached prefix. For Conversation scope, everything up to the last human message is cached.

## Google AI Provider: System Message Handling

Google AI's API accepts a single `SystemInstruction` field, not multiple system messages in the `contents` array. When we send multiple system messages (base prompt, agent prompt, account prompt, etc.), they are **merged into one `SystemInstruction`** by concatenating all their parts. This happens in `llms/googleai/caching.go` and `llms/googleai/googleai.go`.

This means:
- `CountTokens`, `CreateCachedContent`, and `GenerateContent` all merge system messages the same way
- The order of system messages is preserved (parts are appended in order)
- Non-system messages (human, AI) go into `contents` as separate entries

## ReWOO Planner Message Layout

Built in `reWooCreatePrompt2()` in `agents/core/planner_rewoo_2.go`:

```
┌─────────────────────────────────────────────────────────────┐
│ SYSTEM MESSAGES (stable — cached at Account/Global scope)   │
├─────────────────────────────────────────────────────────────┤
│ 1. Base planner prompt (planner_rewoo_2_base.txt)           │
│    Template vars: tool_names, tool_descriptions,            │
│    max_plan_steps, workspace_enabled, shell_tool_enabled,   │
│    context_management_rules, time_handling_rules,           │
│    code_analysis_rules                                      │
├─────────────────────────────────────────────────────────────┤
│ 2. <task_instructions>{agentPrompt}</task_instructions>     │
│    Agent's domain-specific prompt                           │
│    (e.g., agent_k8s_debug.txt content)                     │
├─────────────────────────────────────────────────────────────┤
│ 3. <task_preferences>{additionalPrompt}</task_preferences>  │
│    Per-agent config overrides from DB (optional)            │
├─────────────────────────────────────────────────────────────┤
│ 4. Client tools priority instruction (if ClientTools exist) │
├─────────────────────────────────────────────────────────────┤
│ 5. <global_preferences>{AccountPrompt}</global_preferences> │
│    Account-level prompt customization (optional)            │
├─────────────────────────────────────────────────────────────┤
│ 6. <critical_rules>                                         │
│    SKILL LISTS + User Request Adherence Protocol            │
│    + Output Format rules                                    │
│    </critical_rules>                                        │
├─────────────────────────────────────────────────────────────┤
│ HUMAN MESSAGE (dynamic — changes every request)             │
├─────────────────────────────────────────────────────────────┤
│ 7. <task_context>                                           │
│      today, task_context, conversation_context, history     │
│    </task_context>                                          │
│    <notebook_content>{notebook}</notebook_content>          │
│    <question_type>{investigation|query}</question_type>     │
│    <task>{input}</task>                                     │
└─────────────────────────────────────────────────────────────┘
```

### ReWOO Reviewer Message Layout

Built in `reviewAndRefinePlan()` in `agents/core/planner_rewoo_2.go`:

```
┌─────────────────────────────────────────────────────────────┐
│ SYSTEM MESSAGE (stable — cacheable across iterations)       │
├─────────────────────────────────────────────────────────────┤
│ 1. Reviewer rules (planner_rewoo_2_reviewer.txt)            │
│    Template vars: tool_names, time_handling_rules           │
│    Contains: 5-Whys, recovery protocol, output format       │
├─────────────────────────────────────────────────────────────┤
│ HUMAN MESSAGE (dynamic — changes every review iteration)    │
├─────────────────────────────────────────────────────────────┤
│ 2. <task_input>, <question_type>, <current_progress>,       │
│    <remaining_plan>, <notebook_content>                     │
└─────────────────────────────────────────────────────────────┘
```

## ReAct Planner Message Layout

Built in `reActCreatePrompt2()` in `agents/core/planner_react_2.go`:

```
┌─────────────────────────────────────────────────────────────┐
│ SYSTEM MESSAGES (stable — cached at Account/Global scope)   │
├─────────────────────────────────────────────────────────────┤
│ 1. Base react prompt (planner_react_base_2.txt)             │
│    Template vars: tool_names, tool_descriptions, today,     │
│    workspace_enabled, shell_tool_enabled,                   │
│    context_management_rules, time_handling_rules,           │
│    data_protection_rules, code_analysis_rules               │
│    Note: includes SKILL LISTS instruction already           │
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
│    e.g., kubectl agent instructions)                        │
├─────────────────────────────────────────────────────────────┤
│ HUMAN MESSAGE (dynamic — changes every iteration)           │
├─────────────────────────────────────────────────────────────┤
│ 6. <task_context>                                           │
│      conversation_context, history                          │
│    </task_context>                                          │
│    <question>{input}</question>                             │
│    {scratchpad}  <- grows each ReAct iteration              │
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
| Previous tool observations, iteration state | Human message (`scratchpad`) | Changes every ReAct iteration |

> **Key rule:** Never add dynamic per-request content (history, user input, scratchpad) to system messages. This breaks cache byte-matching and forces cache misses on every request, wasting the entire cached prefix.

## Declaring Cache Scope for a New Agent

Implement `NBAgentCacheScopeProvider` (defined in `agents/core/interface.go`):

```go
func (a *MyAgent) GetCacheScope() core.CacheScope {
    return core.CacheScopeAccount // or CacheScopeGlobal
}
```

If not implemented, the agent defaults to `CacheScopeConversation` (10m TTL, no cross-conversation sharing).
