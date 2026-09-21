package events

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildAuthoredEvidenceReadsTheFieldsForThePickedType(t *testing.T) {
	evidence := buildAuthoredEvidence(map[string]any{
		"type":     "markdown",
		"title":    "  Rollout blocked  ",
		"severity": "Critical",
		"content":  "The new pods never became ready.",
	})

	assert.Equal(t, "markdown", evidence.Type)
	assert.Equal(t, "Rollout blocked", evidence.Title)
	assert.Equal(t, "Critical", evidence.Severity)
	assert.Equal(t, "The new pods never became ready.", evidence.Content)
}

func TestBuildAuthoredEvidenceAcceptsRealJsonValuesNotJustText(t *testing.T) {
	// The form sends a string, but a YAML- or Nubi-authored automation can pass
	// the real thing.
	evidence := buildAuthoredEvidence(map[string]any{
		"type":      "json",
		"json_data": map[string]any{"replicas": 3},
	})
	assert.Equal(t, `{"replicas":3}`, evidence.JsonData)

	table := buildAuthoredEvidence(map[string]any{
		"type":    "table",
		"headers": []any{"Pod", "Reason"},
		"rows":    []any{[]any{"api-7f9", "CrashLoopBackOff"}},
	})
	assert.Equal(t, []string{"Pod", "Reason"}, table.Headers)
	assert.Equal(t, `[["api-7f9","CrashLoopBackOff"]]`, table.Rows)
}

func TestInputSchemaOffersOnlyDisplayableTypes(t *testing.T) {
	schema := (&EventsAddEvidenceTask{}).InputSchema()

	typeProp := schema.Properties["type"]
	assert.Equal(t, []string{"markdown", "json", "table"}, typeProp.Options)
	// Neither required nor defaulted: the builder stamps defaults onto a step as
	// soon as it is opened, so either would make an automation written before
	// this form existed unsavable the moment someone looks at it.
	assert.False(t, typeProp.Required)
	assert.Nil(t, typeProp.Default)

	for field, expectedType := range map[string]string{
		"content":   "markdown",
		"json_data": "json",
		"headers":   "table",
		"rows":      "table",
	} {
		prop := schema.Properties[field]
		require.NotNilf(t, prop.VisibleWhen, "%s should only show for its own type", field)
		assert.Equal(t, "type", prop.VisibleWhen.Field)
		assert.Equal(t, []string{expectedType}, prop.VisibleWhen.Value)
		require.NotNilf(t, prop.RequiredWhen, "%s should be required for its own type", field)
		assert.Equal(t, []string{expectedType}, prop.RequiredWhen.Value)
	}

	// Picking a type has to clear a legacy step's array, or it would keep
	// winning over every field the author just filled in.
	evidences := schema.Properties["evidences"]
	assert.True(t, evidences.Hidden)
	assert.Equal(t, []string{"type"}, evidences.DependsOn)
}

func TestInputSchemaValidatesAuthoredAndLegacyParams(t *testing.T) {
	schema := (&EventsAddEvidenceTask{}).InputSchema()

	require.NoError(t, schema.Validate(map[string]any{
		"event_id": "e1", "type": "markdown", "content": "body",
	}))
	require.NoError(t, schema.Validate(map[string]any{
		"event_id": "e1", "type": "table", "headers": []any{"Pod"}, "rows": `[["api-7f9"]]`,
	}))

	// An automation written before the form existed.
	require.NoError(t, schema.Validate(map[string]any{
		"event_id":  "e1",
		"evidences": []any{map[string]any{"type": "markdown"}},
	}))

	err := schema.Validate(map[string]any{"event_id": "e1", "type": "json"})
	require.ErrorContains(t, err, "json_data")
}

func TestBuildAuthoredEvidenceCarriesTheAuthorsHighlight(t *testing.T) {
	evidence := buildAuthoredEvidence(map[string]any{
		"type": "markdown", "summary": "3 pods never became ready", "severity": "Critical", "content": "body",
	})
	assert.Equal(t, "3 pods never became ready", evidence.Summary)
	assert.Equal(t, "Critical", evidence.Severity)
}
