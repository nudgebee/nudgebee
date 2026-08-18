package dashboard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A table panel over events, which is where a link column earns its keep.
func eventsTablePanel(options map[string]any) Panel {
	return Panel{
		Id:         1,
		Title:      "Open events",
		Type:       VizTable,
		Datasource: DatasourceNudgebee,
		AccountIds: []string{accountA},
		GridPos:    GridPos{W: 6, H: 8},
		Targets: []PanelTarget{{RefId: "A", Query: map[string]any{
			"table":   "events_v2",
			"columns": []any{map[string]any{"name": "title"}, map[string]any{"name": "id"}, map[string]any{"name": "account_id"}},
		}}},
		Options: options,
	}
}

func TestValidateDefinition_AcceptsColumnSettings(t *testing.T) {
	// Both shapes: an existing column made clickable, and a column of links added.
	def := Definition{Panels: []Panel{eventsTablePanel(map[string]any{
		"columns": []any{
			map[string]any{"name": "subject_name", "link": map[string]any{"url": "/investigate?id={{id}}"}},
			map[string]any{"name": "id", "visibility": "hidden"},
			map[string]any{"name": "account_id", "visibility": "hidden"},
			map[string]any{"name": "starts_at", "title": "When", "format": "time"},
			map[string]any{"title": "Investigate", "link": map[string]any{"url": "/investigate?id={{id}}&accountId={{account_id}}"}},
		},
	})}}
	require.NoError(t, ValidateDefinition(def))
}

// The whole reason this is validated server-side: the definition one user saves
// is the href every other viewer of the dashboard follows.
func TestValidateDefinition_RejectsLinkOffProduct(t *testing.T) {
	// `/\evil.test` is the subtle one: the URL spec treats `\` as `/` for http(s),
	// so a browser parses it as the protocol-relative `//evil.test`.
	for _, url := range []string{"javascript:alert(1)", "https://evil.test/steal?id={{id}}", "//evil.test/steal", "/\tjavascript:alert(1)", "/\\evil.test", ""} {
		def := Definition{Panels: []Panel{eventsTablePanel(map[string]any{
			"columns": []any{map[string]any{"title": "Investigate", "link": map[string]any{"url": url}}},
		})}}
		err := ValidateDefinition(def)
		require.Error(t, err, "url %q should be refused", url)
		assert.Contains(t, err.Error(), "inside the product")
	}
}

func TestValidateDefinition_RejectsUnusableColumnSettings(t *testing.T) {
	// Names nothing: neither a column to configure nor a column to add.
	anonymous := Definition{Panels: []Panel{eventsTablePanel(map[string]any{
		"columns": []any{map[string]any{"visibility": "hidden"}},
	})}}
	require.ErrorContains(t, ValidateDefinition(anonymous), "needs the name of a column")

	// Two entries for one column leaves no answer to which one wins.
	duplicate := Definition{Panels: []Panel{eventsTablePanel(map[string]any{
		"columns": []any{
			map[string]any{"name": "title", "visibility": "hidden"},
			map[string]any{"name": "title", "link": map[string]any{"url": "/investigate?id={{id}}"}},
		},
	})}}
	require.ErrorContains(t, ValidateDefinition(duplicate), "configured twice")

	// An added column with no link renders a column of identical text.
	pointless := Definition{Panels: []Panel{eventsTablePanel(map[string]any{
		"columns": []any{map[string]any{"title": "Investigate"}},
	})}}
	require.ErrorContains(t, ValidateDefinition(pointless), "needs a link")

	unknownVisibility := Definition{Panels: []Panel{eventsTablePanel(map[string]any{
		"columns": []any{map[string]any{"name": "id", "visibility": "collapsed"}},
	})}}
	require.ErrorContains(t, ValidateDefinition(unknownVisibility), "unknown visibility")
}

// A series has no row to open and no column to hide, so column settings on one
// are settings that would silently do nothing.
func TestValidateDefinition_RejectsColumnSettingsOnChart(t *testing.T) {
	p := timeseriesPanel()
	p.Options = map[string]any{"columns": []any{map[string]any{"title": "Investigate", "link": map[string]any{"url": "/investigate?id={{id}}"}}}}
	require.ErrorContains(t, ValidateDefinition(Definition{Panels: []Panel{p}}), "only apply to a table panel")
}

// Options are free-form and also arrive by import; unrelated keys are none of
// this validator's business.
func TestValidateDefinition_IgnoresUnrelatedOptions(t *testing.T) {
	def := Definition{Panels: []Panel{eventsTablePanel(map[string]any{"legend": "right"})}}
	require.NoError(t, ValidateDefinition(def))
}

// The shape `columns` replaced. A dashboard saved before the change must render
// the same afterwards, and must stop carrying the old arrays once rewritten.
func TestUpgradeDefinition_FoldsLegacyColumnArrays(t *testing.T) {
	def := Definition{Panels: []Panel{eventsTablePanel(map[string]any{
		"link_columns": []any{
			map[string]any{"column": "subject_name", "url": "/investigate?id={{id}}"},
			map[string]any{"title": "Investigate", "url": "/investigate?id={{id}}&accountId={{account_id}}"},
			// A link on a column that is ALSO hidden has to merge into that one
			// entry, or the upgrade would emit the duplicate it then rejects.
			map[string]any{"column": "id", "url": "/investigate?id={{id}}"},
		},
		"hidden_columns": []any{"id", "account_id"},
	})}}
	upgradeDefinition(&def)

	options := def.Panels[0].Options
	assert.NotContains(t, options, "link_columns")
	assert.NotContains(t, options, "hidden_columns")

	opts, err := decodeTableOptions(options)
	require.NoError(t, err)
	require.Len(t, opts.Columns, 4)
	assert.Equal(t, TableColumn{Name: "id", Visibility: VisibilityHidden, Link: &ColumnLink{Url: "/investigate?id={{id}}"}}, opts.Columns[0])
	assert.Equal(t, TableColumn{Name: "account_id", Visibility: VisibilityHidden}, opts.Columns[1])
	assert.Equal(t, TableColumn{Name: "subject_name", Link: &ColumnLink{Url: "/investigate?id={{id}}"}}, opts.Columns[2])
	assert.Equal(t, TableColumn{Title: "Investigate", Link: &ColumnLink{Url: "/investigate?id={{id}}&accountId={{account_id}}"}}, opts.Columns[3])

	// The upgraded definition is one the validator accepts — the merge above is
	// what keeps it from producing a duplicate name.
	require.NoError(t, ValidateDefinition(def))

	// Idempotent: a second pass over the upgraded panel changes nothing.
	again := def
	upgradeDefinition(&again)
	assert.Equal(t, options, again.Panels[0].Options)
}
