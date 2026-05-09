# Requirement: Explicit Config Name Matching in Tool Config Selection

## Problem

When a user explicitly names a tool configuration in their query (e.g., `"use 'gcp-dev - nudgebee-dev' account"`), the system does not recognize it as a direct config selection. It falls through all existing auto-selection strategies and prompts the user to select — even though the answer was already in the original query.

### Current selection strategy order (`followupForMultipleToolConfigs`)

| # | Strategy | How |
|---|---|---|
| 1 | Config already in `QueryConfig.ToolConfigs` | Exact map lookup |
| 2 | Only one config available | Auto-select |
| 3 | `IdentifyConfig` (tool-level keyword matching) | Tags, environment keywords, hostnames |
| 4 | LLM-based selection (feature-flagged) | Semantic match via prompt |
| 5 | **Ask user** | Followup dialog |

The missing strategy: a **verbatim / normalized config name match** against the user's query — before LLM or user prompt.

### Example

Available configs: `gcp-dev`, `gcp-dev - nudgebee-dev`, `gcp-dev - nudgebee-prod`
User query: `"use 'gcp-dev - nudgebee-dev' account"`
Expected: auto-select `gcp-dev - nudgebee-dev`
Actual: falls to step 5 (ask user)

---

## Desired Behavior

Insert a new strategy (Strategy 2.5) between `IdentifyConfig` and LLM selection:

**For each available config name, check if the user's query contains a normalized match:**
1. Strip quotes and punctuation from both the query and config name
2. Compare case-insensitively
3. If a single config matches → auto-select it; log strategy as `"explicit_name_match"`
4. If multiple configs match → continue to next strategy (LLM or ask user)

This requires no LLM call and handles the most common "power user" pattern where they copy-paste or type a config name directly.

---

## Requirements

### Functional
- **FR-1:** Before invoking LLM-based selection, check all available config names against the user's original query (`agentRequest.Query`) using **exact, case-insensitive** matching (strip surrounding quotes/whitespace from query, then compare whole config name).
- **FR-2:** Partial matches (e.g., `nudgebee-dev` matching `gcp-dev - nudgebee-dev`) must NOT trigger auto-selection — the full config name must be present in the query.
- **FR-3:** If exactly one config name is found in the query, auto-select it and skip remaining strategies.
- **FR-4:** If more than one config name matches (unlikely with exact matching), do not auto-select — continue to LLM or ask-user fallback.
- **FR-5:** Record strategy as `"explicit_name_match"` in `recordConfigSelectionStrategy` for observability.

### Non-functional
- No LLM call required for this strategy — must be pure string matching.
- Must not break existing behavior when no explicit name is mentioned.

---

## Implementation Hint

In `executor_planner.go`, inside `followupForMultipleToolConfigs`, add after the `IdentifyConfig` block and before the LLM block:

```go
// Strategy 3.5: Check if the user explicitly named a config in their query (exact match)
if e.agentRequest.Query != "" {
    matched := findConfigByExactNameInQuery(e.agentRequest.Query, configs)
    if matched != nil {
        if e.agentRequest.QueryConfig.ToolConfigs == nil {
            e.agentRequest.QueryConfig.ToolConfigs = make(map[string]string)
        }
        e.agentRequest.QueryConfig.ToolConfigs[tool.Name()] = matched.Name
        recordConfigSelectionStrategy(&e.agentRequest.QueryConfig, tool.Name(), "explicit_name_match")
        e.ctx.GetLogger().Info("plannerexecutor: resolved tool config via explicit name match",
            "tool", tool.Name(), "config", matched.Name)
        return nil, nil, nil
    }
}

// helper — exact, case-insensitive match only; no partial/substring config names
func findConfigByExactNameInQuery(query string, configs []toolcore.ToolConfig) *toolcore.ToolConfig {
    // Strip surrounding quotes and normalize whitespace from query
    normalizedQuery := strings.ToLower(strings.TrimSpace(query))
    var matched *toolcore.ToolConfig
    for i := range configs {
        name := strings.ToLower(strings.TrimSpace(configs[i].Name))
        if strings.Contains(normalizedQuery, name) {
            if matched != nil {
                return nil // two config names both appear verbatim — ambiguous, don't guess
            }
            matched = &configs[i]
        }
    }
    return matched
}
```

---

## Out of Scope
- Fuzzy/typo-tolerant matching (e.g., Levenshtein distance)
- Matching config names mentioned in conversation history (only current query)
- Changes to the followup UI or config management APIs

## Decisions
- **Exact match only** — partial/substring config name matches must not trigger auto-selection, to avoid ambiguity (e.g., `"nudgebee-dev"` must NOT match `"gcp-dev - nudgebee-dev"`).

## Open Questions
- Should this also scan `ConversationContext` (previous turns), not just the current `Query`?
