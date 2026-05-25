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
