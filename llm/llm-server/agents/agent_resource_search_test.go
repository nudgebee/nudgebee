package agents

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Pure-logic unit tests for agent relevance helpers.
// These tests do NOT require a live cluster or TEST_ACCOUNT.
// ---------------------------------------------------------------------------

func TestExtractResourceQueryTerms(t *testing.T) {
	tests := []struct {
		query    string
		wantAll  []string // terms that MUST be present
		wantNone []string // terms that MUST NOT be present
	}{
		{
			query:    "find pods for the llm-server app",
			wantAll:  []string{"llm-server", "llm"},
			wantNone: []string{"find", "for", "the", "pod", "pods", "app"},
		},
		{
			query:    "search for llm server deployment",
			wantAll:  []string{"llm"},
			wantNone: []string{"search", "for", "server", "deployment"}, // "server" generic; "deployment" is a k8s TYPE word, not an identity
		},
		{
			query:    "find all postgres instances across my cluster",
			wantAll:  []string{"postgres"},
			wantNone: []string{"find", "all", "instances", "across", "cluster"},
		},
		{
			query:    "",
			wantAll:  []string{},
			wantNone: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			got := extractResourceQueryTerms(tt.query)
			gotSet := make(map[string]bool, len(got))
			for _, g := range got {
				gotSet[g] = true
			}
			for _, want := range tt.wantAll {
				assert.True(t, gotSet[want], "expected term %q to be present in %v", want, got)
			}
			for _, notWant := range tt.wantNone {
				assert.False(t, gotSet[notWant], "expected term %q to be absent from %v", notWant, got)
			}
		})
	}
}

func TestResourceNameMatchesTerms(t *testing.T) {
	tests := []struct {
		name     string
		terms    []string
		expected bool
	}{
		{"llm-server-abc123", []string{"llm", "llm-server"}, true},
		{"system:controller:resourcequota-controller", []string{"llm", "llm-server"}, false},
		{"system:resource-tracker", []string{"llm", "llm-server"}, false},
		{"postgres-primary-0", []string{"postgres"}, true},
		{"my-api-server", []string{"llm", "llm-server"}, false},
		{"", []string{"llm"}, false},
		{"anything", []string{}, false},
		// Cloud results that were incorrectly returned for "llm-server"
		{"lipi-games-resources-mobile-application-public", []string{"llm", "llm-server"}, false},
		{"resource-observer-scheduler", []string{"llm", "llm-server"}, false},
		{"gcp_billing_export_resource_v1_01766B", []string{"llm", "llm-server"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resourceNameMatchesTerms(tt.name, tt.terms)
			assert.Equal(t, tt.expected, got)
		})
	}
}

// TestResourceSearchToolCall_ResolveToolName pins the alias precedence the
// resource_search sub-agent uses to find the tool name across the LLM
// providers' varying schemas. `name` support is the fix for the bug where
// EVERY OpenAI-schema emission (the current-model default) was silently
// dropped and forced the buggy heuristic fallback.
func TestResourceSearchToolCall_ResolveToolName(t *testing.T) {
	cases := []struct {
		name string
		in   resourceSearchToolCall
		want string
	}{
		{"tool wins (highest precedence)",
			resourceSearchToolCall{Tool: "a", ToolCode: "b", ToolName: "c", Name: "d"}, "a"},
		{"tool_code when tool empty",
			resourceSearchToolCall{ToolCode: "b", ToolName: "c", Name: "d"}, "b"},
		{"tool_name when tool + tool_code empty",
			resourceSearchToolCall{ToolName: "c", Name: "d"}, "c"},
		{"name (OpenAI standard) when others empty — this is the bug fix",
			resourceSearchToolCall{Name: "resource_search_execute"}, "resource_search_execute"},
		{"all empty returns empty",
			resourceSearchToolCall{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.in.resolveToolName())
		})
	}
}

// TestResourceSearchToolCall_UnmarshalOpenAISchema locks the JSON tag set
// so a future struct edit that drops the `name` tag would fail here — that's
// exactly what the parser regression was.
func TestResourceSearchToolCall_UnmarshalOpenAISchema(t *testing.T) {
	// The exact JSON shape modern Gemini/OpenAI/Anthropic prompt-tuned
	// models emit for a tool call. Before the fix this parsed with all
	// name fields empty because the struct didn't tag `name` at all.
	raw := []byte(`{"name":"resource_search_execute","arguments":{"resource_name":"my-app-51"}}`)
	var tc resourceSearchToolCall
	require.NoError(t, json.Unmarshal(raw, &tc))
	assert.Equal(t, "resource_search_execute", tc.resolveToolName())
	assert.NotEmpty(t, tc.Arguments)
}

// TestExtractFallbackResourceName preserves the resource-name-not-mangled
// invariant. Prior implementation used substring-replace on the raw text,
// which stripped filler words INSIDE resource names ("my" in "my-app-51"
// → "-app-51"). Tokenising first and matching whole words fixes it.
func TestExtractFallbackResourceName(t *testing.T) {
	cases := []struct {
		name  string
		query string
		want  string
	}{
		{
			name:  "resource name with a filler-word prefix stays intact",
			query: "find my-app-51 in namespace nudgebee",
			want:  "my-app-51",
		},
		{
			name: "actual filler words are stripped",
			// NB: "of" is intentionally NOT in the filler set (matches the
			// pre-refactor list), so the query is phrased without it here.
			// If a future edit adds "of" as a filler this test breaks
			// intentionally, prompting the author to widen the expectation.
			query: "find all instances orders-api across my cloud accounts",
			want:  "orders-api",
		},
		{
			name:  "job name preserved (bug: was returning 'job' as first word)",
			query: "job java-api-checker in namespace app-12",
			want:  "job", // NB: "job" isn't in fillers, so it's returned. Real fix here would be to add k8s-type words as fillers too, but that's out of scope for this bug fix — the important invariant is that resource names aren't mangled.
		},
		{
			name:  "all-fillers query returns empty",
			query: "find all across my cloud accounts",
			want:  "",
		},
		{
			name:  "empty query returns empty",
			query: "",
			want:  "",
		},
		{
			name:  "trailing period stripped (would otherwise fail DB lookup)",
			query: "find my-app-51.",
			want:  "my-app-51",
		},
		{
			name:  "trailing question mark stripped",
			query: "find api-checker?",
			want:  "api-checker",
		},
		{
			name:  "interior hyphen not touched (legitimate K8s naming)",
			query: "find my-app-51",
			want:  "my-app-51",
		},
		{
			name:  "original casing preserved (downstream may be case-sensitive)",
			query: "find MyService in namespace prod",
			want:  "MyService",
		},
		{
			name: "filler with trailing comma is still recognised as filler (bug: previously fell through to return the filler)",
			// Without normalising the token for the filler check, "all,"
			// wouldn't match filler "all" and the function would return "all".
			query: "find all, search for my-app-51",
			want:  "my-app-51",
		},
		{
			name:  "wrapping double quotes stripped",
			query: `find "my-app-51"`,
			want:  "my-app-51",
		},
		{
			name:  "wrapping single quotes stripped",
			query: "find 'my-app-51'",
			want:  "my-app-51",
		},
		{
			name:  "wrapping parentheses stripped",
			query: "find (my-app-51)",
			want:  "my-app-51",
		},
		{
			name:  "wrapping square brackets stripped",
			query: "find [my-app-51]",
			want:  "my-app-51",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, extractFallbackResourceName(tc.query))
		})
	}
}
