package core

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func boolPtr(b bool) *bool { return &b }

// The three switches are independent. This is the combination that motivated
// the grouped shape: keep reading memory, stop writing it, stop reranking it.
func TestMemoryOverrides_WriteAndRerankOffKeepsReading(t *testing.T) {
	q := NBQueryConfig{Memory: &MemoryOverrides{Write: boolPtr(false), Rerank: boolPtr(false)}}
	assert.True(t, q.MemoryReadAllowed(), "read was not mentioned, so it stays on")
	assert.False(t, q.MemoryWriteAllowed())
	assert.False(t, q.MemoryRerankAllowed())
}

// It must survive the wire in that shape, since callers send JSON.
func TestMemoryOverrides_JSONRoundTrip(t *testing.T) {
	var q NBQueryConfig
	require.NoError(t, json.Unmarshal([]byte(`{"memory":{"write":false,"rerank":false}}`), &q))
	require.NotNil(t, q.Memory)
	assert.Nil(t, q.Memory.Read, "an absent key must stay unset, not default to false")
	assert.True(t, q.MemoryReadAllowed())
	assert.False(t, q.MemoryWriteAllowed())
	assert.False(t, q.MemoryRerankAllowed())
}

// Reading off collapses the other two: nothing to rank, and a turn that ran
// without memory must not shape later turns with what it produced.
func TestMemoryOverrides_ReadOffImpliesWriteAndRerankOff(t *testing.T) {
	q := NBQueryConfig{Memory: &MemoryOverrides{Read: boolPtr(false)}}
	assert.False(t, q.MemoryReadAllowed())
	assert.False(t, q.MemoryWriteAllowed())
	assert.False(t, q.MemoryRerankAllowed())
}

// Fill-if-unset per switch, so a turn that set one still inherits the others.
func TestMemoryOverrides_MergeFromPerSwitch(t *testing.T) {
	q := NBQueryConfig{Memory: &MemoryOverrides{Write: boolPtr(true)}}
	q.MergeFrom(NBQueryConfig{Memory: &MemoryOverrides{Write: boolPtr(false), Rerank: boolPtr(false)}})
	assert.True(t, *q.Memory.Write, "an explicit switch must not be overwritten")
	require.NotNil(t, q.Memory.Rerank)
	assert.False(t, *q.Memory.Rerank, "an unset switch inherits")

	var empty NBQueryConfig
	empty.MergeFrom(NBQueryConfig{Memory: &MemoryOverrides{Read: boolPtr(false)}})
	require.NotNil(t, empty.Memory)
	assert.False(t, *empty.Memory.Read)
}

// Merging must not alias. src is usually the conversation's stored config,
// inherited by sub-agents the planner runs concurrently; a shared pointer lets
// one action's merge mutate another's view.
func TestMemoryOverrides_MergeDoesNotAlias(t *testing.T) {
	src := NBQueryConfig{Memory: &MemoryOverrides{Read: boolPtr(false), Write: boolPtr(false)}}

	var q NBQueryConfig
	q.MergeFrom(src)
	require.NotNil(t, q.Memory)
	assert.NotSame(t, src.Memory, q.Memory, "the block must be copied, not shared")
	assert.NotSame(t, src.Memory.Read, q.Memory.Read, "the switches must be copied too")

	// Mutating the merged copy must leave the source untouched.
	*q.Memory.Read = true
	q.Memory.Rerank = boolPtr(false)
	assert.False(t, *src.Memory.Read, "source switch changed through the alias")
	assert.Nil(t, src.Memory.Rerank, "source gained a switch through the alias")

	// And the per-switch path must not alias either.
	partial := NBQueryConfig{Memory: &MemoryOverrides{Read: boolPtr(true)}}
	partial.MergeFrom(src)
	require.NotNil(t, partial.Memory.Write)
	assert.NotSame(t, src.Memory.Write, partial.Memory.Write)
	*partial.Memory.Write = true
	assert.False(t, *src.Memory.Write, "source switch changed via the per-switch merge")
}

// Clone is nil-safe and independent.
func TestMemoryOverrides_Clone(t *testing.T) {
	var nilBlock *MemoryOverrides
	assert.Nil(t, nilBlock.Clone())

	orig := &MemoryOverrides{Read: boolPtr(false)}
	cp := orig.Clone()
	require.NotNil(t, cp.Read)
	assert.False(t, *cp.Read)
	*cp.Read = true
	assert.False(t, *orig.Read, "clone shares a switch with the original")
	assert.Nil(t, cp.Write)
}

// With no override every switch allows, leaving the decision entirely to the
// gate's own deployment / enrolment / consent checks.
func TestMemoryOverrides_AbsentAllowsAll(t *testing.T) {
	var q NBQueryConfig
	assert.True(t, q.MemoryReadAllowed())
	assert.True(t, q.MemoryWriteAllowed())
	assert.True(t, q.MemoryRerankAllowed())
	assert.True(t, q.IsEmpty(), "an absent block must not make the config look set")
	assert.False(t, NBQueryConfig{Memory: &MemoryOverrides{Write: boolPtr(false)}}.IsEmpty())
	assert.True(t, NBQueryConfig{Memory: &MemoryOverrides{}}.IsEmpty(), "an empty block is still no override")
}
