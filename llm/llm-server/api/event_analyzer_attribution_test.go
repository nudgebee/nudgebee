package api

import (
	"testing"

	"nudgebee/llm/events"

	"github.com/stretchr/testify/assert"
)

// TestShouldAttributeToSystemUser is a regression test for #35805: event
// analyses were attributed to whichever operator's session happened to be
// open, as long as an analysis row already existed for that event — even
// when the run was actually automated (first-time ingestion, or an implicit
// retry of a previously failed analysis on the next page view), not a
// deliberate human "Regenerate" click.
func TestShouldAttributeToSystemUser(t *testing.T) {
	existing := &events.EventAnalysis{}

	// First-time analysis (nothing stored yet) is always system-attributed,
	// regardless of the regenerate flag.
	assert.True(t, shouldAttributeToSystemUser(nil, false))
	assert.True(t, shouldAttributeToSystemUser(nil, true))

	// An existing analysis with no explicit regenerate request is an implicit
	// re-trigger (e.g. a plain page view retrying a failed analysis) — still
	// automated, must stay system-attributed.
	assert.True(t, shouldAttributeToSystemUser(existing, false))

	// Only an EXPLICIT regenerate against an existing analysis is the
	// reliable signal of a deliberate human action — keep the caller's
	// identity in that case alone.
	assert.False(t, shouldAttributeToSystemUser(existing, true))
}
