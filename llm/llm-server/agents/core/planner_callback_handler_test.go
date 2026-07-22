package core

import (
	"encoding/json"
	"testing"

	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

// TestMergeToolResponseMetadata_NilInputsReturnNil pins the historical contract:
// a tool that populates neither Metadata nor AdditionalDetails persists NULL on
// the metadata column (not `{}`). SaveConversationToolCall checks `len(blob)==0`
// before binding the JSONB param, so the helper returning nil keeps NULL on disk.
func TestMergeToolResponseMetadata_NilInputsReturnNil(t *testing.T) {
	blob, err := mergeToolResponseMetadata(nil, nil)
	assert.NoError(t, err)
	assert.Nil(t, blob)
}

// TestMergeToolResponseMetadata_OnlyInternalAdditionalDetailsReturnsNil locks in
// the filter: a tool response that ONLY carries internal-routing keys (agent_id,
// client_tools, follow-up handoff) must NOT produce a metadata blob. Persisting
// {"agent_id":"<uuid>"} would leak the in-process routing identifier into the
// downstream analytics query surface (tool_usage.go reads `tc.metadata->>'…'`).
func TestMergeToolResponseMetadata_OnlyInternalAdditionalDetailsReturnsNil(t *testing.T) {
	blob, err := mergeToolResponseMetadata(nil, map[string]any{
		nbToolCallAdditionalDatailsAgentId:         "00000000-0000-0000-0000-000000000001",
		nbToolCallAdditionalDatailsMessageId:       "msg-1",
		nbToolCallAdditionalDatailsQuery:           "what is broken?",
		nbToolCallAdditionalDatailsFollowupRequest: map[string]any{"foo": "bar"},
		toolcore.ToolMessageConfigClientToolsKey:   []any{"client-tool-1"},
	})
	assert.NoError(t, err)
	assert.Nil(t, blob, "all-internal AdditionalDetails must collapse to NULL — none of these keys belong in the persisted JSONB")
}

// TestMergeToolResponseMetadata_MetadataOnlyMatchesLegacyShape proves the
// pre-merge code path (Metadata alone, no AdditionalDetails) still produces the
// exact same on-disk JSON. Any drift here breaks tool_usage.go's
// `tc.metadata->>'execution_duration_ms'` reader, which is the cost-analyser
// data source.
func TestMergeToolResponseMetadata_MetadataOnlyMatchesLegacyShape(t *testing.T) {
	meta := &toolcore.NBToolResponseMetadata{
		ExitStatus:          1,
		ExecutionDurationMs: 1234,
		Stderr:              "permission denied",
	}
	blob, err := mergeToolResponseMetadata(meta, nil)
	assert.NoError(t, err)
	var got map[string]any
	assert.NoError(t, json.Unmarshal(blob, &got))
	assert.Equal(t, float64(1), got["exit_status"])
	assert.Equal(t, float64(1234), got["execution_duration_ms"])
	assert.Equal(t, "permission denied", got["stderr"])
	// `omitempty` fields must not leak as zero values.
	_, hasTruncated := got["truncated"]
	assert.False(t, hasTruncated, "truncated=false should be omitted via omitempty")
	_, hasOriginalLen := got["original_len"]
	assert.False(t, hasOriginalLen, "original_len=0 should be omitted via omitempty")
}

// TestMergeToolResponseMetadata_DelegateTelemetryPersists is the regression
// guard for the bug this helper fixes: delegate_agent emits per-call telemetry
// (iterations_used / tools_used / budget_exhausted / iteration_budget /
// total_steps from agent_delegate.go:192-201) via AdditionalDetails, and PR
// #32334's whole point was to surface that signal. Before the merge, those keys
// were dropped on the floor and the only "metadata" persisted was the typed
// Metadata struct — which delegate_agent doesn't populate at all.
func TestMergeToolResponseMetadata_DelegateTelemetryPersists(t *testing.T) {
	additional := map[string]any{
		nbToolCallAdditionalDatailsAgentId: "00000000-0000-0000-0000-000000000001", // internal — must drop
		"iteration_budget":                 10,
		"iterations_used":                  4,
		"total_steps":                      7,
		"budget_exhausted":                 false,
		"tools_used":                       map[string]any{"kubectl": 2, "fetch_logs": 2},
	}
	blob, err := mergeToolResponseMetadata(nil, additional)
	assert.NoError(t, err)
	var got map[string]any
	assert.NoError(t, json.Unmarshal(blob, &got))

	_, hasAgentID := got["agent_id"]
	assert.False(t, hasAgentID, "internal agent_id key must NOT land in the persisted blob")
	assert.Equal(t, float64(10), got["iteration_budget"])
	assert.Equal(t, float64(4), got["iterations_used"])
	assert.Equal(t, float64(7), got["total_steps"])
	assert.Equal(t, false, got["budget_exhausted"])
	tu, ok := got["tools_used"].(map[string]any)
	assert.True(t, ok, "tools_used must round-trip as a JSON object")
	assert.Equal(t, float64(2), tu["kubectl"])
	assert.Equal(t, float64(2), tu["fetch_logs"])
}

// TestMergeToolResponseMetadata_MetadataWinsOnKeyCollision pins the precedence
// rule: if a tool ever populates AdditionalDetails with a key that collides
// with NBToolResponseMetadata's typed schema, the typed value wins. Otherwise
// a buggy tool could overwrite e.g. `execution_duration_ms` and corrupt the
// downstream latency aggregates in tool_usage.go.
func TestMergeToolResponseMetadata_MetadataWinsOnKeyCollision(t *testing.T) {
	meta := &toolcore.NBToolResponseMetadata{
		ExitStatus:          0,
		ExecutionDurationMs: 999,
	}
	additional := map[string]any{
		"exit_status":           42,     // would corrupt aggregates if it won
		"execution_duration_ms": 111111, // ditto
		"iterations_used":       3,      // legitimately new
	}
	blob, err := mergeToolResponseMetadata(meta, additional)
	assert.NoError(t, err)
	var got map[string]any
	assert.NoError(t, json.Unmarshal(blob, &got))
	assert.Equal(t, float64(0), got["exit_status"], "Metadata.ExitStatus must win on collision")
	assert.Equal(t, float64(999), got["execution_duration_ms"], "Metadata.ExecutionDurationMs must win on collision")
	assert.Equal(t, float64(3), got["iterations_used"], "non-colliding AdditionalDetails key must still land")
}

// TestMergeToolResponseMetadata_MixedInternalAndPublicAdditionalDetails covers
// the realistic delegate_agent shape: a single AdditionalDetails map carrying
// BOTH the internal agent_id (used for refAgentId routing in
// AfterToolCallResponse) AND public telemetry the persisted blob should keep.
// Failure mode this guards against: a regex-style filter that's too broad and
// drops the public keys, or one that's too narrow and lets agent_id leak.
func TestMergeToolResponseMetadata_MixedInternalAndPublicAdditionalDetails(t *testing.T) {
	meta := &toolcore.NBToolResponseMetadata{
		ExitStatus:          0,
		ExecutionDurationMs: 4500,
	}
	additional := map[string]any{
		nbToolCallAdditionalDatailsAgentId:         "sub-agent-uuid", // internal
		nbToolCallAdditionalDatailsFollowupRequest: map[string]any{}, // internal
		toolcore.ToolMessageConfigClientToolsKey:   []any{},          // internal
		"iterations_used":                          4,                // public
		"tools_used":                               map[string]any{"shell": 1, "kubectl": 3},
		"budget_exhausted":                         true,
	}
	blob, err := mergeToolResponseMetadata(meta, additional)
	assert.NoError(t, err)
	var got map[string]any
	assert.NoError(t, json.Unmarshal(blob, &got))

	for _, internal := range []string{
		nbToolCallAdditionalDatailsAgentId,
		nbToolCallAdditionalDatailsFollowupRequest,
		toolcore.ToolMessageConfigClientToolsKey,
	} {
		_, found := got[internal]
		assert.Falsef(t, found, "internal key %q must NOT land in the persisted blob", internal)
	}
	assert.Equal(t, float64(0), got["exit_status"])
	assert.Equal(t, float64(4500), got["execution_duration_ms"])
	assert.Equal(t, float64(4), got["iterations_used"])
	assert.Equal(t, true, got["budget_exhausted"])
	tu, ok := got["tools_used"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, float64(1), tu["shell"])
	assert.Equal(t, float64(3), tu["kubectl"])
}

// TestInternalAdditionalDetailsKeys_StaysInSyncWithCallsites is a structural
// reminder: if you add a new internal AD key to factory_agent.go or
// tool_context.go, add it to internalAdditionalDetailsKeys too — otherwise the
// next contributor will be debugging why a routing UUID leaked into the cost
// analyser.
func TestInternalAdditionalDetailsKeys_StaysInSyncWithCallsites(t *testing.T) {
	for _, k := range []string{
		nbToolCallAdditionalDatailsAgentId,
		nbToolCallAdditionalDatailsMessageId,
		nbToolCallAdditionalDatailsQuery,
		nbToolCallAdditionalDatailsFollowupRequest,
		toolcore.ToolMessageConfigClientToolsKey,
		toolcore.ToolMessageConfigToolConfigsKey,
		toolcore.ToolMessageConfigCapabilitiesKey,
		toolcore.ToolMessageConfigToolConfirmationKey,
		toolcore.ToolMessageConfigToolConfigMetadataKey,
	} {
		_, ok := internalAdditionalDetailsKeys[k]
		assert.Truef(t, ok, "internalAdditionalDetailsKeys must include %q (defined in factory_agent.go or tool_context.go)", k)
	}
}

// TestMergeToolResponseMetadata_FiltersClientToolWaitingKeys pins the
// orchestration-key filter added after the initial review: when the planner
// emits a "Waiting for client to execute tool" response
// (executor_planner.go:2009-2012, 2092, 2473, 1477-1479), it stamps tool_name
// / tool_input / tool_id / followupId into AdditionalDetails so the resume
// flow can pick them up. tool_input in particular carries LLM-generated
// function-call arguments and sometimes user-typed content — none of which
// belongs in the metadata analytics surface. These keys are filtered as
// aggressively as the routing UUIDs.
func TestMergeToolResponseMetadata_FiltersClientToolWaitingKeys(t *testing.T) {
	blob, err := mergeToolResponseMetadata(nil, map[string]any{
		"tool_name":       "internal_runbook_step",
		"tool_input":      map[string]any{"user_email": "a@b.c", "free_text": "<llm-generated args>"},
		"tool_id":         "tc-abc-123",
		"followupId":      "fup-xyz-789",
		"iterations_used": 2, // legitimate public telemetry — MUST survive
	})
	assert.NoError(t, err)
	var got map[string]any
	assert.NoError(t, json.Unmarshal(blob, &got))
	for _, k := range []string{"tool_name", "tool_input", "tool_id", "followupId"} {
		_, found := got[k]
		assert.Falsef(t, found, "%q is orchestration state, not telemetry — must NOT land in metadata", k)
	}
	assert.Equal(t, float64(2), got["iterations_used"], "non-orchestration AD key must still land alongside")
}

// TestMergeToolResponseMetadata_FallsBackToMetadataOnAdMarshalFail is the
// regression guard for the marshal-fail concern surfaced in self-review: a
// future tool could put an unmarshalable value (channel, func, complex
// struct) in AdditionalDetails. Pre-merge, that input only dropped AD; the
// Metadata typed telemetry was unaffected because it took an independent
// marshal path. The merge step joins them, so without the fallback BOTH
// would now be lost. The helper must isolate the failure — Metadata-only
// blob is the correct degradation, matching pre-merge behavior.
func TestMergeToolResponseMetadata_FallsBackToMetadataOnAdMarshalFail(t *testing.T) {
	meta := &toolcore.NBToolResponseMetadata{
		ExitStatus:          0,
		ExecutionDurationMs: 1234,
	}
	additional := map[string]any{
		"iterations_used":  3,
		"unmarshalable_ch": make(chan int), // encoding/json refuses to marshal channels
	}
	blob, err := mergeToolResponseMetadata(meta, additional)
	assert.NoError(t, err, "marshal-fail on AD must NOT propagate as a hard error — Metadata fallback should absorb it")
	assert.NotNil(t, blob, "fallback must produce a Metadata-only blob, not NULL")
	var got map[string]any
	assert.NoError(t, json.Unmarshal(blob, &got))
	assert.Equal(t, float64(0), got["exit_status"])
	assert.Equal(t, float64(1234), got["execution_duration_ms"])
	_, hasIter := got["iterations_used"]
	assert.False(t, hasIter, "fallback path is Metadata-only; AD keys are dropped — corollary: a misbehaving tool loses its public AD telemetry but keeps the typed contract")
}

// TestMergeToolResponseMetadata_FallsBackHardWhenMetadataIsNil locks in the
// other half of the fallback semantics: if there's no Metadata to fall back
// to and the merged map can't be marshalled, surface the error so the caller
// can log it. The previous helper version swallowed this case silently.
func TestMergeToolResponseMetadata_FallsBackHardWhenMetadataIsNil(t *testing.T) {
	blob, err := mergeToolResponseMetadata(nil, map[string]any{
		"iterations_used":  3,
		"unmarshalable_ch": make(chan int),
	})
	assert.Error(t, err, "no Metadata to fall back to + AD marshal-fail must surface an error")
	assert.Nil(t, blob)
}
