package core

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestToolCallStatsNilIsNoOp pins the nil-receiver contract: tools that don't
// record, and call sites that build an NbToolContext without a Stats pointer,
// must not panic on the shared code path.
func TestToolCallStatsNilIsNoOp(t *testing.T) {
	var s *ToolCallStats
	assert.NotPanics(t, func() {
		s.RecordDB("select 1", "1 row", nil, time.Second)
		s.RecordRelay("kubectl get pods", "out", nil, time.Second)
	})
	assert.Nil(t, s.ApplyTo(nil))
	steps, dropped := s.Steps()
	assert.Empty(t, steps)
	assert.Zero(t, dropped)
}

// TestToolCallStatsConcurrentRecording is the reason the counters are atomic and
// the step slice is mutex-guarded: resource_search fans its search strategies out
// across goroutines that all share one accumulator, so writes race by construction.
func TestToolCallStatsConcurrentRecording(t *testing.T) {
	s := &ToolCallStats{}
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(2)
		go func() { defer wg.Done(); s.RecordDB(fmt.Sprintf("q%d", i), "1 row", nil, 2*time.Millisecond) }()
		go func() { defer wg.Done(); s.RecordRelay(fmt.Sprintf("kubectl %d", i), "out", nil, 4*time.Millisecond) }()
	}
	wg.Wait()

	meta := s.ApplyTo(nil)
	require.NotNil(t, meta)
	assert.Equal(t, 20, meta.DBQueries)
	assert.Equal(t, 20, meta.RelayCalls)
	// Busy time is the sum of every call; elapsed is the span they occupied. With
	// 20 concurrent calls the sum must exceed the wall clock — that gap is the
	// whole reason the two are reported separately.
	assert.Equal(t, int64(40), meta.DBBusyMs)
	assert.Equal(t, int64(80), meta.RelayBusyMs)
	assert.Less(t, meta.RelayMs, meta.RelayBusyMs)
	assert.Len(t, meta.Steps, 40)
}

// TestToolCallStatsCapsSteps verifies the cap counts what it drops rather than
// silently truncating — a clipped list presented as complete is worse than no
// list, because it reads as "that's all that ran".
func TestToolCallStatsCapsSteps(t *testing.T) {
	s := &ToolCallStats{}
	for i := range maxRecordedSteps + 6 {
		s.RecordRelay(fmt.Sprintf("kubectl get pods %d", i), "out", nil, time.Millisecond)
	}
	steps, dropped := s.Steps()
	assert.Len(t, steps, maxRecordedSteps)
	assert.Equal(t, 6, dropped)

	meta := s.ApplyTo(nil)
	// Counters still reflect every call, only the step list is capped.
	assert.Equal(t, maxRecordedSteps+6, meta.RelayCalls)
	assert.Equal(t, 6, meta.StepsDropped)
	// The span must cover dropped calls too, or elapsed time under-reports.
	assert.Equal(t, int64(maxRecordedSteps+6), meta.RelayBusyMs)
}

// TestToolCallStatsRecordsFailures keeps a failed step in the list — a command
// that errored is usually the one being debugged.
func TestToolCallStatsRecordsFailures(t *testing.T) {
	s := &ToolCallStats{}
	s.RecordRelay("kubectl get pods -n missing", "", errors.New("namespace not found"), 5*time.Millisecond)

	steps, _ := s.Steps()
	require.Len(t, steps, 1)
	assert.Equal(t, "relay", steps[0].Kind)
	assert.Equal(t, "kubectl get pods -n missing", steps[0].Command)
	assert.Equal(t, "namespace not found", steps[0].Err)
}

// TestToolCallStatsApplyToPreservesExistingMetadata verifies the counters are
// merged into a tool's own metadata rather than replacing it — kubectl sets
// Stderr, and losing it would trade one blind spot for another.
func TestToolCallStatsApplyToPreservesExistingMetadata(t *testing.T) {
	s := &ToolCallStats{}
	s.RecordRelay("kubectl get pods", "out", nil, 3*time.Millisecond)

	meta := s.ApplyTo(&NBToolResponseMetadata{Stderr: "boom", ExitStatus: 1})

	require.NotNil(t, meta)
	assert.Equal(t, "boom", meta.Stderr)
	assert.Equal(t, 1, meta.ExitStatus)
	assert.Equal(t, 1, meta.RelayCalls)
}

// TestToolCallStatsUnusedStaysAbsent keeps the persisted JSONB clean: a tool
// that attaches an accumulator but never records shouldn't write zero-valued
// counters that read as "0 DB queries" rather than "not measured".
func TestToolCallStatsUnusedStaysAbsent(t *testing.T) {
	s := &ToolCallStats{}
	assert.Nil(t, s.ApplyTo(nil))

	existing := &NBToolResponseMetadata{ExitStatus: 0}
	assert.Equal(t, existing, s.ApplyTo(existing))
}
