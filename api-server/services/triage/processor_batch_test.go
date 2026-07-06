package triage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildCorrelationInsert_MatchesPerCandidateSemantics asserts the batched
// multi-row INSERT binds exactly the arguments the old per-candidate
// insertCorrelation wrote: two rows per candidate (both directions), the second
// with a negated time offset, sequentially numbered placeholders, and the
// idempotent ON CONFLICT clause. This is the correctness proof for the N+1 fix —
// a batching bug (off-by-one placeholder, swapped ids, un-negated offset) would
// surface here rather than silently corrupting event_correlations.
func TestBuildCorrelationInsert_MatchesPerCandidateSemantics(t *testing.T) {
	triaged := makeEvent("T", time.Now())
	triaged.CloudAccountId = strPtr("acct-1")
	triaged.Tenant = strPtr("tenant-1")

	c1 := makeEvent("C1", time.Now())
	c1.Fingerprint = strPtr("fp1")
	c2 := makeEvent("C2", time.Now())
	c2.Fingerprint = strPtr("fp2")

	candidates := []correlatedCandidate{
		{event: c1, result: CorrelationResult{
			CorrelationType: "downstream_impact", CorrelationScore: 0.70,
			CorrelationReason: "r1", TimeOffsetMinutes: 5, DependencyDistance: 1,
		}},
		{event: c2, result: CorrelationResult{
			CorrelationType: "same_service", CorrelationScore: 0.90,
			CorrelationReason: "r2", TimeOffsetMinutes: -3, DependencyDistance: 2,
		}},
	}

	query, args := buildCorrelationInsert(triaged, candidates)

	// 2 candidates × 2 directions = 4 rows × 9 cols = 36 args.
	require.Len(t, args, 4*correlationInsertCols)
	assert.Contains(t, query, "ON CONFLICT (related_event_id, event_id, cloud_account_id) DO NOTHING")
	// Each value tuple ends with its 9th placeholder ($9, $18, $27, $36): four
	// sequentially-numbered tuples, no gaps or repeats.
	for _, end := range []string{"$9)", "$18)", "$27)", "$36)"} {
		assert.Contains(t, query, end, "expected a value tuple ending at "+end)
	}

	// Extract the 9 args for row r (0-indexed).
	row := func(r int) []interface{} { return args[r*correlationInsertCols : (r+1)*correlationInsertCols] }

	// Row 0 — direction 1 for c1: triaged -> candidate, forward offset.
	assertRow(t, row(0), "T", "C1", "acct-1", "tenant-1", "downstream_impact", 0.70, "r1", 5, 1)
	// Row 1 — direction 2 for c1: candidate -> triaged, NEGATED offset.
	assertRow(t, row(1), "C1", "T", "acct-1", "tenant-1", "downstream_impact", 0.70, "r1", -5, 1)
	// Row 2 — direction 1 for c2.
	assertRow(t, row(2), "T", "C2", "acct-1", "tenant-1", "same_service", 0.90, "r2", -3, 2)
	// Row 3 — direction 2 for c2: negated offset -(-3) = 3.
	assertRow(t, row(3), "C2", "T", "acct-1", "tenant-1", "same_service", 0.90, "r2", 3, 2)
}

func TestBuildCorrelationInsert_Empty(t *testing.T) {
	triaged := makeEvent("T", time.Now())
	triaged.CloudAccountId = strPtr("acct-1")
	triaged.Tenant = strPtr("tenant-1")

	query, args := buildCorrelationInsert(triaged, nil)
	assert.Empty(t, query)
	assert.Nil(t, args)
}

// TestFilterNewCorrelations_DropsAlreadyCorrelatedFingerprints asserts the dedup
// filter drops candidates whose fingerprint pair already exists and keeps the
// rest — including candidates with a nil fingerprint (which can never be in the
// existing set), matching the old per-candidate skip behavior.
func TestFilterNewCorrelations_DropsAlreadyCorrelatedFingerprints(t *testing.T) {
	c1 := makeEvent("C1", time.Now())
	c1.Fingerprint = strPtr("fp1")
	c2 := makeEvent("C2", time.Now())
	c2.Fingerprint = strPtr("fp2")
	c3 := makeEvent("C3", time.Now()) // nil fingerprint

	correlated := []correlatedCandidate{
		{event: c1}, {event: c2}, {event: c3},
	}
	existing := map[string]bool{"fp1": true}

	kept := filterNewCorrelations(correlated, existing)

	ids := make([]string, 0, len(kept))
	for _, c := range kept {
		ids = append(ids, c.event.Id)
	}
	assert.Equal(t, []string{"C2", "C3"}, ids,
		"fp1 already correlated → dropped; fp2 new and nil-fp kept")
}

func TestFilterNewCorrelations_NoneExisting(t *testing.T) {
	c1 := makeEvent("C1", time.Now())
	c1.Fingerprint = strPtr("fp1")
	correlated := []correlatedCandidate{{event: c1}}

	kept := filterNewCorrelations(correlated, map[string]bool{})
	require.Len(t, kept, 1)
	assert.Equal(t, "C1", kept[0].event.Id)
}

// assertRow checks the 9 bound arguments for one event_correlations row.
func assertRow(t *testing.T, row []interface{}, eventID, relatedID, acct, tenant, corrType string, score float64, reason string, offset, depDist int) {
	t.Helper()
	require.Len(t, row, correlationInsertCols)
	assert.Equal(t, eventID, row[0], "event_id")
	assert.Equal(t, relatedID, row[1], "related_event_id")
	assert.Equal(t, acct, row[2], "cloud_account_id")
	require.NotNil(t, row[3])
	assert.Equal(t, tenant, *(row[3].(*string)), "tenant_id")
	assert.Equal(t, corrType, row[4], "correlation_type")
	assert.Equal(t, score, row[5], "correlation_score")
	assert.Equal(t, reason, row[6], "correlation_reason")
	assert.Equal(t, offset, row[7], "time_offset_minutes")
	assert.Equal(t, depDist, row[8], "dependency_distance")
}
