package core

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// turnToolCallCache provides thread-safe, normalized caching for tool call results
// within a single conversation turn. It persists across plan regeneration/critique
// cycles within the same turn, preventing duplicate tool executions.
type turnToolCallCache struct {
	cache   map[string]NBAgentPlannerToolActionStep
	mu      sync.RWMutex
	hits    atomic.Int64
	misses  atomic.Int64
	entries atomic.Int64
}

// Get checks the cache for a normalized key. Returns the cached step and true if found.
// Callers must treat the returned value as read-only; reference-type fields (e.g. slices
// in Artifacts) are shared with the cached entry and must not be mutated.
func (c *turnToolCallCache) Get(toolName, toolInput string) (NBAgentPlannerToolActionStep, bool) {
	key := normalizeToolCallKey(toolName, toolInput)
	c.mu.RLock()
	defer c.mu.RUnlock()
	if step, exists := c.cache[key]; exists {
		c.hits.Add(1)
		return step, true
	}
	c.misses.Add(1)
	return NBAgentPlannerToolActionStep{}, false
}

// Put stores a tool result under the normalized key.
func (c *turnToolCallCache) Put(toolName, toolInput string, step NBAgentPlannerToolActionStep) {
	key := normalizeToolCallKey(toolName, toolInput)
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.cache[key]; !exists {
		c.entries.Add(1)
	}
	c.cache[key] = step
}

// Stats returns cache hit/miss statistics.
func (c *turnToolCallCache) Stats() (hits, misses, entries int64) {
	return c.hits.Load(), c.misses.Load(), c.entries.Load()
}

// duplicateCallNotice is the render-only prefix shown in the PLANNER's scratchpad for
// a turn-cache hit (a step with IsDuplicateCacheHit). The cache silently prevented a
// redundant EXECUTION, but the LLM iteration that chose to repeat the call still
// happened — and without a signal the planner keeps re-deciding the same call
// (observed: events sub-agents re-issued identical queries ~55% of the time, driving
// deep-turn LLM-call blowups). Making the repeat VISIBLE breaks that loop. Injected at
// scratchpad-build time (ConstructScratchPad), NOT baked into the step's Observation,
// so it never reaches the terminal response, GetToolInvocations/UI, or the summarizer.
const duplicateCallNotice = "⚠️ ALREADY EXECUTED THIS TURN: this tool call (or a normalized-equivalent one) was already run earlier in this turn and the result below is UNCHANGED. Do NOT repeat it — reason from this result, take a DIFFERENT action, or finalize your answer.\n\n"

// repeatedResultNotice covers what duplicateCallNotice cannot: a DIFFERENT input to
// the same tool that produced the IDENTICAL result. The turn cache keys on the input,
// so rephrasing the command every iteration walks straight past it. That is the exact
// shape of the expensive failure — a model that mis-typed one argument (a case-typo'd
// `--query` field) rewrote its command six times, got the same null result each time,
// and diagnosed the CLI instead of its own input. Naming the pattern is what redirects
// it from "the tool is broken" to "my argument is wrong". Render-only, like the notice
// above, so it never reaches the terminal response, the UI, or the summarizer.
// repeatedResultNotice escalates with the occurrence count. A 7-day DB sweep found a
// single run where shell_execute returned the same "File not found" recovery hint to
// 16 different commands and the model kept rewriting the command — a static sentence
// repeated verbatim reads as part of the output, not as a signal. Naming the number is
// what makes the repetition itself visible.
func repeatedResultNotice(occurrences int) string {
	if occurrences >= repeatedResultEscalateAt {
		return fmt.Sprintf("🛑 STOP — IDENTICAL RESULT %d TIMES: %d different commands to this tool have all returned the byte-identical output below. Continuing to vary the command WILL NOT change it. Either (a) the resource genuinely does not exist / is empty, or (b) your arguments are wrong in a way you keep repeating (check field-name casing and paths against the raw output). Do NOT issue another variation of this command — use a different tool, or answer with what you have.\n\n", occurrences, occurrences)
	}
	return "⚠️ SAME RESULT AS AN EARLIER CALL: you changed the command but got a byte-identical result from this tool. Re-running variations will not change it. The problem is most likely in your ARGUMENTS — check field names against the raw output (they are case-sensitive), or take a genuinely different approach.\n\n"
}

// repeatedResultEscalateAt is the occurrence count at which the advisory notice becomes
// a stop directive. Chosen from the 7-day distribution of repeat groups: 185 groups
// stopped at 2 occurrences (advisory is enough / the loop self-terminated), while the
// 152 groups that reached 3+ accounted for 321 of the 506 wasted calls — 63%.
const repeatedResultEscalateAt = 3

// trivialResultNotice covers the retry-on-empty-result pattern that
// repeatedResultNotice's byte-identical guard doesn't catch: the same tool has
// returned trivial output (`[]`, `null`, "no data") N+ times in this turn.
// Two distinct real-world causes converge on this signal:
//  1. Retry loop — the model concludes the OUTPUT FORMAT is broken and
//     rewrites with --output table → --output json → --output text / jq, all
//     with the same mis-cased filter field, all returning trivially empty.
//     Case study: 7d sweep session 9ac1beed had 12 aws_execute + shell_execute
//     calls on `--query DbInstanceIdentifier` (should be DBInstanceIdentifier).
//  2. Legit multi-region/namespace sweep where most or all come back empty.
//
// The notice text handles both correctly: naming "no matches" as a valid
// conclusion is exactly what a sweep should conclude on empty regions, AND
// what the retry-loop model needs to hear to stop varying the command.
// Rendered at scratchpad-build time — never reaches the terminal response,
// the UI, or the summarizer.
func trivialResultNotice(toolName string, occurrences int) string {
	return fmt.Sprintf(
		"🛑 EMPTY RESULT %d TIMES — STOP RETRYING: `%s` has returned trivial/empty output (`[]`, `null`, or \"no data\") %d times this turn. "+
			"Empty output IS a valid CLI response, not a format bug — retrying with different --output flags, jq piping, or format variations will produce the same empty result. "+
			"Either: (a) accept that no matching resources/rows exist and answer with what you have, or "+
			"(b) if you used a --query / --filter / -o jsonpath / WHERE clause with a specific field name, verify field-name CASING against a raw un-filtered sample (JMESPath / JSONPath / SQL identifiers are case-sensitive), or "+
			"(c) switch to a genuinely different tool or approach. Do NOT issue another format variation of this command.\n\n",
		occurrences, toolName, occurrences,
	)
}

// whitespaceRegex matches one or more whitespace characters.
var whitespaceRegex = regexp.MustCompile(`\s+`)

// normalizeToolCallKey creates a normalized cache key for tool call deduplication.
// It normalizes both the tool name and input to catch near-identical queries that
// differ only in whitespace, casing, or JSON key ordering.
func normalizeToolCallKey(toolName, toolInput string) string {
	normalizedTool := strings.ToLower(strings.TrimSpace(toolName))
	normalizedInput := normalizeToolInput(toolInput)
	// Use null byte separator to avoid collisions when tool names contain ':'
	return fmt.Sprintf("%s\x00%s", normalizedTool, normalizedInput)
}

// normalizeToolInput normalizes tool input text to catch near-identical queries.
// It trims whitespace, collapses internal whitespace runs, and normalizes JSON
// objects to have sorted keys for consistent comparison.
func normalizeToolInput(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return input
	}

	// Try JSON normalization first (handles both objects and arrays)
	if normalized, ok := tryNormalizeJSON(input); ok {
		return normalized
	}

	// For non-JSON inputs, collapse internal whitespace
	return collapseWhitespace(input)
}

// collapseWhitespace replaces all runs of whitespace characters (spaces, tabs,
// newlines) with a single space.
func collapseWhitespace(s string) string {
	return whitespaceRegex.ReplaceAllString(s, " ")
}

// tryNormalizeJSON attempts to parse the input as JSON and re-serialize it with
// sorted keys. This catches semantically identical JSON that differs in key
// ordering or whitespace. Returns the normalized string and true if successful.
func tryNormalizeJSON(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) == 0 {
		return s, false
	}

	// Quick check: must start with { or [
	if s[0] != '{' && s[0] != '[' {
		return s, false
	}

	var parsed any
	if err := json.Unmarshal([]byte(s), &parsed); err != nil {
		return s, false
	}

	normalized := sortAndSerialize(parsed)
	return normalized, true
}

// sortAndSerialize recursively sorts JSON object keys and produces a canonical
// string representation for cache key comparison.
func sortAndSerialize(v any) string {
	switch val := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%q:%s", k, sortAndSerialize(val[k])))
		}
		return "{" + strings.Join(parts, ",") + "}"

	case []any:
		parts := make([]string, 0, len(val))
		for _, item := range val {
			parts = append(parts, sortAndSerialize(item))
		}
		return "[" + strings.Join(parts, ",") + "]"

	case string:
		b, _ := json.Marshal(val)
		return string(b)

	case float64:
		b, _ := json.Marshal(val)
		return string(b)

	case bool:
		if val {
			return "true"
		}
		return "false"

	case nil:
		return "null"

	default:
		b, _ := json.Marshal(val)
		return string(b)
	}
}
