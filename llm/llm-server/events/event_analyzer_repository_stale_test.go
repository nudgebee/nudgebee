package events

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// IsAnalysisStale is the single predicate behind every reuse gate. The gates in
// api/event_analyzer.go all consult it, so its edges are the edges of the
// feature: get the zero-time or disabled case wrong and either nothing is ever
// reused or everything is regenerated on every event.
func TestIsAnalysisStale(t *testing.T) {
	now := time.Now()

	cases := []struct {
		name      string
		freshness time.Duration
		writtenAt time.Time
		want      bool
		why       string
	}{
		{
			name:      "disabled: an ancient analysis is never stale",
			freshness: 0,
			writtenAt: now.Add(-365 * 24 * time.Hour),
			want:      false,
			why:       "zero freshness must restore the previous reuse-forever behaviour exactly",
		},
		{
			name:      "inside the window",
			freshness: 24 * time.Hour,
			writtenAt: now.Add(-1 * time.Hour),
			want:      false,
		},
		{
			name:      "past the window",
			freshness: 24 * time.Hour,
			writtenAt: now.Add(-25 * time.Hour),
			want:      true,
		},
		{
			name:      "just inside the window",
			freshness: 24 * time.Hour,
			writtenAt: now.Add(-24*time.Hour + time.Minute),
			want:      false,
		},
		{
			name:      "just past the window",
			freshness: 24 * time.Hour,
			writtenAt: now.Add(-24*time.Hour - time.Minute),
			want:      true,
			why: "the exact boundary is not asserted: now is captured at setup, so " +
				"time.Since has already advanced past it by the time the call runs",
		},
		{
			name:      "unknown timestamp is never stale",
			freshness: 24 * time.Hour,
			writtenAt: time.Time{},
			want:      false,
			why: "a zero time means the row carried no usable timestamp. Treating that as " +
				"infinitely stale would regenerate every stage on every event forever",
		},
		{
			name:      "future timestamp is not stale",
			freshness: 24 * time.Hour,
			writtenAt: now.Add(1 * time.Hour),
			want:      false,
			why:       "clock skew between app and database must not force a regenerate",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &EventAnalysisRepository{analysisFreshness: tc.freshness}
			assert.Equal(t, tc.want, repo.IsAnalysisStale(tc.writtenAt), tc.why)
		})
	}
}

// The bound is only coherent if every gate reaches the same verdict for the same
// row. If an outer gate rejects a stage as stale but a per-step cache still
// considers it fresh, the pipeline is dispatched, the cache skips the work,
// nothing is written, updated_at does not move -- and the next event repeats the
// round. Nothing in that cycle ever advances the timestamp that would end it.
//
// A shared predicate is what makes that impossible, so this pins that the same
// input cannot produce two answers.
func TestIsAnalysisStaleIsDeterministicAcrossGates(t *testing.T) {
	repo := &EventAnalysisRepository{analysisFreshness: 24 * time.Hour}
	writtenAt := time.Now().Add(-48 * time.Hour)

	first := repo.IsAnalysisStale(writtenAt)
	for i := 0; i < 8; i++ {
		assert.Equal(t, first, repo.IsAnalysisStale(writtenAt),
			"every gate must reach the same verdict for one row, or reuse and regeneration deadlock")
	}
	assert.True(t, first, "a 48h-old analysis is past a 24h window")
}
