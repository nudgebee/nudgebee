package dashboard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A timeseries panel carrying the given drill-down links.
func chartWithLinks(links ...any) Panel {
	p := timeseriesPanel()
	p.Options = map[string]any{"data_links": links}
	return p
}

func TestValidateDataLinks_AcceptsValid(t *testing.T) {
	p := chartWithLinks(
		map[string]any{"title": "Open logs", "url": "/kubernetes/details/{{__account_id}}?pod={{pod}}&start_time={{__from}}&end_time={{__to}}#logs"},
		map[string]any{"title": "Workload", "url": "/kubernetes/details/{{__account_id}}#overview"},
	)
	require.NoError(t, validateDataLinks(p))
}

func TestValidateDataLinks_NoneIsFine(t *testing.T) {
	require.NoError(t, validateDataLinks(timeseriesPanel()))
	require.NoError(t, validateDataLinks(chartWithLinks()))
}

// The whole reason this is validated server-side: the definition one user saves
// is the href every other viewer of the dashboard follows.
func TestValidateDataLinks_RejectsLinkOffProduct(t *testing.T) {
	// `/\evil.test` is the subtle one: the URL spec treats `\` as `/` for http(s),
	// so a browser parses it as the protocol-relative `//evil.test`.
	for _, url := range []string{"javascript:alert(1)", "https://evil.test/steal?pod={{pod}}", "//evil.test/steal", "/\tjavascript:alert(1)", "/\\evil.test", ""} {
		p := chartWithLinks(map[string]any{"title": "Open logs", "url": url})
		err := validateDataLinks(p)
		require.Error(t, err, "url %q should be refused", url)
		assert.Contains(t, err.Error(), "inside the product")
	}
}

func TestValidateDataLinks_RejectsEmptyTitle(t *testing.T) {
	p := chartWithLinks(map[string]any{"title": "  ", "url": "/kubernetes/details/{{__account_id}}"})
	require.ErrorContains(t, validateDataLinks(p), "needs a title")
}

// Only a metrics timeseries panel has points to click; the facet anywhere else
// is config nothing can fire — the mirror of the table-column rule.
func TestValidateDataLinks_RejectsOutsideMetricsTimeseries(t *testing.T) {
	// A non-timeseries type.
	stat := chartWithLinks(map[string]any{"title": "Open logs", "url": "/kubernetes/details/{{__account_id}}"})
	stat.Type = VizStat
	require.ErrorContains(t, validateDataLinks(stat), "only apply to a metrics timeseries panel")

	// A traces panel can be drawn as a timeseries but returns table data with no
	// series, so a data link there can never fire — refused at save.
	traces := chartWithLinks(map[string]any{"title": "Open logs", "url": "/kubernetes/details/{{__account_id}}"})
	traces.Datasource = DatasourceTraces
	require.ErrorContains(t, validateDataLinks(traces), "only apply to a metrics timeseries panel")
}

// Wired into ValidateDefinition, so a bad link fails the save, not just the unit.
func TestValidateDefinition_RejectsBadDataLink(t *testing.T) {
	p := chartWithLinks(map[string]any{"title": "Open logs", "url": "https://evil.test"})
	require.ErrorContains(t, ValidateDefinition(Definition{Panels: []Panel{p}}), "inside the product")
}
