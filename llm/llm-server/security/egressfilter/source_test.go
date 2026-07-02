package egressfilter

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tmc/langchaingo/llms"
)

func TestSourceForRole(t *testing.T) {
	cases := []struct {
		role llms.ChatMessageType
		want Source
	}{
		{llms.ChatMessageTypeSystem, SourceSystem},
		{llms.ChatMessageTypeHuman, SourceUser},
		{llms.ChatMessageTypeAI, SourceAssistant},
		{llms.ChatMessageTypeTool, SourceToolResult},
		{llms.ChatMessageType("unknown_role"), SourceUser}, // conservative fallback
	}
	for _, c := range cases {
		assert.Equal(t, c.want, sourceForRole(c.role), "role=%q", c.role)
	}
}

func TestSerializeMessagesWithSources_TextRoleMapping(t *testing.T) {
	msgs := []llms.MessageContent{
		{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{llms.TextContent{Text: "sys"}}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "user"}}},
		{Role: llms.ChatMessageTypeAI, Parts: []llms.ContentPart{llms.TextContent{Text: "asst"}}},
	}
	_, regions := serializeMessagesWithSources(msgs)
	if assert.Len(t, regions, 3) {
		assert.Equal(t, SourceSystem, regions[0].Source)
		assert.Equal(t, SourceUser, regions[1].Source)
		assert.Equal(t, SourceAssistant, regions[2].Source)
	}
}

func TestSerializeMessagesWithSources_TypeBasedOverridesRole(t *testing.T) {
	// Tool/Image parts get tagged by type, not role, so a Human-role message
	// containing a ToolCall part still tags as tool_call_args.
	msgs := []llms.MessageContent{
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{
			llms.ToolCall{FunctionCall: &llms.FunctionCall{Arguments: `{"x":1}`}},
		}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{
			llms.ToolCallResponse{Content: "tool out"},
		}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{
			llms.ImageURLContent{URL: "https://s3/foo?X-Amz-Credential=x"},
		}},
	}
	_, regions := serializeMessagesWithSources(msgs)
	if assert.Len(t, regions, 3) {
		assert.Equal(t, SourceToolCallArgs, regions[0].Source)
		assert.Equal(t, SourceToolResult, regions[1].Source)
		assert.Equal(t, SourceImageURL, regions[2].Source)
	}
}

func TestSerializeMessagesWithSources_RegionsTilePayload(t *testing.T) {
	// Each region's [start, end) must cover up to and including the newline
	// separator, so the regions tile the payload — no gaps.
	msgs := []llms.MessageContent{
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "abc"}}},
		{Role: llms.ChatMessageTypeAI, Parts: []llms.ContentPart{llms.TextContent{Text: "xy"}}},
	}
	payload, regions := serializeMessagesWithSources(msgs)
	assert.Equal(t, "abc\nxy\n", payload)
	if assert.Len(t, regions, 2) {
		assert.Equal(t, 0, regions[0].Start)
		assert.Equal(t, 4, regions[0].End) // "abc\n"
		assert.Equal(t, 4, regions[1].Start)
		assert.Equal(t, 7, regions[1].End) // "xy\n"
	}
}

func TestSerializeMessagesWithSources_EmptyTextSkipsRegion(t *testing.T) {
	msgs := []llms.MessageContent{
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: ""}}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{llms.TextContent{Text: "real"}}},
	}
	payload, regions := serializeMessagesWithSources(msgs)
	assert.Equal(t, "real\n", payload)
	assert.Len(t, regions, 1)
}

func TestTagHitsBySource_MatchesContainingRegion(t *testing.T) {
	regions := []SourceRegion{
		{Start: 0, End: 5, Source: SourceSystem},
		{Start: 5, End: 12, Source: SourceUser},
		{Start: 12, End: 20, Source: SourceToolResult},
	}
	hits := []Hit{
		{RuleID: "x", Start: 2, End: 4},   // inside system
		{RuleID: "x", Start: 7, End: 10},  // inside user
		{RuleID: "x", Start: 15, End: 18}, // inside tool_result
		{RuleID: "x", Start: 5, End: 7},   // start of user region (boundary)
	}
	out := tagHitsBySource(hits, regions)
	assert.Equal(t, SourceSystem, out[0].Source)
	assert.Equal(t, SourceUser, out[1].Source)
	assert.Equal(t, SourceToolResult, out[2].Source)
	assert.Equal(t, SourceUser, out[3].Source, "Start == region.Start must tag as that region")
}

func TestTagHitsBySource_UnmappedStaysEmpty(t *testing.T) {
	regions := []SourceRegion{
		{Start: 0, End: 5, Source: SourceSystem},
	}
	hits := []Hit{{RuleID: "x", Start: 100, End: 105}} // far outside any region
	out := tagHitsBySource(hits, regions)
	assert.Equal(t, Source(""), out[0].Source, "hit outside all regions must remain untagged")
}

func TestTagHitsBySource_NoRegionsOrNoHitsIsNoop(t *testing.T) {
	// No regions: hits returned unchanged.
	hits := []Hit{{RuleID: "x", Start: 0, End: 3}}
	out := tagHitsBySource(hits, nil)
	assert.Equal(t, Source(""), out[0].Source)
	// No hits: nil slice returned cleanly.
	assert.Nil(t, tagHitsBySource(nil, []SourceRegion{{Start: 0, End: 5, Source: SourceUser}}))
}

func TestDistinctSources_DedupAndSort(t *testing.T) {
	hits := []Hit{
		{Source: SourceUser},
		{Source: SourceToolResult},
		{Source: SourceUser},       // dup
		{Source: ""},               // empty must be dropped
		{Source: SourceToolResult}, // dup
		{Source: SourceSystem},
	}
	got := distinctSources(hits)
	assert.Equal(t, []Source{SourceSystem, SourceToolResult, SourceUser}, got)
}

func TestDistinctSources_EmptyOrAllUntagged(t *testing.T) {
	assert.Nil(t, distinctSources(nil))
	assert.Nil(t, distinctSources([]Hit{{Source: ""}, {Source: ""}}))
}

// TestWithAgentName_AndAccessor pins the contract for the agent-name
// context plumbing: empty input is a no-op, non-empty is round-trippable.
func TestWithAgentName_AndAccessor(t *testing.T) {
	base := context.Background()

	// Empty input → ctx unchanged + accessor returns false.
	out := WithAgentName(base, "")
	_, ok := AgentNameFromContext(out)
	assert.False(t, ok)
	assert.Equal(t, base, out)

	// Non-empty input → round-trippable.
	out = WithAgentName(base, "k8s_debug")
	name, ok := AgentNameFromContext(out)
	assert.True(t, ok)
	assert.Equal(t, "k8s_debug", name)
}

// TestWrapModel_FilterEvent_CarriesSourceAndAgent is the end-to-end check
// that pins what dashboards will see: each Hit on the FilterEvent carries
// the Source of the part that produced it, the aggregated HitSources is
// populated, and AgentName is propagated when the wire-up has attached
// it to ctx. Without this test the rest of the wiring could silently
// drop the source tag without an existing-test regression.
func TestWrapModel_FilterEvent_CarriesSourceAndAgent(t *testing.T) {
	inner := &fakeModel{respText: "ok"}
	wrapped := WrapModel(inner, "openai", "gpt-4o", true, ModeDetect)

	var captured FilterEvent
	ctx := WithFilterEventReporter(context.Background(), func(e FilterEvent) {
		captured = e
	})
	ctx = WithAgentName(ctx, "k8s_debug")

	// Three different-source secrets: user message, tool result, system prompt.
	msgs := []llms.MessageContent{
		{Role: llms.ChatMessageTypeSystem, Parts: []llms.ContentPart{
			llms.TextContent{Text: "system prompt mentions ghp_Abcdefghijklmnopqrstuvwxyz0123456789"},
		}},
		{Role: llms.ChatMessageTypeHuman, Parts: []llms.ContentPart{
			llms.TextContent{Text: "AKIAIOSFODNN7EXAMPLE plus question"},
		}},
		{Role: llms.ChatMessageTypeTool, Parts: []llms.ContentPart{
			llms.ToolCallResponse{Content: "ghp_Zyxwvutsrqponmlkjihgfedcba0123456789 from tool"},
		}},
	}
	_, err := wrapped.GenerateContent(ctx, msgs)
	assert.NoError(t, err) // detect mode never blocks

	// AgentName threaded through.
	assert.Equal(t, "k8s_debug", captured.AgentName)

	// Per-hit Source set on every hit, never empty for this test (every
	// hit landed inside a region).
	if assert.NotEmpty(t, captured.Hits) {
		for _, h := range captured.Hits {
			assert.NotEqual(t, Source(""), h.Source,
				"every hit must be source-tagged (rule=%s, start=%d)", h.RuleID, h.Start)
		}
	}

	// Aggregated HitSources sorted + deduped. We expect exactly the three
	// sources we wrote: system, user, tool_result.
	assert.Equal(t,
		[]Source{SourceSystem, SourceToolResult, SourceUser},
		captured.HitSources)
}
