package eventrule

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// event_rules.severity is foreign-keyed to critical/warning, so every raw
// provider severity must narrow to one of the two.
func TestNarrowSeverity(t *testing.T) {
	for raw, want := range map[string]string{
		"critical":     "critical",
		"CRITICAL":     "critical",
		"high":         "critical",
		" High ":       "critical",
		"warning":      "warning",
		"medium":       "warning",
		"low":          "warning",
		"info":         "warning",
		"error":        "warning",
		"availability": "warning",
		"":             "warning",
	} {
		assert.Equal(t, want, NarrowSeverity(raw), raw)
	}
}

func TestNarrowLabelsSeverity(t *testing.T) {
	assert.JSONEq(t, `{"severity":"critical","team":"a"}`, string(narrowLabelsSeverity([]byte(`{"severity":"high","team":"a"}`))))
	assert.JSONEq(t, `{"team":"a"}`, string(narrowLabelsSeverity([]byte(`{"team":"a"}`))))
	assert.Equal(t, "not json", string(narrowLabelsSeverity([]byte("not json"))))
}
