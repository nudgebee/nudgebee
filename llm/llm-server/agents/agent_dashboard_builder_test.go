package agents

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewDashboardBuilderAgentProviderGate pins the allowlist: the backends
// api-server queries as PromQL are accepted, and every other one — including a
// backend nobody has listed yet — is refused before the LLM runs.
func TestNewDashboardBuilderAgentProviderGate(t *testing.T) {
	for _, provider := range []string{"prometheus", "victoria_metrics", "victoria-metrics", "chronosphere", "cubeapm", "openobserve", "", "Prometheus"} {
		t.Run("accepts "+provider, func(t *testing.T) {
			agent, err := newDashboardBuilderAgent("acct", services_server.ObservabilityProvider{Provider: provider})
			require.NoError(t, err)
			assert.Equal(t, DashboardBuilderAgentName, agent.GetName())
			assert.Equal(t, core.AgentPlannerTypeCustom, agent.GetPlannerType())
			assert.Equal(t, dashboardWriterAgentName, agent.writer.GetName())
		})
	}
	for _, provider := range []string{"ES", "elasticsearch", "opensearch", "datadog", "aws", "gcp", "azure", "newrelic", "NewRelic", "dynatrace", "solarwinds", "splunk_enterprise", "some-new-backend"} {
		t.Run("refuses "+provider, func(t *testing.T) {
			_, err := newDashboardBuilderAgent("acct", services_server.ObservabilityProvider{Provider: provider})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "Prometheus-compatible")
			assert.Contains(t, err.Error(), provider)
		})
	}
}

func runDashboardBuilder(t *testing.T, writerAnswer string, writerErr error) core.NBAgentResponse {
	t.Helper()
	agent, err := newDashboardBuilderAgent("acct-1", services_server.ObservabilityProvider{Provider: "prometheus"})
	require.NoError(t, err)
	var gotCommand string
	agent.executor = func(toolCtx toolcore.NbToolContext, writer core.NBAgent, req toolcore.NBToolCallRequest) (core.NBAgentResponse, error) {
		assert.Equal(t, dashboardWriterAgentName, writer.GetName())
		assert.Equal(t, string(core.KnowledgeDisabled), toolCtx.KnowledgePolicy, "the writer must not get search_skills/load_skills")
		assert.True(t, toolCtx.KnowledgePolicyResolved)
		gotCommand = req.Command
		if writerErr != nil {
			return core.NBAgentResponse{}, writerErr
		}
		return core.NBAgentResponse{Response: []string{writerAnswer}}, nil
	}
	ctx := security.NewRequestContextForTenantAccountAdmin("tenant-1", "user-1", []string{"acct-1"})
	resp, err := agent.Execute(ctx, core.NBAgentRequest{Query: "cluster health", AccountId: "acct-1"})
	require.NoError(t, err, "Execute reports failures in the response, never as an error")
	assert.Contains(t, gotCommand, "Design a monitoring dashboard")
	assert.True(t, strings.HasSuffix(gotCommand, "Dashboard description: cluster health"), gotCommand)
	return resp
}

func decodeDraft(t *testing.T, resp core.NBAgentResponse) generatedDashboard {
	t.Helper()
	require.Equal(t, core.ConversationStatusCompleted, resp.Status, "response: %v", resp.Response)
	require.Len(t, resp.Response, 1)
	var draft generatedDashboard
	require.NoError(t, json.Unmarshal([]byte(resp.Response[0]), &draft))
	return draft
}

func TestDashboardBuilderExecuteNormalisesTheDraft(t *testing.T) {
	answer := "```json\n" + `{
	  "title": "Cluster health",
	  "description": "Headline numbers then trends.",
	  "panels": [
	    {"title": "Ready nodes", "type": "stat", "unit": "nodes",
	     "targets": [{"expr": "sum(kube_node_status_condition{__CLUSTER__ condition=\"Ready\", status=\"true\"})"}]},
	    {"title": "CPU by namespace", "type": "line", "width": 20,
	     "targets": [{"expr": "sum by (namespace) (rate(container_cpu_usage_seconds_total{__CLUSTER__}[5m]))", "legend_format": "{{namespace}}"},
	                 {"expr": "sum(machine_cpu_cores{__CLUSTER__})"}]},
	    {"title": "Notes", "type": "text", "content": "Read me first."},
	    {"title": "Broken", "type": "timeseries", "targets": [{"expr": "sum(rate(foo[5m])"}]},
	    {"title": "Pie", "type": "piechart", "targets": [{"expr": "up"}]}
	  ]
	}` + "\n```"

	draft := decodeDraft(t, runDashboardBuilder(t, answer, nil))

	assert.Equal(t, "Cluster health", draft.Title)
	assert.Equal(t, "Headline numbers then trends.", draft.Description)
	require.Len(t, draft.Definition.Panels, 3)

	stat := draft.Definition.Panels[0]
	assert.Equal(t, 1, stat.Id)
	assert.Equal(t, "stat", stat.Type)
	assert.Equal(t, "metrics", stat.Datasource)
	assert.Equal(t, 3, stat.GridPos.W, "stat defaults to a quarter-row tile")
	assert.Equal(t, defaultPanelHeight, stat.GridPos.H)
	assert.Equal(t, "nodes", stat.Unit)
	require.Len(t, stat.Targets, 1)
	assert.Equal(t, "A", stat.Targets[0].RefId)

	chart := draft.Definition.Panels[1]
	assert.Equal(t, 2, chart.Id)
	assert.Equal(t, "timeseries", chart.Type, "'line' folds to timeseries")
	assert.Equal(t, 12, chart.GridPos.W, "width is clamped to the 12-column grid")
	require.Len(t, chart.Targets, 2)
	assert.Equal(t, []string{"A", "B"}, []string{chart.Targets[0].RefId, chart.Targets[1].RefId})
	assert.Equal(t, "{{namespace}}", chart.Targets[0].LegendFormat)

	text := draft.Definition.Panels[2]
	assert.Equal(t, 3, text.Id)
	assert.Equal(t, "text", text.Type)
	assert.Equal(t, "Read me first.", text.Content)
	assert.Equal(t, 12, text.GridPos.W)
	assert.Empty(t, text.Targets)

	warnings := strings.Join(draft.Warnings, "\n")
	assert.Contains(t, warnings, `"Broken": dropped — dropped a query that does not parse`)
	assert.Contains(t, warnings, `"Pie": dropped — unsupported panel type "piechart"`)
}

func TestDashboardBuilderAcceptsPanelsUnderDefinition(t *testing.T) {
	answer := `{"title": "T", "definition": {"panels": [{"title": "Up", "type": "stat", "grid_pos": {"w": 4, "h": 5}, "targets": [{"expr": "sum(up{__CLUSTER__})"}]}]}}`

	draft := decodeDraft(t, runDashboardBuilder(t, answer, nil))

	require.Len(t, draft.Definition.Panels, 1)
	assert.Equal(t, 4, draft.Definition.Panels[0].GridPos.W)
	assert.Equal(t, 5, draft.Definition.Panels[0].GridPos.H)
	assert.Empty(t, draft.Warnings)
}

func TestDashboardBuilderKeepsAPanelWhenOnlySomeQueriesFail(t *testing.T) {
	answer := `{"title": "T", "panels": [{"title": "Mixed", "targets": [{"expr": "up{__CLUSTER__}"}, {"expr": "up; down"}, {"expr": ""}]}]}`

	draft := decodeDraft(t, runDashboardBuilder(t, answer, nil))

	require.Len(t, draft.Definition.Panels, 1)
	panel := draft.Definition.Panels[0]
	assert.Equal(t, "timeseries", panel.Type, "a missing type defaults to timeseries")
	require.Len(t, panel.Targets, 1)
	assert.Equal(t, "topk(10, up{__CLUSTER__})", panel.Targets[0].Expr, "a bare selector on a chart is capped")
	require.Len(t, draft.Warnings, 1)
	assert.Contains(t, draft.Warnings[0], "one query per target")
	assert.Contains(t, draft.Warnings[0], "top 10 series")
}

func TestDashboardBuilderCapsPanelsAndQueries(t *testing.T) {
	targets := make([]string, 0, maxTargetsPerPanel+2)
	for i := 0; i < maxTargetsPerPanel+2; i++ {
		targets = append(targets, fmt.Sprintf(`{"expr": "up{__CLUSTER__ job=\"j%d\"}"}`, i))
	}
	panels := make([]string, 0, maxGeneratedPanels+3)
	for i := 0; i < maxGeneratedPanels+3; i++ {
		panels = append(panels, fmt.Sprintf(`{"title": "P%d", "targets": [%s]}`, i, strings.Join(targets, ",")))
	}
	answer := `{"title": "Big", "panels": [` + strings.Join(panels, ",") + `]}`

	draft := decodeDraft(t, runDashboardBuilder(t, answer, nil))

	require.Len(t, draft.Definition.Panels, maxGeneratedPanels)
	assert.Len(t, draft.Definition.Panels[0].Targets, maxTargetsPerPanel)
	assert.Contains(t, strings.Join(draft.Warnings, "\n"), "3 more were left out")
}

func TestDashboardBuilderFailsWhenNothingIsUsable(t *testing.T) {
	for name, tc := range map[string]struct {
		answer  string
		err     error
		message string
	}{
		"writer error":     {err: fmt.Errorf("model timeout"), message: "dashboard generation: model timeout"},
		"empty answer":     {answer: "  ", message: "empty response"},
		"prose answer":     {answer: "I could not find any metrics for that.", message: "the answer was not a dashboard"},
		"no panels":        {answer: `{"title": "Empty", "panels": []}`, message: "the answer contained no panels"},
		"every panel bad":  {answer: `{"title": "Bad", "panels": [{"title": "X", "targets": [{"expr": "rate(("}]}]}`, message: `"X": dropped`},
		"text without any": {answer: `{"title": "T", "panels": [{"title": "Notes", "type": "text"}]}`, message: "text panel had no content"},
	} {
		t.Run(name, func(t *testing.T) {
			resp := runDashboardBuilder(t, tc.answer, tc.err)
			assert.Equal(t, core.ConversationStatusFailed, resp.Status)
			require.Len(t, resp.Response, 1)
			assert.Contains(t, resp.Response[0], tc.message)
		})
	}
}

func TestDashboardBuilderDefaultsMissingTitles(t *testing.T) {
	answer := `{"panels": [{"type": "gauge", "targets": [{"expr": "avg(up{__CLUSTER__})"}]}]}`

	draft := decodeDraft(t, runDashboardBuilder(t, answer, nil))

	assert.Equal(t, "Generated dashboard", draft.Title)
	require.Len(t, draft.Definition.Panels, 1)
	assert.Equal(t, "Panel 1", draft.Definition.Panels[0].Title)
	assert.Equal(t, 3, draft.Definition.Panels[0].GridPos.W)
}

func TestDashboardWriterPrompt(t *testing.T) {
	writer := &dashboardWriterAgent{accountId: "acct"}
	ctx := security.NewRequestContextForTenantAccountAdmin("tenant-1", "user-1", []string{"acct"})
	prompt := writer.GetSystemPrompt(ctx, core.NBAgentRequest{AccountId: "acct"})

	all := strings.Join(append(append([]string{prompt.Role, prompt.OutputFormat}, prompt.Instructions...), prompt.Constraints...), "\n")
	assert.NotContains(t, all, "TODO")
	assert.Contains(t, all, "__CLUSTER__")
	assert.Contains(t, all, "FUNCTION RESTRICTION")
	assert.Contains(t, all, "NEVER invent a metric name")
	assert.Equal(t, core.AgentPlannerTypeReAct, writer.GetPlannerType())
	assert.False(t, writer.CritiqueEnabled())
	assert.Equal(t, 8, writer.GetMaxIterations())
	assert.True(t, writer.OptOutDefaultTools(), "no shell_execute: with a shell the writer investigated instead of designing")
	assert.Equal(t, core.AgentKnowledgeDisabled, writer.GetKnowledgeMode())

	// Every example answer must itself survive the normaliser untouched: the
	// prompt teaches the shape the wrapper accepts.
	for _, ex := range dashboardWriterExamples() {
		draft, err := normaliseGeneratedDashboard(ex.Answer)
		require.NoError(t, err, "example %q", ex.Question)
		assert.Empty(t, draft.Warnings, "example %q", ex.Question)
		assert.NotEmpty(t, draft.Definition.Panels)
	}
}

// fakeDiscoveryTool answers every call with a fixed text.
type fakeDiscoveryTool struct {
	toolcore.NBTool
	output string
}

func (f fakeDiscoveryTool) Call(toolcore.NbToolContext, toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	return toolcore.NBToolResponse{Data: f.output}, nil
}

// The cap has to be on the tool result: react_4, the default engine, never calls
// an agent's UpdateToolResponseForPlanner, so a cap there only held on react_3.
func TestDashboardWriterCapsDiscoveryOutput(t *testing.T) {
	long := capDiscovery(fakeDiscoveryTool{output: strings.Repeat("x", 10000)})
	out, err := long.Call(toolcore.NbToolContext{}, toolcore.NBToolCallRequest{})
	require.NoError(t, err)
	assert.Less(t, len(out.Data), 10000)
	assert.Contains(t, out.Data, "truncated")

	short := capDiscovery(fakeDiscoveryTool{output: "short"})
	out, err = short.Call(toolcore.NbToolContext{}, toolcore.NBToolCallRequest{})
	require.NoError(t, err)
	assert.Equal(t, "short", out.Data)

	writer := &dashboardWriterAgent{accountId: "acct", discoveryProvider: "victoriametrics"}
	for _, tool := range writer.GetSupportedTools(nil) {
		capped, ok := tool.(tools.CappedOutputTool)
		require.True(t, ok, "%s must be capped", tool.Name())
		switch inner := capped.NBTool.(type) {
		case tools.MetricsListTool:
			assert.Equal(t, "victoriametrics", inner.Provider, "reuses the provider the builder resolved")
		case tools.ListMetricsLabelsTool:
			assert.Equal(t, "victoriametrics", inner.Provider, "reuses the provider the builder resolved")
		default:
			t.Fatalf("unexpected tool %T", inner)
		}
	}
}

// The builder runs the writer; it calls no tools itself, and answering with the
// writer's would resolve the account's provider again for nothing.
func TestDashboardBuilderHasNoToolsOfItsOwn(t *testing.T) {
	builder, err := newDashboardBuilderAgent("acct", services_server.ObservabilityProvider{Provider: "prometheus"})
	require.NoError(t, err)
	assert.Empty(t, builder.GetSupportedTools(nil))
}

// A range vector parses, and the browser's instant check reads its matrix as
// data, but every panel query fails on it once the chart runs a range query.
func TestCheckGeneratedPromQLRequiresAnInstantVector(t *testing.T) {
	for _, expr := range []string{`up{__CLUSTER__}[5m]`, `rate(x{__CLUSTER__}[5m])[30m:1m]`} {
		err := checkGeneratedPromQL(expr)
		require.Error(t, err, expr)
		assert.Contains(t, err.Error(), "range vector", expr)
	}
	for _, expr := range []string{`sum(rate(x{__CLUSTER__}[5m]))`, `scalar(sum(up))`, `42`, `max_over_time(rate(x[5m])[30m:1m])`} {
		assert.NoError(t, checkGeneratedPromQL(expr), expr)
	}
}

// A strict decode used to fail on one mistyped field, after which the generic JSON
// fallback took the first inner object — a panel — as the whole dashboard, losing
// every other panel with no warning.
func TestDashboardBuilderToleratesMistypedFields(t *testing.T) {
	answer := `{"title": "T", "panels": [
	  {"title": "A", "type": "stat", "width": "3", "height": 4.6, "targets": [{"expr": "sum(up{__CLUSTER__})"}]},
	  {"title": "B", "type": "timeseries", "width": 6, "targets": ["sum(rate(container_cpu_usage_seconds_total{__CLUSTER__}[5m]))"]}
	]}`

	draft := decodeDraft(t, runDashboardBuilder(t, answer, nil))

	assert.Equal(t, "T", draft.Title)
	require.Len(t, draft.Definition.Panels, 2)
	assert.Equal(t, 3, draft.Definition.Panels[0].GridPos.W)
	assert.Equal(t, 5, draft.Definition.Panels[0].GridPos.H)
	require.Len(t, draft.Definition.Panels[1].Targets, 1, "a bare query string is a target")
	assert.Empty(t, draft.Warnings)
}

func TestDashboardBuilderNeverTakesAPanelForTheDashboard(t *testing.T) {
	// Prose around the JSON pushes decoding onto the fallback path.
	answer := `Here you go: {"title": "Outer", "panels": [{"title": "Inner", "width": "wide", "targets": [{"expr": "up{__CLUSTER__}"}]}]} hope it helps`

	draft := decodeDraft(t, runDashboardBuilder(t, answer, nil))

	assert.Equal(t, "Outer", draft.Title)
	require.Len(t, draft.Definition.Panels, 1)
	assert.Equal(t, "Inner", draft.Definition.Panels[0].Title)
	assert.Equal(t, 6, draft.Definition.Panels[0].GridPos.W, "an unreadable width falls back to the type default")

	resp := runDashboardBuilder(t, `{"title": "Just a panel", "type": "stat", "targets": [{"expr": "up"}]}`, nil)
	assert.Equal(t, core.ConversationStatusFailed, resp.Status, "a lone panel is not a dashboard")
}

func TestDashboardBuilderCleansUnitsAndHeights(t *testing.T) {
	answer := `{"title": "T", "panels": [
	  {"title": "A", "unit": "requests per second", "height": 400, "targets": [{"expr": "up{__CLUSTER__}"}]},
	  {"title": "B", "unit": "µs µs µs µs µs µs", "height": 1, "targets": [{"expr": "up{__CLUSTER__}"}]}
	]}`

	draft := decodeDraft(t, runDashboardBuilder(t, answer, nil))

	require.Len(t, draft.Definition.Panels, 2)
	assert.Equal(t, "requests per sec", draft.Definition.Panels[0].Unit)
	assert.Equal(t, maxPanelHeight, draft.Definition.Panels[0].GridPos.H)
	assert.Equal(t, "µs µs µs µs µs µ", draft.Definition.Panels[1].Unit, "cut by characters, never mid-rune, with no marker")
	assert.Equal(t, minPanelHeight, draft.Definition.Panels[1].GridPos.H)
}

// The first live run on dev charted CPU by (namespace, pod): 593 series on a panel
// that draws 20. Chart queries that can fan out are capped; single-series shapes
// and stat panels are not touched.
func TestCapChartSeries(t *testing.T) {
	for expr, want := range map[string]string{
		`sum(rate(container_cpu_usage_seconds_total{image!=""}[5m])) by (namespace, pod)`: `topk(10, sum(rate(container_cpu_usage_seconds_total{image!=""}[5m])) by (namespace, pod))`,
		`rate(http_requests_total{__CLUSTER__}[5m])`:                                      `topk(10, rate(http_requests_total{__CLUSTER__}[5m]))`,
		`container_memory_working_set_bytes`:                                              `topk(10, container_memory_working_set_bytes)`,
		`(sum without (instance) (up))`:                                                   `topk(10, (sum without (instance) (up)))`,
		`sum(up)`:                                                                         `sum(up)`,
		`100 * sum(rate(a[5m])) / sum(b)`:                                                 `100 * sum(rate(a[5m])) / sum(b)`,
		`topk(5, sum by (pod) (up))`:                                                      `topk(5, sum by (pod) (up))`,
		`bottomk(3, up)`:                                                                  `bottomk(3, up)`,
		`vector(1) + 1`:                                                                   `vector(1) + 1`,
		`scalar(sum(up))`:                                                                 `scalar(sum(up))`,
		// One series out, though the top level is a function call.
		`histogram_quantile(0.99, sum by (le) (rate(x_bucket[5m])))`: `histogram_quantile(0.99, sum by (le) (rate(x_bucket[5m])))`,
		`round(sum(x))`:       `round(sum(x))`,
		`absent(up{job="a"})`: `absent(up{job="a"})`,
		// Still one series per label value.
		`histogram_quantile(0.99, sum by (le, service) (rate(x_bucket[5m])))`: `topk(10, histogram_quantile(0.99, sum by (le, service) (rate(x_bucket[5m]))))`,
		`round(sum by (pod) (x))`:     `topk(10, round(sum by (pod) (x)))`,
		`sum(a) / on() group_right b`: `topk(10, sum(a) / on() group_right b)`,
	} {
		got, _ := capChartSeries(expr)
		assert.Equal(t, want, got, expr)
		require.NoError(t, checkGeneratedPromQL(got), "a capped query must still parse: %s", got)
	}
}

func TestDashboardBuilderCapsOnlyChartPanels(t *testing.T) {
	answer := `{"title": "T", "panels": [
	  {"title": "CPU by pod", "type": "timeseries", "targets": [{"expr": "sum(rate(container_cpu_usage_seconds_total[5m])) by (namespace, pod)"}]},
	  {"title": "Restarts", "type": "stat", "targets": [{"expr": "sum by (namespace) (kube_pod_container_status_restarts_total)"}]}
	]}`

	draft := decodeDraft(t, runDashboardBuilder(t, answer, nil))

	require.Len(t, draft.Definition.Panels, 2)
	assert.True(t, strings.HasPrefix(draft.Definition.Panels[0].Targets[0].Expr, "topk(10, "))
	assert.Equal(t, "sum by (namespace) (kube_pod_container_status_restarts_total)", draft.Definition.Panels[1].Targets[0].Expr, "stat panels combine series themselves")
	require.Len(t, draft.Warnings, 1)
	assert.Contains(t, draft.Warnings[0], `"CPU by pod": limited a per-series query to the top 10 series at each point in time`)
}

// The executor renders every system prompt as a Go template and, on a render
// error, runs the agent with an EMPTY system prompt (agents/core/executor.go). The
// legend placeholders this prompt teaches ({{namespace}}) are template syntax, so
// the first live runs on dev had no instructions at all. Render it the same way.
func TestDashboardWriterPromptRendersAsTemplate(t *testing.T) {
	writer := &dashboardWriterAgent{accountId: "acct"}
	ctx := security.NewRequestContextForTenantAccountAdmin("tenant-1", "user-1", []string{"acct"})
	req := core.NBAgentRequest{AccountId: "acct", Query: "cluster health"}
	for _, planner := range []core.AgentPlannerType{core.AgentPlannerTypeReAct, core.AgentPlannerTypeReAct3} {
		rendered, err := core.GetPromptTemplate(writer.GetSystemPrompt(ctx, req), req, planner).Format(map[string]any{"history": ""})
		require.NoError(t, err, "planner %s", planner)
		assert.Contains(t, rendered, "Design one monitoring dashboard", "planner %s: instructions must reach the model", planner)
		assert.Contains(t, rendered, `"legend_format": "{{namespace}}"`, "planner %s: placeholders reach the model literally", planner)
		assert.NotContains(t, rendered, `{{"{{"}}`, "planner %s: escape syntax must not leak", planner)
	}
}
