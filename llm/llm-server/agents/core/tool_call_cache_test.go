package core

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeToolCallKey_BasicNormalization(t *testing.T) {
	tests := []struct {
		name      string
		toolName  string
		toolInput string
		expected  string
	}{
		{
			name:      "lowercase tool name",
			toolName:  "KUBECTL_EXECUTE",
			toolInput: "get pods",
			expected:  "kubectl_execute\x00get pods",
		},
		{
			name:      "trim whitespace",
			toolName:  "  kubectl_execute  ",
			toolInput: "  get pods  ",
			expected:  "kubectl_execute\x00get pods",
		},
		{
			name:      "collapse internal whitespace",
			toolName:  "kubectl_execute",
			toolInput: "get   pods   -n   default",
			expected:  "kubectl_execute\x00get pods -n default",
		},
		{
			name:      "collapse tabs and newlines",
			toolName:  "kubectl_execute",
			toolInput: "get\tpods\n-n\tdefault",
			expected:  "kubectl_execute\x00get pods -n default",
		},
		{
			name:      "empty input",
			toolName:  "tool",
			toolInput: "",
			expected:  "tool\x00",
		},
		{
			name:      "whitespace only input",
			toolName:  "tool",
			toolInput: "   \t\n  ",
			expected:  "tool\x00",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeToolCallKey(tt.toolName, tt.toolInput)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNormalizeToolCallKey_JSONNormalization(t *testing.T) {
	tests := []struct {
		name   string
		inputA string
		inputB string
	}{
		{
			name:   "different key order",
			inputA: `{"namespace":"default","resource":"pods"}`,
			inputB: `{"resource":"pods","namespace":"default"}`,
		},
		{
			name:   "different whitespace in JSON",
			inputA: `{"namespace": "default", "resource": "pods"}`,
			inputB: `{  "namespace":"default" ,  "resource" : "pods"  }`,
		},
		{
			name:   "nested objects with different key order",
			inputA: `{"spec":{"replicas":3,"selector":"app"},"name":"deploy"}`,
			inputB: `{"name":"deploy","spec":{"selector":"app","replicas":3}}`,
		},
		{
			name:   "arrays preserved in order",
			inputA: `{"items":["a","b","c"]}`,
			inputB: `{ "items" : [ "a" , "b" , "c" ] }`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keyA := normalizeToolCallKey("tool", tt.inputA)
			keyB := normalizeToolCallKey("tool", tt.inputB)
			assert.Equal(t, keyA, keyB, "should produce identical cache keys")
		})
	}
}

func TestNormalizeToolCallKey_DifferentInputsStayDifferent(t *testing.T) {
	tests := []struct {
		name      string
		toolNameA string
		toolNameB string
		inputA    string
		inputB    string
	}{
		{
			name:      "different values",
			toolNameA: "tool", toolNameB: "tool",
			inputA: `{"namespace":"default"}`,
			inputB: `{"namespace":"production"}`,
		},
		{
			name:      "different tools same input",
			toolNameA: "tool_a", toolNameB: "tool_b",
			inputA: "get pods",
			inputB: "get pods",
		},
		{
			name:      "different text commands",
			toolNameA: "tool", toolNameB: "tool",
			inputA: "get pods -n default",
			inputB: "get pods -n production",
		},
		{
			name:      "array order matters",
			toolNameA: "tool", toolNameB: "tool",
			inputA: `["a","b"]`,
			inputB: `["b","a"]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keyA := normalizeToolCallKey(tt.toolNameA, tt.inputA)
			keyB := normalizeToolCallKey(tt.toolNameB, tt.inputB)
			assert.NotEqual(t, keyA, keyB, "different inputs should produce different cache keys")
		})
	}
}

func TestCollapseWhitespace(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"single spaces preserved", "a b c", "a b c"},
		{"multiple spaces collapsed", "a   b   c", "a b c"},
		{"tabs collapsed", "a\tb\tc", "a b c"},
		{"newlines collapsed", "a\nb\nc", "a b c"},
		{"mixed whitespace", "a \t\n b", "a b"},
		{"empty string", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, collapseWhitespace(tt.input))
		})
	}
}

func TestTryNormalizeJSON(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		expectJSON bool
	}{
		{"valid object", `{"a":1,"b":2}`, true},
		{"valid array", `[1,2,3]`, true},
		{"not JSON", "get pods -n default", false},
		{"empty string", "", false},
		{"partial JSON", `{"a":1`, false},
		{"number", "42", false},
		{"string", `"hello"`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := tryNormalizeJSON(tt.input)
			assert.Equal(t, tt.expectJSON, ok)
		})
	}
}

func TestTurnToolCallCache_GetPut(t *testing.T) {
	cache := turnToolCallCache{cache: make(map[string]NBAgentPlannerToolActionStep)}

	step := NBAgentPlannerToolActionStep{
		Action:      NBAgentPlannerToolAction{ToolID: "Plan1", Tool: "kubectl", ToolInput: "get pods"},
		Observation: "pod-1 Running",
		Status:      ToolStatusSuccess,
	}

	// Miss before put
	_, found := cache.Get("kubectl", "get pods")
	assert.False(t, found)

	// Put and get
	cache.Put("kubectl", "get pods", step)
	cached, found := cache.Get("kubectl", "get pods")
	assert.True(t, found)
	assert.Equal(t, "pod-1 Running", cached.Observation)

	// Normalized key match (extra whitespace)
	cached, found = cache.Get("kubectl", "get   pods")
	assert.True(t, found, "should match with extra whitespace")
	assert.Equal(t, "pod-1 Running", cached.Observation)

	// Case-insensitive tool name
	cached, found = cache.Get("KUBECTL", "get pods")
	assert.True(t, found, "should match with different tool name case")
}

func TestTurnToolCallCache_Stats(t *testing.T) {
	cache := turnToolCallCache{cache: make(map[string]NBAgentPlannerToolActionStep)}

	step := NBAgentPlannerToolActionStep{
		Action: NBAgentPlannerToolAction{ToolID: "Plan1", Tool: "tool", ToolInput: "input"},
		Status: ToolStatusSuccess,
	}

	// Miss
	cache.Get("tool", "input")
	hits, misses, entries := cache.Stats()
	assert.Equal(t, int64(0), hits)
	assert.Equal(t, int64(1), misses)
	assert.Equal(t, int64(0), entries)

	// Put
	cache.Put("tool", "input", step)
	hits, misses, entries = cache.Stats()
	assert.Equal(t, int64(0), hits)
	assert.Equal(t, int64(1), misses)
	assert.Equal(t, int64(1), entries)

	// Hit
	cache.Get("tool", "input")
	hits, misses, entries = cache.Stats()
	assert.Equal(t, int64(1), hits)
	assert.Equal(t, int64(1), misses)
	assert.Equal(t, int64(1), entries)
}

func TestTurnToolCallCache_ConcurrentAccess(t *testing.T) {
	cache := turnToolCallCache{cache: make(map[string]NBAgentPlannerToolActionStep)}

	step := NBAgentPlannerToolActionStep{
		Action: NBAgentPlannerToolAction{ToolID: "Plan1", Tool: "tool", ToolInput: "input"},
		Status: ToolStatusSuccess,
	}

	cache.Put("tool", "input", step)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, found := cache.Get("tool", "input")
			assert.True(t, found)
		}()
	}
	wg.Wait()

	hits, _, _ := cache.Stats()
	assert.Equal(t, int64(100), hits)
}

func TestTurnToolCallCache_JSONDeduplication(t *testing.T) {
	cache := turnToolCallCache{cache: make(map[string]NBAgentPlannerToolActionStep)}

	step := NBAgentPlannerToolActionStep{
		Action:      NBAgentPlannerToolAction{ToolID: "Plan1", Tool: "api_call", ToolInput: `{"namespace":"default","resource":"pods"}`},
		Observation: "result",
		Status:      ToolStatusSuccess,
	}

	// Store with one key ordering
	cache.Put("api_call", `{"namespace":"default","resource":"pods"}`, step)

	// Retrieve with different key ordering
	cached, found := cache.Get("api_call", `{"resource":"pods","namespace":"default"}`)
	assert.True(t, found, "should match JSON with different key order")
	assert.Equal(t, "result", cached.Observation)
}

func TestTurnToolCallCache_PersistsAcrossPlanRegeneration(t *testing.T) {
	// Simulates the scenario where a plan is regenerated with new ToolIDs
	// but the same tool+input combination. The cache should still return hits.
	cache := turnToolCallCache{cache: make(map[string]NBAgentPlannerToolActionStep)}

	// First plan execution
	step1 := NBAgentPlannerToolActionStep{
		Action:      NBAgentPlannerToolAction{ToolID: "Plan1", Tool: "kubectl", ToolInput: "get pods -n default"},
		Observation: "pod-1 Running\npod-2 Running",
		Status:      ToolStatusSuccess,
	}
	cache.Put("kubectl", "get pods -n default", step1)

	// Simulating plan regeneration: new ToolID, same tool+input
	cached, found := cache.Get("kubectl", "get pods -n default")
	assert.True(t, found, "cache should persist across plan regeneration")
	assert.Equal(t, step1.Observation, cached.Observation)

	// Verify stats show the hit
	hits, _, entries := cache.Stats()
	assert.Equal(t, int64(1), hits)
	assert.Equal(t, int64(1), entries)
}
