package dashboard

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const accountA = "6f1c2d3e-0000-4000-8000-000000000001"
const accountB = "6f1c2d3e-0000-4000-8000-000000000002"

func timeseriesPanel() Panel {
	return Panel{
		Id:         1,
		Title:      "p99 latency",
		Type:       VizTimeseries,
		Datasource: DatasourceMetrics,
		AccountIds: []string{accountA},
		GridPos:    GridPos{W: 6, H: 8},
		Targets:    []PanelTarget{{RefId: "A", Expr: "histogram_quantile(0.99, x)"}},
	}
}

func TestSlugify(t *testing.T) {
	assert.Equal(t, "checkout-latency-error-budget", Slugify("Checkout latency & error budget"))
	assert.Equal(t, "dashboard", Slugify("   "))
	assert.Equal(t, "dashboard", Slugify("!!!"))
	assert.Equal(t, "already-clean", Slugify("already-clean"))
	// Long titles are truncated without leaving a trailing separator.
	long := Slugify(string(make([]byte, 0)) + "a very long title that keeps going and going and going and going and going and going")
	assert.LessOrEqual(t, len(long), 80)
	assert.NotEqual(t, byte('-'), long[len(long)-1])
}

func TestValidateDefinition_AcceptsValidPanels(t *testing.T) {
	gauge := timeseriesPanel()
	gauge.Id = 3
	gauge.Type = VizGauge
	def := Definition{
		Panels: []Panel{
			timeseriesPanel(),
			{Id: 2, Title: "Notes", Type: VizText, GridPos: GridPos{W: 12, H: 4}, Content: "hello"},
			gauge,
		},
	}
	require.NoError(t, ValidateDefinition(def))
}

// Two panels on two accounts in one dashboard is the reason accounts live on
// the panel rather than the dashboard.
func TestValidateDefinition_AcceptsPanelsOnDifferentAccounts(t *testing.T) {
	a, b := timeseriesPanel(), timeseriesPanel()
	b.Id = 2
	b.AccountIds = []string{accountB}
	require.NoError(t, ValidateDefinition(Definition{Panels: []Panel{a, b}}))
}

func TestValidateDefinition_AcceptsMultiAccountAndTypePanels(t *testing.T) {
	multi := timeseriesPanel()
	multi.AccountIds = []string{accountA, accountB}
	require.NoError(t, ValidateDefinition(Definition{Panels: []Panel{multi}}))

	byType := timeseriesPanel()
	byType.AccountIds = nil
	byType.AccountType = "AWS"
	require.NoError(t, ValidateDefinition(Definition{Panels: []Panel{byType}}))
}

// A panel must have ONE unambiguous answer to "which accounts is this?".
// A provider names the query language a panel's expression is written in, which
// only means something where a provider is resolved per account.
func TestValidateDefinition_ProviderOnlyOnProviderDatasources(t *testing.T) {
	for _, datasource := range []string{DatasourceMetrics, DatasourceLogs, DatasourceTraces} {
		p := timeseriesPanel()
		p.Datasource = datasource
		p.Provider = "prometheus"
		// logs and traces render as tables; traces also reads the query engine, so
		// it takes a structured query rather than an expression.
		if datasource != DatasourceMetrics {
			p.Type = VizTable
		}
		if IsEntityDatasource(datasource) {
			p.Targets = []PanelTarget{{RefId: "A", Query: map[string]any{
				"table":   "traces_v2",
				"columns": []any{map[string]any{"name": "trace_id"}},
			}}}
		}
		require.NoError(t, ValidateDefinition(Definition{Panels: []Panel{p}}), datasource)
	}

	for _, datasource := range []string{DatasourceNudgebee, DatasourceRedis, DatasourceRabbitMQ, DatasourcePostgres} {
		p := timeseriesPanel()
		p.Datasource = datasource
		p.Type = VizTable
		p.Provider = "prometheus"
		p.Targets = []PanelTarget{{RefId: "A", Expr: "PING"}}
		if IsEntityDatasource(datasource) {
			p.Targets = []PanelTarget{{RefId: "A", Query: map[string]any{
				"table":   "event_groupings_v2",
				"columns": []any{map[string]any{"name": "event_count"}},
			}}}
		}
		err := ValidateDefinition(Definition{Panels: []Panel{p}})
		require.Error(t, err, datasource)
		assert.Contains(t, err.Error(), "no provider to pin", datasource)
	}
}

// An index only means something alongside the provider it belongs to.
func TestValidateDefinition_ProviderIndexNeedsAProvider(t *testing.T) {
	p := timeseriesPanel()
	p.Datasource = DatasourceMetrics
	p.ProviderIndex = "metricbeat-*"

	err := ValidateDefinition(Definition{Panels: []Panel{p}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "needs a provider to belong to")

	p.Provider = "ES"
	require.NoError(t, ValidateDefinition(Definition{Panels: []Panel{p}}))
}

// Every panel written before Provider existed carries none, and must keep
// validating unchanged — the field needs no migration precisely because empty
// already means "each account's own default".
func TestValidateDefinition_AcceptsPanelsWithoutAProvider(t *testing.T) {
	p := timeseriesPanel()
	assert.Empty(t, p.Provider)
	require.NoError(t, ValidateDefinition(Definition{Panels: []Panel{p}}))
}

func TestValidateDefinition_RejectsBothOrNeitherAccountSelection(t *testing.T) {
	both := timeseriesPanel()
	both.AccountType = "K8S"
	both.AccountIds = []string{accountA}
	assert.Error(t, ValidateDefinition(Definition{Panels: []Panel{both}}), "type and ids together must be rejected")

	neither := timeseriesPanel()
	neither.AccountIds = nil
	assert.Error(t, ValidateDefinition(Definition{Panels: []Panel{neither}}))

	// A picker that emitted blanks is not a selection.
	blanks := timeseriesPanel()
	blanks.AccountIds = []string{"", "  "}
	assert.Error(t, ValidateDefinition(Definition{Panels: []Panel{blanks}}))

	// ...and blanks alongside a type must not trip the "both" branch either.
	typeWithBlanks := timeseriesPanel()
	typeWithBlanks.AccountType = "GCP"
	typeWithBlanks.AccountIds = []string{""}
	assert.NoError(t, ValidateDefinition(Definition{Panels: []Panel{typeWithBlanks}}))
}

func TestValidateDefinition_RejectsBadPanels(t *testing.T) {
	cases := map[string]func(p *Panel){
		"missing title":    func(p *Panel) { p.Title = "" },
		"unknown type":     func(p *Panel) { p.Type = "flamegraph" },
		"bad datasource":   func(p *Panel) { p.Datasource = "graphite" },
		"width too wide":   func(p *Panel) { p.GridPos.W = 13 },
		"width zero":       func(p *Panel) { p.GridPos.W = 0 },
		"no targets":       func(p *Panel) { p.Targets = nil },
		"blank expression": func(p *Panel) { p.Targets = []PanelTarget{{RefId: "A", Expr: "   "}} },
		// Without accounts the provider lookup has nothing to resolve, so the
		// panel would render an error on every load instead of failing at save.
		"missing account": func(p *Panel) { p.AccountIds = nil },
		"blank account":   func(p *Panel) { p.AccountIds = []string{"  "} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := timeseriesPanel()
			mutate(&p)
			assert.Error(t, ValidateDefinition(Definition{Panels: []Panel{p}}))
		})
	}
}

func TestValidateDefinition_RejectsDuplicatePanelIds(t *testing.T) {
	a, b := timeseriesPanel(), timeseriesPanel()
	assert.Error(t, ValidateDefinition(Definition{Panels: []Panel{a, b}}))
}

// A text panel carries prose, so it must not be forced to declare a datasource
// or targets the renderer would never read.
func TestValidateDefinition_TextPanelNeedsNoQuery(t *testing.T) {
	def := Definition{Panels: []Panel{{Id: 1, Title: "Notes", Type: VizText, GridPos: GridPos{W: 12, H: 4}}}}
	require.NoError(t, ValidateDefinition(def))
}

func TestValidateDefinition_NudgebeePanelNeedsQueryNotExpr(t *testing.T) {
	p := timeseriesPanel()
	p.Datasource = DatasourceNudgebee
	// An entity query returns rows, so the chart types have nothing to plot.
	p.Type = VizTable
	p.Targets = []PanelTarget{{RefId: "A", Expr: "sum(x)"}}
	assert.Error(t, ValidateDefinition(Definition{Panels: []Panel{p}}), "expr is not a valid nudgebee target")

	p.Targets = []PanelTarget{{RefId: "A", Query: map[string]any{
		"table":   "event_groupings_v2",
		"columns": []any{map[string]any{"name": "event_count"}},
	}}}
	assert.NoError(t, ValidateDefinition(Definition{Panels: []Panel{p}}))

	// A chart visualisation is refused even with a valid query.
	chart := p
	chart.Type = VizTimeseries
	assert.Error(t, ValidateDefinition(Definition{Panels: []Panel{chart}}), "a nudgebee panel renders a table")
}

// Dashboards authored before a panel could name several accounts stored a
// single `account_id`. Reading one back must not render "No account" on every
// panel — verified against a real stored definition from the dev database.
func TestUpgradeDefinition_FoldsLegacyAccountIdIntoAccountIds(t *testing.T) {
	stored := []byte(`{"panels":[
		{"id":1,"type":"timeseries","title":"up query","datasource":"metrics",
		 "grid_pos":{"h":8,"w":6,"x":0,"y":0},
		 "targets":[{"expr":"up","ref_id":"A"}],
		 "account_id":"a2a30b02-0f67-42e5-a2ab-c658230fd798"}]}`)

	var def Definition
	require.NoError(t, json.Unmarshal(stored, &def))
	upgradeDefinition(&def)

	assert.Equal(t, []string{"a2a30b02-0f67-42e5-a2ab-c658230fd798"}, def.Panels[0].AccountIds)
	assert.Empty(t, def.Panels[0].AccountType)
	// The upgraded panel must now pass the validation that rejected it before.
	require.NoError(t, ValidateDefinition(def))

	// The legacy key must not survive a write, or it becomes a second source of
	// truth that silently disagrees with account_ids.
	out, err := json.Marshal(def)
	require.NoError(t, err)
	assert.NotContains(t, string(out), `"account_id"`)
	assert.Contains(t, string(out), `"account_ids"`)
}

func TestUpgradeDefinition_IsIdempotentAndNeverOverwrites(t *testing.T) {
	// Running twice must not duplicate or re-add anything.
	def := Definition{Panels: []Panel{{LegacyAccountId: accountA}}}
	upgradeDefinition(&def)
	upgradeDefinition(&def)
	assert.Equal(t, []string{accountA}, def.Panels[0].AccountIds)

	// A panel already on the new shape wins over a stale legacy value.
	withIds := Definition{Panels: []Panel{{AccountIds: []string{accountB}, LegacyAccountId: accountA}}}
	upgradeDefinition(&withIds)
	assert.Equal(t, []string{accountB}, withIds.Panels[0].AccountIds)

	withType := Definition{Panels: []Panel{{AccountType: "AWS", LegacyAccountId: accountA}}}
	upgradeDefinition(&withType)
	assert.Equal(t, "AWS", withType.Panels[0].AccountType)
	assert.Empty(t, withType.Panels[0].AccountIds, "legacy id must not create a both-set panel")
	assert.Empty(t, withType.Panels[0].LegacyAccountId)
}

func TestPanelAccountIds(t *testing.T) {
	a, b, dup := timeseriesPanel(), timeseriesPanel(), timeseriesPanel()
	b.Id, b.AccountIds = 2, []string{accountB, accountA} // accountA repeats across panels
	dup.Id = 3                                           // same account as a — must not be listed twice
	text := Panel{Id: 4, Title: "Notes", Type: VizText, GridPos: GridPos{W: 12, H: 4}}

	// Order is preserved so the caller's 403 names the first offending account.
	assert.Equal(t, []string{accountA, accountB},
		PanelAccountIds(Definition{Panels: []Panel{a, b, dup, text}}))
	assert.Equal(t, []string{}, PanelAccountIds(Definition{Panels: []Panel{text}}))
	assert.Equal(t, []string{}, PanelAccountIds(Definition{}))
}

// An account-type panel names no account, so there is nothing to authorize at
// save time — it resolves per viewer at render.
func TestPanelAccountIds_IgnoresAccountTypePanels(t *testing.T) {
	byType := timeseriesPanel()
	byType.AccountIds = nil
	byType.AccountType = "AWS"
	assert.Equal(t, []string{}, PanelAccountIds(Definition{Panels: []Panel{byType}}))
}

func TestValidateBindings(t *testing.T) {
	assert.NoError(t, validateBindings([]Binding{
		{ScopeType: "workload", MatchKind: "app_type", MatchValue: map[string]any{"app_type": "postgres"}},
		{ScopeType: "namespace", MatchKind: "name_regex", MatchValue: map[string]any{"regex": "^payments$"}},
	}))

	assert.Error(t, validateBindings([]Binding{{ScopeType: "galaxy", MatchKind: "all"}}))
	assert.Error(t, validateBindings([]Binding{{ScopeType: "workload", MatchKind: "vibes"}}))
	// An unparseable regex must fail at save time, not silently match nothing
	// forever on every detail-page load.
	assert.Error(t, validateBindings([]Binding{
		{ScopeType: "workload", MatchKind: "name_regex", MatchValue: map[string]any{"regex": "([a-z"}},
	}))
	assert.Error(t, validateBindings([]Binding{
		{ScopeType: "workload", MatchKind: "name_regex", MatchValue: map[string]any{}},
	}))
}

func TestBindingMatches(t *testing.T) {
	req := ResolveRequest{ScopeType: "workload", Name: "payments-api-7d9f", Namespace: "payments", AppType: "postgres"}

	tests := []struct {
		name    string
		binding Binding
		want    bool
	}{
		{"all matches anything", Binding{MatchKind: "all"}, true},
		{"app_type exact", Binding{MatchKind: "app_type", MatchValue: map[string]any{"app_type": "postgres"}}, true},
		{"app_type is case-insensitive", Binding{MatchKind: "app_type", MatchValue: map[string]any{"app_type": "PostGres"}}, true},
		{"app_type mismatch", Binding{MatchKind: "app_type", MatchValue: map[string]any{"app_type": "redis"}}, false},
		{"regex prefix", Binding{MatchKind: "name_regex", MatchValue: map[string]any{"regex": "^payments-"}}, true},
		{"regex mismatch", Binding{MatchKind: "name_regex", MatchValue: map[string]any{"regex": "^billing-"}}, false},
		{"invalid regex never matches", Binding{MatchKind: "name_regex", MatchValue: map[string]any{"regex": "([a-z"}}, false},
		{"namespace selector", Binding{MatchKind: "label_selector", MatchValue: map[string]any{"namespace": "payments"}}, true},
		{"namespace selector mismatch", Binding{MatchKind: "label_selector", MatchValue: map[string]any{"namespace": "billing"}}, false},
		{"empty app_type does not match", Binding{MatchKind: "app_type", MatchValue: map[string]any{}}, false},
		{"unknown match kind", Binding{MatchKind: "telepathy"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, bindingMatches(tc.binding, req))
		})
	}
}
