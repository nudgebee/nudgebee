package core

// Unit tests for the memory-attribution parser + helper wired into
// planner_react_shared.go. Pure parsing — no DB, no planner runtime.
// End-to-end coverage (parse from real ReAct output → SaveConversationToolCall
// → hydration SELECT → UI) lives in a separate integration test if we add
// one; that requires a live PG + reference-log seed and isn't part of the
// unit surface.

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMemoryUsedFromActionContent_HappyPath(t *testing.T) {
	// Realistic <action> body: tool + input, then a <memory_used> with two
	// distinct refs. Verifies (a) positional int extraction, (b) note
	// capture, (c) preserved emission order.
	action := `
<tool_name>loki_query</tool_name>
<tool_input>{"query": "orders-api errors last 1h", "source": "loki"}</tool_input>
<memory_used>
  <ref n="2" note="preferred_log_source=loki drove tool choice" />
  <ref n="4" note="pattern match — orders-api OOMKill history" />
</memory_used>
`
	refs := parseMemoryUsedFromActionContent(action)
	require.Len(t, refs, 2)
	assert.Equal(t, 2, refs[0].Position)
	assert.Equal(t, "preferred_log_source=loki drove tool choice", refs[0].Note)
	assert.Equal(t, 4, refs[1].Position)
	assert.Equal(t, "pattern match — orders-api OOMKill history", refs[1].Note)
}

func TestParseMemoryUsedFromActionContent_NoTag_ReturnsNil(t *testing.T) {
	// Absence of <memory_used> is the common case — most actions won't cite
	// memory. Must be nil (not empty slice) so downstream JSON marshalling
	// omits the field cleanly (`memory_refs,omitempty`).
	action := `<tool_name>kubectl_get</tool_name><tool_input>pod orders-api</tool_input>`
	assert.Nil(t, parseMemoryUsedFromActionContent(action))
}

func TestParseMemoryUsedFromActionContent_EmptyBlock_ReturnsNil(t *testing.T) {
	// <memory_used></memory_used> with no children — treat as "the LLM
	// emitted the tag but had nothing to cite". Not an error; just nil.
	action := `<tool_name>x</tool_name><memory_used>  </memory_used>`
	assert.Nil(t, parseMemoryUsedFromActionContent(action))
}

func TestParseMemoryUsedFromActionContent_MalformedRef_SkippedIndividually(t *testing.T) {
	// One good ref + one malformed (missing n=) + one with non-numeric n.
	// Malformed refs must drop individually — the good ref survives. This
	// is the "defensive parse" contract: attribution is best-effort audit
	// data, we never fail the action on a bad ref tag.
	action := `
<tool_name>x</tool_name>
<memory_used>
  <ref n="3" note="valid" />
  <ref note="missing n attr" />
  <ref n="notanint" note="bad n" />
  <ref n="-2" note="non-positive n" />
</memory_used>
`
	refs := parseMemoryUsedFromActionContent(action)
	require.Len(t, refs, 1)
	assert.Equal(t, 3, refs[0].Position)
	assert.Equal(t, "valid", refs[0].Note)
}

func TestParseMemoryUsedFromActionContent_DuplicateN_Deduped(t *testing.T) {
	// LLM emits the same position twice — should count once, keep the
	// first-seen note. Prevents inflated used-counts downstream when the
	// model is repetitive.
	action := `
<tool_name>x</tool_name>
<memory_used>
  <ref n="5" note="first" />
  <ref n="5" note="second (dup)" />
</memory_used>
`
	refs := parseMemoryUsedFromActionContent(action)
	require.Len(t, refs, 1)
	assert.Equal(t, 5, refs[0].Position)
	assert.Equal(t, "first", refs[0].Note, "duplicate position keeps the first-seen note")
}

func TestParseMemoryUsedFromActionContent_SelfClosingAndFullTag_BothWork(t *testing.T) {
	// LLMs vary on self-closing vs full-open/close for empty elements.
	// Both shapes must parse — the regex is deliberately tolerant.
	action := `
<memory_used>
  <ref n="1" note="self-closing"/>
  <ref n="2" note="open+close" ></ref>
</memory_used>
`
	refs := parseMemoryUsedFromActionContent(action)
	require.Len(t, refs, 2)
	assert.Equal(t, 1, refs[0].Position)
	assert.Equal(t, 2, refs[1].Position)
}

func TestParseMemoryUsedFromActionContent_NoNote_PositionCaptured(t *testing.T) {
	// note is optional — a bare <ref n="7"/> should still be captured with
	// an empty Note. Signals "the LLM cited this row but didn't explain".
	action := `<memory_used><ref n="7"/></memory_used>`
	refs := parseMemoryUsedFromActionContent(action)
	require.Len(t, refs, 1)
	assert.Equal(t, 7, refs[0].Position)
	assert.Empty(t, refs[0].Note)
}

func TestParseMemoryUsedFromActionContent_EmptyInput(t *testing.T) {
	assert.Nil(t, parseMemoryUsedFromActionContent(""))
}

func TestParseMemoryUsedFromActionContent_SlashInNote_PreservesRef(t *testing.T) {
	// Regression for the `[^/>]*?` attr-class bug: a slash inside a quoted
	// note value used to terminate attribute matching, dropping the whole
	// ref. Realistic — LLMs commonly cite path-like tokens ("path/to/pod",
	// "and/or") in the note text.
	action := `<memory_used>
  <ref n="7" note="preferred_log_source=path/to/loki" />
</memory_used>`
	refs := parseMemoryUsedFromActionContent(action)
	require.Len(t, refs, 1)
	assert.Equal(t, 7, refs[0].Position)
	assert.Equal(t, "preferred_log_source=path/to/loki", refs[0].Note)
}

func TestParseMemoryUsedFromActionContent_GreaterThanInNote_PreservesRef(t *testing.T) {
	// Regression for the `[^>]*?` attr-class: a `>` inside a quoted note
	// value used to terminate attribute matching and drop the ref. LLMs
	// commonly cite comparisons ("value > 5", "latency > 500ms") or
	// arrows ("x -> y") in the note text.
	action := `<memory_used>
  <ref n="9" note="latency > 500ms triggered the p99 alert" />
</memory_used>`
	refs := parseMemoryUsedFromActionContent(action)
	require.Len(t, refs, 1)
	assert.Equal(t, 9, refs[0].Position)
	assert.Equal(t, "latency > 500ms triggered the p99 alert", refs[0].Note)
}

func TestParseMemoryUsedFromActionContent_SingleQuotedAttrs(t *testing.T) {
	// Smaller models sometimes emit single-quoted attrs even though the
	// prompt shows double quotes. Both must parse — pinning the dual-
	// quote regex support.
	action := `<memory_used>
  <ref n='3' note='pattern match' />
  <ref n="11" note="mixed dbl-quote still works" />
</memory_used>`
	refs := parseMemoryUsedFromActionContent(action)
	require.Len(t, refs, 2)
	assert.Equal(t, 3, refs[0].Position)
	assert.Equal(t, "pattern match", refs[0].Note)
	assert.Equal(t, 11, refs[1].Position)
	assert.Equal(t, "mixed dbl-quote still works", refs[1].Note)
}
