package playbooks

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Regression: server-side Stage-2.2 table enrichers (oom_killer_enricher,
// resource_events_enricher, …) were emitting `{type:"table", rows, headers}`
// with no `data` wrapper. Frontend `extractAlertLabels` crashed in
// `i.data.table_name.includes(…)` and killed the whole investigate render.
// ShapeEvidenceForFrontend coerces into the Robusta-compatible shape the UI
// expects.
func TestShapeEvidenceForFrontend(t *testing.T) {
	t.Run("table_evidence_wraps_rows_and_headers", func(t *testing.T) {
		in := map[string]any{
			"type":    "table",
			"rows":    [][]any{{"a", "b"}},
			"headers": []string{"col1", "col2"},
			"additional_info": map[string]any{
				"title":       "Recent Pod Events",
				"action_name": "resource_events_enricher",
			},
		}
		ShapeEvidenceForFrontend(in)

		_, rowsAtTop := in["rows"]
		assert.False(t, rowsAtTop, "rows must be moved under data")
		_, headersAtTop := in["headers"]
		assert.False(t, headersAtTop, "headers must be moved under data")

		data, ok := in["data"].(map[string]any)
		assert.True(t, ok, "data wrapper must be present")
		assert.Equal(t, "Recent Pod Events", data["table_name"])
		assert.NotNil(t, data["rows"])
		assert.NotNil(t, data["headers"])
		assert.NotNil(t, data["column_renderers"])
	})

	t.Run("idempotent_when_data_already_present", func(t *testing.T) {
		in := map[string]any{
			"type": "table",
			"data": map[string]any{
				"table_name": "Pre-existing",
				"rows":       [][]any{{"x"}},
			},
		}
		ShapeEvidenceForFrontend(in)
		data := in["data"].(map[string]any)
		assert.Equal(t, "Pre-existing", data["table_name"], "must not overwrite existing data")
	})

	t.Run("non_table_types_are_no_op", func(t *testing.T) {
		for _, formatName := range []string{"json", "markdown", "file", "prometheus", "knowledge_graph"} {
			in := map[string]any{
				"type": formatName,
				"data": "raw-payload",
			}
			ShapeEvidenceForFrontend(in)
			assert.Equal(t, "raw-payload", in["data"], "format %s must be untouched", formatName)
		}
	})

	t.Run("nil_safe", func(t *testing.T) {
		assert.NotPanics(t, func() { ShapeEvidenceForFrontend(nil) })
	})

	t.Run("missing_title_falls_back_to_empty_table_name", func(t *testing.T) {
		in := map[string]any{
			"type":            "table",
			"rows":            [][]any{},
			"headers":         []string{},
			"additional_info": map[string]any{},
		}
		ShapeEvidenceForFrontend(in)
		data := in["data"].(map[string]any)
		assert.Equal(t, "", data["table_name"])
	})

	t.Run("missing_additional_info_does_not_panic", func(t *testing.T) {
		in := map[string]any{
			"type":    "table",
			"rows":    [][]any{{"r"}},
			"headers": []string{"h"},
		}
		assert.NotPanics(t, func() { ShapeEvidenceForFrontend(in) })
		data := in["data"].(map[string]any)
		assert.Equal(t, "", data["table_name"])
	})

	t.Run("table_without_rows_or_headers", func(t *testing.T) {
		in := map[string]any{
			"type":            "table",
			"additional_info": map[string]any{"title": "Empty"},
		}
		ShapeEvidenceForFrontend(in)
		data, ok := in["data"].(map[string]any)
		assert.True(t, ok)
		assert.Equal(t, "Empty", data["table_name"])
		assert.NotNil(t, data["column_renderers"])
	})
}
