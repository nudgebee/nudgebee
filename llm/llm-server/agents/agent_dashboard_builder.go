package agents

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/prometheus/prometheus/promql/parser"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
)

// DashboardBuilderAgentName backs the Dashboards page's "Generate with AI" option.
// It turns a description into a custom-dashboard draft in the same file shape the
// page's Import option reads; the page opens that draft in the editor unsaved, so
// nothing is stored until the user presses Save there.
const DashboardBuilderAgentName = "dashboard_builder"

// dashboardWriterAgentName is the ReAct agent that does the discovery and writing.
// DashboardBuilderAgent wraps it the way metricsQueryAgent wraps promql_query: the
// writer answers in JSON and the wrapper checks that answer in Go before it is
// handed to the page.
const dashboardWriterAgentName = "dashboard_writer"

const (
	// maxGeneratedPanels bounds one draft. Every panel is one query per render, so
	// a runaway answer would also be a slow dashboard.
	maxGeneratedPanels = 24
	// maxTargetsPerPanel matches the editor's practical limit: charts cap at 20
	// series, and more than a few queries on one panel is never what was asked.
	maxTargetsPerPanel = 5
	maxPanelUnitLength = 16
	defaultPanelHeight = 8
	// Height is in 30px grid rows; the server validates only width, so an
	// unclamped height is a 12,000px panel.
	minPanelHeight = 3
	maxPanelHeight = 24
	// maxChartSeries is the topk a per-series chart query is capped to. The panel
	// draws at most 20 series; on dev the writer's per-pod CPU chart returned 593.
	maxChartSeries = 10
)

// generatedPanelTypes are the dashboard panel types a generated draft may use.
// Mirrors api-server dashboard.Viz* (model.go); logs/traces/entity tables are out
// of scope for generation, so every non-text panel here is a metrics panel.
var generatedPanelTypes = map[string]bool{
	"timeseries": true,
	"stat":       true,
	"gauge":      true,
	"bar":        true,
	"table":      true,
	"text":       true,
}

// panelTypeAliases folds names a model reaches for into the dashboard's own.
var panelTypeAliases = map[string]string{
	"line":       "timeseries",
	"graph":      "timeseries",
	"chart":      "timeseries",
	"singlestat": "stat",
	"number":     "stat",
	"barchart":   "bar",
	"markdown":   "text",
}

// DashboardBuilderAgent is a Custom agent: it runs the writer, then normalises and
// checks its answer. It is not registered in the agent catalogue — like
// metrics_query it is reached only through its own endpoint, which builds it per
// request after checking the account's metrics backend.
type DashboardBuilderAgent struct {
	accountId string
	provider  string
	writer    core.NBAgent
	executor  agentExecutorFunc
}

// NewDashboardBuilderAgent returns the builder for an account, or an error the
// endpoint shows as-is when the account's metrics backend does not speak PromQL.
func NewDashboardBuilderAgent(ctx *security.RequestContext, accountId string) (core.NBAgent, error) {
	provider, err := tools.GetMetricsProvider(accountId)
	if err != nil {
		return nil, fmt.Errorf("dashboard_builder: resolve metrics provider: %w", err)
	}
	return newDashboardBuilderAgent(accountId, provider)
}

func newDashboardBuilderAgent(accountId string, provider services_server.ObservabilityProvider) (*DashboardBuilderAgent, error) {
	providerName := strings.ToLower(strings.TrimSpace(provider.Provider))
	if !dashboardProviderSpeaksPromQL(providerName) {
		return nil, fmt.Errorf("AI dashboard generation currently supports Prometheus-compatible metrics backends only; this account's metrics come from %q", provider.Provider)
	}
	return &DashboardBuilderAgent{
		accountId: accountId,
		provider:  providerName,
		// The provider is already resolved; the writer's discovery tools reuse it
		// rather than asking the api-server again.
		writer: &dashboardWriterAgent{accountId: accountId, discoveryProvider: tools.MetricsDiscoveryProviderFor(provider)},
	}, nil
}

// dashboardProviderSpeaksPromQL is an allowlist: only the backends api-server
// runs a metrics query against as PromQL (observability/service.go's utilisation
// switch: prometheus, victoria_metrics, chronosphere, cubeapm, and openobserve,
// a PromQL subset whose gaps the browser's panel check catches), plus an
// unresolved provider, which falls back to the agent's Prometheus. Everything
// else — New Relic (NRQL), Dynatrace, SolarWinds, Splunk, Datadog,
// Elasticsearch, the cloud consoles, and any backend added later — is refused
// before the LLM runs, since the writer only writes PromQL.
func dashboardProviderSpeaksPromQL(provider string) bool {
	switch provider {
	case "", "prometheus", "victoria_metrics", "victoria-metrics", "victoriametrics", "chronosphere", "cubeapm", "openobserve":
		return true
	}
	return false
}

func (a *DashboardBuilderAgent) GetName() string { return DashboardBuilderAgentName }

func (a *DashboardBuilderAgent) GetNameAliases() []string { return []string{"Dashboard Builder"} }

func (a *DashboardBuilderAgent) GetDescription() string {
	return `Builds a custom dashboard draft (metrics and text panels) from a natural-language description, using metric names that exist on the account. Generation only — it never saves the dashboard.`
}

func (a *DashboardBuilderAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	return core.NBAgentPrompt{Role: "an SRE expert that designs monitoring dashboards"}
}

// GetSupportedTools: the builder calls no tools itself; the writer it runs has
// its own. Answering with the writer's would resolve its provider for nothing.
func (a *DashboardBuilderAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{}
}

func (a *DashboardBuilderAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeCustom
}

// GetMemoryEnabled: a dashboard request is a one-off build, not a conversation
// worth mining for long-term memory.
func (a *DashboardBuilderAgent) GetMemoryEnabled() bool { return false }

func (a *DashboardBuilderAgent) Execute(ctx *security.RequestContext, query core.NBAgentRequest) (core.NBAgentResponse, error) {
	nbRequestContext := toolcore.NbToolContext{
		Ctx:                     ctx,
		AccountId:               a.accountId,
		ConversationId:          query.ConversationId,
		ParentAgentId:           nestedAgentParentID(query),
		MessageId:               query.MessageId,
		QueryContext:            query.QueryContext,
		QueryConfig:             query.QueryConfig,
		UserId:                  query.UserId,
		InheritSkillsFromAgents: append(query.InheritSkillsFromAgents, a.GetName()),
		OriginalQuery:           query.OriginalQuery,
		OriginalQueryFromUser:   query.OriginalQueryFromUser,
		SelectedSkillIds:        query.SelectedSkillIds,
		// Disabled outright, not inherited: the planner injects search_skills /
		// load_skills from the request's policy, and on dev the writer spent two
		// of its four minutes searching the knowledge base for "dashboard JSON".
		KnowledgePolicy:         string(core.KnowledgeDisabled),
		KnowledgePolicyResolved: true,
	}

	nbToolCallRequest := toolcore.NBToolCallRequest{
		Command:   dashboardWriterCommand(query.Query),
		Context:   query.QueryContext,
		Arguments: map[string]any{},
	}

	executor := a.executor
	if executor == nil {
		executor = core.ExecuteAgentToolCall
	}
	resp, err := executor(nbRequestContext, a.writer, nbToolCallRequest)
	if err != nil {
		return errorResponse(a.GetName(), fmt.Errorf("dashboard generation: %w", err)), nil
	}
	if len(resp.Response) == 0 {
		return errorResponse(a.GetName(), fmt.Errorf("dashboard generation: %s returned no answer", a.writer.GetName())), nil
	}

	draft, err := normaliseGeneratedDashboard(resp.Response[0])
	if err != nil {
		return errorResponse(a.GetName(), err), nil
	}

	data, err := common.MarshalJson(draft)
	if err != nil {
		return errorResponse(a.GetName(), fmt.Errorf("dashboard generation: marshal result: %w", err)), nil
	}

	return core.NBAgentResponse{
		AgentName: a.GetName(),
		Response:  []string{string(data)},
		Status:    core.ConversationStatusCompleted,
	}, nil
}

// dashboardWriterCommand frames the user's text as a design brief. Passed bare, a
// description such as "Kubernetes cluster health: CPU, memory, pod restarts" is
// indistinguishable from a question about the cluster, and the writer answered it
// as one.
func dashboardWriterCommand(description string) string {
	return "Design a monitoring dashboard for the following description and return only the dashboard JSON object. " +
		"Do not answer the description as a question about the current state of the system.\n\nDashboard description: " +
		strings.TrimSpace(description)
}

// generatedDashboard is the draft handed to the page. It is the dashboard file
// shape the page's Import option reads (`{title, description, definition:
// {panels}}`), plus the notes the normaliser made while checking it. The page
// applies the account scope; no panel here names an account.
type generatedDashboard struct {
	Title       string              `json:"title"`
	Description string              `json:"description,omitempty"`
	Definition  generatedDefinition `json:"definition"`
	// Warnings lists what the normaliser dropped or changed, for the page to show
	// next to the draft. Not part of the dashboard; the importer ignores it.
	Warnings []string `json:"warnings,omitempty"`
}

type generatedDefinition struct {
	Panels []generatedPanel `json:"panels"`
}

type generatedPanel struct {
	Id          int               `json:"id"`
	Title       string            `json:"title"`
	Description string            `json:"description,omitempty"`
	Type        string            `json:"type"`
	Datasource  string            `json:"datasource"`
	GridPos     generatedGridPos  `json:"grid_pos"`
	Targets     []generatedTarget `json:"targets,omitempty"`
	Unit        string            `json:"unit,omitempty"`
	Content     string            `json:"content,omitempty"`
}

type generatedGridPos struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

type generatedTarget struct {
	RefId        string `json:"ref_id"`
	Expr         string `json:"expr"`
	LegendFormat string `json:"legend_format,omitempty"`
}

// writerDashboard is what the writer is asked for. It accepts panels either at the
// top level or under definition, since both shapes appear in the examples a model
// has seen, and is lenient about a panel's width/height spelling.
type writerDashboard struct {
	Title       string        `json:"title"`
	Description string        `json:"description"`
	Panels      []writerPanel `json:"panels"`
	Definition  struct {
		Panels []writerPanel `json:"panels"`
	} `json:"definition"`
}

type writerPanel struct {
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Type        string  `json:"type"`
	Unit        string  `json:"unit"`
	Content     string  `json:"content"`
	Width       flexInt `json:"width"`
	Height      flexInt `json:"height"`
	GridPos     struct {
		W flexInt `json:"w"`
		H flexInt `json:"h"`
	} `json:"grid_pos"`
	Targets []writerTarget `json:"targets"`
}

type writerTarget struct {
	Expr         string `json:"expr"`
	LegendFormat string `json:"legend_format"`
}

// UnmarshalJSON accepts a bare query string as well as {"expr": ...}. A strict
// decode failing on one panel would fail the whole answer.
func (t *writerTarget) UnmarshalJSON(data []byte) error {
	var expr string
	if err := json.Unmarshal(data, &expr); err == nil {
		t.Expr = expr
		return nil
	}
	type plain writerTarget
	var p plain
	if err := json.Unmarshal(data, &p); err != nil {
		return fmt.Errorf("dashboard target: %w", err)
	}
	*t = writerTarget(p)
	return nil
}

// flexInt decodes a number, a fractional number or a numeric string, and treats
// anything else as unset. Models write "width": "6" and "width": 4.5; failing the
// decode for that would throw away an otherwise good dashboard.
type flexInt int

func (f *flexInt) UnmarshalJSON(data []byte) error {
	var n float64
	if err := json.Unmarshal(data, &n); err == nil {
		*f = flexInt(math.Round(n))
		return nil
	}
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		if v, perr := strconv.ParseFloat(strings.TrimSpace(str), 64); perr == nil {
			*f = flexInt(math.Round(v))
			return nil
		}
	}
	*f = 0
	return nil
}

// normaliseGeneratedDashboard turns the writer's answer into a draft the dashboard
// server will accept, dropping (with a warning) every panel it cannot vouch for.
//
// What it guarantees per kept panel: a known type, a title, a width of 1..12, and
// for a metrics panel at least one target whose PromQL parses. It does not run the
// queries — the page does that through the same request a panel renders with, so
// "returns data" is checked on the path that will actually draw the chart.
//
// It never returns a draft with zero panels and a nil error: an empty editor after
// a spinner gives the user nothing to act on.
func normaliseGeneratedDashboard(raw string) (generatedDashboard, error) {
	trimmed := stripQueryCodeFence(strings.TrimSpace(raw))
	if trimmed == "" {
		return generatedDashboard{}, fmt.Errorf("dashboard generation: empty response")
	}
	parsed, ok := decodeWriterDashboard(trimmed)
	if !ok {
		return generatedDashboard{}, fmt.Errorf("dashboard generation: the answer was not a dashboard (%s)", truncateString(trimmed, 200))
	}
	panels := parsed.panels()

	draft := generatedDashboard{
		Title:       strings.TrimSpace(parsed.Title),
		Description: strings.TrimSpace(parsed.Description),
		Definition:  generatedDefinition{Panels: []generatedPanel{}},
	}
	if draft.Title == "" {
		draft.Title = "Generated dashboard"
	}

	for i, p := range panels {
		label := strings.TrimSpace(p.Title)
		if label == "" {
			label = fmt.Sprintf("panel %d", i+1)
		}
		if len(draft.Definition.Panels) == maxGeneratedPanels {
			draft.Warnings = append(draft.Warnings, fmt.Sprintf("Kept the first %d panels; %d more were left out.", maxGeneratedPanels, len(panels)-i))
			break
		}
		panel, warning := normaliseGeneratedPanel(p, len(draft.Definition.Panels)+1)
		if warning != "" {
			draft.Warnings = append(draft.Warnings, fmt.Sprintf("%q: %s", label, warning))
		}
		if panel != nil {
			draft.Definition.Panels = append(draft.Definition.Panels, *panel)
		}
	}

	if len(draft.Definition.Panels) == 0 {
		reason := "the answer contained no panels"
		if len(draft.Warnings) > 0 {
			reason = strings.Join(draft.Warnings, "; ")
		}
		return generatedDashboard{}, fmt.Errorf("dashboard generation: no usable panels — %s", reason)
	}
	return draft, nil
}

func (d writerDashboard) panels() []writerPanel {
	if len(d.Panels) > 0 {
		return d.Panels
	}
	return d.Definition.Panels
}

// decodeWriterDashboard reads the answer as a dashboard: directly when it is clean
// JSON, else from the first embedded object that carries panels. Requiring panels
// matters for the fallback — common.ExtractAndUnmarshalJSON accepts the first inner
// block that decodes to anything, and a lone panel object decodes as a dashboard
// titled after that panel.
func decodeWriterDashboard(text string) (writerDashboard, bool) {
	var direct writerDashboard
	if err := json.Unmarshal([]byte(text), &direct); err == nil && len(direct.panels()) > 0 {
		return direct, true
	}
	var extracted writerDashboard
	if err := common.ExtractAndUnmarshalJSON([]byte(text), &extracted); err == nil && len(extracted.panels()) > 0 {
		return extracted, true
	}
	// A clean but panel-less dashboard is a real (empty) answer, reported as such.
	if err := json.Unmarshal([]byte(text), &direct); err == nil && strings.HasPrefix(text, "{") {
		return direct, true
	}
	return writerDashboard{}, false
}

// normaliseGeneratedPanel returns the panel to keep (nil to drop it) and a note on
// what was dropped or changed ("" when nothing was).
func normaliseGeneratedPanel(p writerPanel, id int) (*generatedPanel, string) {
	panelType := strings.ToLower(strings.TrimSpace(p.Type))
	if alias, ok := panelTypeAliases[panelType]; ok {
		panelType = alias
	}
	if panelType == "" {
		panelType = "timeseries"
	}
	if !generatedPanelTypes[panelType] {
		return nil, fmt.Sprintf("dropped — unsupported panel type %q", p.Type)
	}

	title := strings.TrimSpace(p.Title)
	if title == "" {
		title = fmt.Sprintf("Panel %d", id)
	}

	panel := &generatedPanel{
		Id:          id,
		Title:       title,
		Description: strings.TrimSpace(p.Description),
		Type:        panelType,
		// The importer writes the same placeholder on text panels; the server
		// stops validating a text panel before it reads the datasource.
		Datasource: "metrics",
		GridPos: generatedGridPos{
			W: clampPanelWidth(firstPositive(int(p.Width), int(p.GridPos.W)), panelType),
			H: clampPanelHeight(firstPositive(int(p.Height), int(p.GridPos.H))),
		},
		Unit: truncateRunesPlain(strings.TrimSpace(p.Unit), maxPanelUnitLength),
	}

	if panelType == "text" {
		panel.Content = strings.TrimSpace(p.Content)
		if panel.Content == "" {
			return nil, "dropped — text panel had no content"
		}
		panel.Unit = ""
		return panel, ""
	}

	var notes []string
	for _, t := range p.Targets {
		expr := strings.TrimSpace(t.Expr)
		if expr == "" {
			continue
		}
		if len(panel.Targets) == maxTargetsPerPanel {
			notes = append(notes, fmt.Sprintf("kept the first %d queries", maxTargetsPerPanel))
			break
		}
		if err := checkGeneratedPromQL(expr); err != nil {
			notes = append(notes, fmt.Sprintf("dropped a query that does not parse (%s)", err))
			continue
		}
		if panelType == "timeseries" || panelType == "bar" {
			if capped, ok := capChartSeries(expr); ok {
				expr = capped
				notes = append(notes, fmt.Sprintf("limited a per-series query to the top %d series at each point in time", maxChartSeries))
			}
		}
		panel.Targets = append(panel.Targets, generatedTarget{
			RefId:        refIdFor(len(panel.Targets)),
			Expr:         expr,
			LegendFormat: strings.TrimSpace(t.LegendFormat),
		})
	}
	if len(panel.Targets) == 0 {
		reason := "no query"
		if len(notes) > 0 {
			reason = strings.Join(notes, "; ")
		}
		return nil, "dropped — " + reason
	}
	return panel, strings.Join(notes, "; ")
}

// checkGeneratedPromQL parses one expression the way promql_query checks its own
// answers: the __CLUSTER__ placeholder is removed first (it is not PromQL; the
// metrics layer expands it), and semicolon-joined multi-queries are refused rather
// than split, because one panel target is one query.
func checkGeneratedPromQL(expr string) error {
	clean := strings.ReplaceAll(expr, "__CLUSTER__", "")
	if strings.Contains(clean, ";") {
		return fmt.Errorf("one query per target, found ';'")
	}
	parsed, err := promQLParser.ParseExpr(clean)
	if err != nil {
		return fmt.Errorf("%s", truncateString(err.Error(), 160))
	}
	// Panels run their queries as instant or range queries, and both reject a
	// range vector (`x[5m]`, a subquery) as the result. The parse alone let one
	// through, and the browser check could not catch it either: its instant
	// query reads the matrix as data, then the chart's range query fails on
	// every load.
	switch parsed.Type() {
	case parser.ValueTypeVector, parser.ValueTypeScalar:
		return nil
	case parser.ValueTypeMatrix:
		return fmt.Errorf("the query returns a range vector; a panel needs an instant vector (wrap a [range] in rate() or a *_over_time() function)")
	default:
		return fmt.Errorf("the query returns a %s; a panel needs an instant vector", parsed.Type())
	}
}

// capChartSeries wraps a chart query in topk when it can return one series per
// pod, container or label value. The prompt asks for this and the model does not
// reliably do it — the first live run charted CPU by (namespace, pod), 593 series
// for a panel that draws 20. Queries that return one series (an aggregation with
// no grouping, `histogram_quantile` over `by (le)`, arithmetic over them) and
// queries already ranked with topk/bottomk are left as written. topk ranks at
// each step of a range query, so a chart can show more than 10 distinct series
// across its window when the leaders change; it bounds each step, not the window.
func capChartSeries(expr string) (string, bool) {
	parsed, err := promQLParser.ParseExpr(strings.ReplaceAll(expr, "__CLUSTER__", ""))
	if err != nil || parsed.Type() != parser.ValueTypeVector {
		return expr, false
	}
	if !returnsManySeries(parsed) {
		return expr, false
	}
	return fmt.Sprintf("topk(%d, %s)", maxChartSeries, expr), true
}

// returnsManySeries reports whether an expression can return more than one series.
// It errs towards false for shapes it cannot read: a wrongly skipped cap costs a
// busy chart, a wrongly applied one a false warning on every single-series panel.
func returnsManySeries(node parser.Node) bool {
	switch n := node.(type) {
	case *parser.ParenExpr:
		return returnsManySeries(n.Expr)
	case *parser.UnaryExpr:
		return returnsManySeries(n.Expr)
	case *parser.StepInvariantExpr:
		return returnsManySeries(n.Expr)
	case *parser.VectorSelector, *parser.MatrixSelector:
		return true
	case *parser.SubqueryExpr:
		return returnsManySeries(n.Expr)
	case *parser.AggregateExpr:
		switch n.Op {
		case parser.TOPK, parser.BOTTOMK:
			return false // already ranked
		case parser.COUNT_VALUES:
			return true // one series per distinct value
		}
		return n.Without || len(n.Grouping) > 0
	case *parser.BinaryExpr:
		return returnsManySeries(n.LHS) || returnsManySeries(n.RHS)
	case *parser.Call:
		switch n.Func.Name {
		case "vector", "time", "scalar", "absent", "absent_over_time", "pi":
			return false
		case "histogram_quantile":
			// Its result keeps every label but `le`: `by (le)` alone is one series.
			if len(n.Args) == 2 {
				if agg, ok := unwrapParens(n.Args[1]).(*parser.AggregateExpr); ok && !agg.Without {
					for _, label := range agg.Grouping {
						if label != "le" {
							return true
						}
					}
					return false
				}
				return returnsManySeries(n.Args[1])
			}
		}
		for _, arg := range n.Args {
			switch arg.Type() {
			case parser.ValueTypeVector, parser.ValueTypeMatrix:
				return returnsManySeries(arg)
			}
		}
		return false
	}
	return false
}

func unwrapParens(node parser.Expr) parser.Expr {
	for {
		paren, ok := node.(*parser.ParenExpr)
		if !ok {
			return node
		}
		node = paren.Expr
	}
}

// clampPanelWidth keeps a width inside the 12-column grid, defaulting by type:
// stat and gauge read as a row of small tiles, text and tables as full or half rows.
func clampPanelWidth(w int, panelType string) int {
	if w <= 0 {
		switch panelType {
		case "stat", "gauge":
			return 3
		case "text":
			return 12
		default:
			return 6
		}
	}
	if w > 12 {
		return 12
	}
	return w
}

func clampPanelHeight(h int) int {
	switch {
	case h <= 0:
		return defaultPanelHeight
	case h < minPanelHeight:
		return minPanelHeight
	case h > maxPanelHeight:
		return maxPanelHeight
	}
	return h
}

// truncateRunesPlain cuts to n characters with no marker: the result is shown to users
// as a unit suffix, so the agents package's truncateString (which appends a
// "... (truncated" note and cuts by bytes) cannot be used here.
func truncateRunesPlain(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n]))
}

func firstPositive(values ...int) int {
	for _, v := range values {
		if v > 0 {
			return v
		}
	}
	return 0
}

func refIdFor(i int) string {
	return string(rune('A' + i))
}

// dashboardWriterAgent discovers metric names and writes the dashboard JSON. It is
// a ReAct agent with promql_query's discovery tools and PromQL rules; its answer is
// checked by DashboardBuilderAgent, never shown to the user directly.
type dashboardWriterAgent struct {
	accountId string
	// discoveryProvider is the metrics_list provider, set when the builder has
	// already resolved the account's backend; empty means resolve it here.
	discoveryProvider string
}

func (w *dashboardWriterAgent) GetName() string { return dashboardWriterAgentName }

func (w *dashboardWriterAgent) GetNameAliases() []string { return []string{"Dashboard Writer"} }

func (w *dashboardWriterAgent) GetDescription() string {
	return `Writes a custom dashboard as JSON (metrics and text panels with PromQL queries) from a description, after checking which metrics exist.`
}

func (w *dashboardWriterAgent) GetPlannerType() core.AgentPlannerType {
	return core.AgentPlannerTypeReAct
}

// GetMaxIterations: discovery is one metrics_list per technology plus the odd
// label lookup, then the answer. More than this is a retry loop, not progress.
func (w *dashboardWriterAgent) GetMaxIterations() int { return 8 }

// CritiqueEnabled: the answer is JSON checked in Go, not prose for a critic.
func (w *dashboardWriterAgent) CritiqueEnabled() bool { return false }

func (w *dashboardWriterAgent) GetMemoryEnabled() bool { return false }

// OptOutDefaultTools keeps the framework from adding shell_execute (and watch
// tools) to the writer. With a shell, a request like "cluster health" reads as a
// question to investigate: on dev the writer ran kubectl and answered with a prose
// health report instead of a dashboard. Discovery tools are all it needs.
func (w *dashboardWriterAgent) OptOutDefaultTools() bool { return true }

// GetKnowledgeMode: knowledge-base skills are runbooks and SOPs, not metric
// names; loading them only gives the writer another way to wander off.
func (w *dashboardWriterAgent) GetKnowledgeMode() core.AgentKnowledgeMode {
	return core.AgentKnowledgeDisabled
}

func (w *dashboardWriterAgent) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	metricsProvider := w.discoveryProvider
	if metricsProvider == "" {
		// Resolved once rather than per tool, as promql_query does: the lookup is
		// an uncached call to the api-server.
		metricsProvider = tools.MetricsDiscoveryProvider(w.accountId)
	}
	return []toolcore.NBTool{
		capDiscovery(tools.MetricsListTool{Provider: metricsProvider}),
		capDiscovery(tools.ListMetricsLabelsTool{Provider: metricsProvider}),
	}
}

// discoveryOutputCap caps discovery output for the same reason promql_query
// does: a broad keyword returns dozens of metric families with full label
// metadata and floods the scratchpad.
func capDiscovery(tool toolcore.NBTool) toolcore.NBTool {
	return tools.CappedOutputTool{NBTool: tool, MaxChars: discoveryOutputCap(), Hint: "use a more specific keyword to narrow results"}
}

func discoveryOutputCap() int {
	if maxChars := config.Config.LlmServerAgentPromqlMaxToolRespChars; maxChars > 0 {
		return maxChars
	}
	return 3000
}

func (w *dashboardWriterAgent) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	instructions := []string{
		"**GOAL:** Design one monitoring dashboard for the user's description and return it as a single JSON object. You only write the dashboard; it is not saved or executed here. The user reviews it in the dashboard editor before saving.",
		"**Plan the panels first:** pick the 4–10 signals that best answer the description. Lead with a row of headline numbers (stat or gauge), then trends over time (timeseries), then breakdowns (bar or table). Add one text panel only if the description asks for notes or context.",
		"**Ground every query in real metrics:**",
		"   - For Kubernetes cluster, node, pod and container signals, prefer the ground truth metrics listed in the constraints.",
		"   - For anything else (redis, kafka, postgres, jvm, nginx, an application's own metrics), call `metrics_list` once per technology keyword and use only metric names it returns.",
		"   - Call `metrics_labels_list` only when you need a label name to group or filter by.",
		"   - You may call several discovery tools in the same step when the technologies are independent.",
		"   - NEVER invent a metric name. If no metric exists for a signal the user asked for, leave that panel out and say so in the dashboard description.",
		"**PromQL rules:**",
		"   - ALWAYS include the `__CLUSTER__` label (with no value) as the first label in every metric selector, e.g. `sum(rate(container_cpu_usage_seconds_total{__CLUSTER__ namespace=\"default\"}[5m]))`.",
		"   - One expression per target. Never join queries with `;`.",
		"   - Use `rate()` over a range (typically `[5m]`) for counters; never chart a raw counter.",
		"   - Stat and gauge panels must reduce to a single number (wrap in `sum(...)`, `avg(...)` or `max(...)`); they are evaluated as instant queries.",
		"   - Timeseries and bar panels should aggregate by a meaningful label (`sum by (namespace) (...)`) and keep to roughly 20 series or fewer — use `topk(10, ...)` for per-pod or per-container breakdowns.",
		"   - Use 'instance' label for node filtering, not 'node' label.",
		"   - For latency, use a P99 `histogram_quantile` over `_bucket` series rather than an average.",
		"**Panel fields:**",
		"   - `type`: one of timeseries, stat, gauge, bar, table, text.",
		"   - `width`: columns out of 12 — use 3 for stat/gauge tiles, 6 for charts side by side, 12 for a full-width chart or text.",
		"   - `unit`: a short suffix shown after the number (`%`, `bytes`, `cores`, `req/s`, `ms`, `pods`). It is display text only and does not scale values — divide in the query if you want MiB or percent.",
		"   - `targets`: one or more `{ \"expr\": \"<PromQL>\", \"legend_format\": \"{{label}}\" }`. `legend_format` is optional.",
		"   - `content`: text panels only, plain prose or simple markdown.",
		"**Do not** add account, cluster or namespace selection fields to panels — the dashboard is scoped to an account when it is opened.",
	}
	availableFns := getAvailablePrometheusFunctions()
	if len(availableFns) > 0 {
		instructions = append(instructions,
			"**CRITICAL - FUNCTION RESTRICTION:** Your queries MUST ONLY use the following Prometheus functions. Any other function will cause the query to fail: "+
				strings.Join(availableFns, ", "),
		)
	}

	constraints := []string{
		"You are an SRE expert who designs monitoring dashboards for Kubernetes and cloud environments.",
		"Return exactly one JSON object as the entire final answer — no prose, no explanation, no code fence.",
		"Do not add job name filters to queries.",
		"Every metric name in every query must come from the ground truth list below or from a discovery tool result in this conversation.",
	}
	constraints = append(constraints, staticGroundTruthConstraints...)

	examples := dashboardWriterExamples()

	toolUsage := map[string][]string{
		tools.ToolMetricsList: {
			"Discovers metric names by technology keyword (substring match).",
			"Input: (required) a single technology keyword (e.g. 'redis', 'kafka', 'pg', 'jvm', 'nginx').",
			"Output: matching metric names (capped). Call it once per technology; do not retry the same technology with other keywords.",
		},
		tools.ToolMetricsLabelsList: {
			"Lists the labels of one metric. Use only when you need a label to group or filter by.",
			"Input: (required) exact metric name.",
		},
	}

	for i := range examples {
		examples[i].Answer = templateLiteral(examples[i].Answer)
	}
	return core.NBAgentPrompt{
		Role:         "an SRE expert who designs monitoring dashboards from Prometheus metrics",
		Instructions: templateLiterals(instructions),
		Constraints:  templateLiterals(constraints),
		Examples:     examples,
		OutputFormat: templateLiteral(`A single JSON object: {"title": "...", "description": "...", "panels": [{"title": "...", "description": "...", "type": "timeseries|stat|gauge|bar|table|text", "width": 3-12, "unit": "...", "targets": [{"expr": "<PromQL>", "legend_format": "{{label}}"}], "content": "<text panels only>"}]}. CRITICAL: queries MUST ONLY use functions listed in the **CRITICAL - FUNCTION RESTRICTION** section.`),
		ToolUsage:    toolUsage,
	}
}

// dashboardWriterExamples are the worked answers in the writer prompt, as the
// model should write them (unescaped). GetSystemPrompt escapes a copy.
func dashboardWriterExamples() []core.NBAgentPromptExample {
	return []core.NBAgentPromptExample{
		{
			Question: "Kubernetes cluster health: CPU, memory, pod restarts and node count",
			Answer: `{"title": "Cluster health", "description": "CPU, memory, restarts and node readiness across the cluster.", "panels": [` +
				`{"title": "Ready nodes", "type": "stat", "width": 3, "unit": "nodes", "targets": [{"expr": "sum(kube_node_status_condition{__CLUSTER__ condition=\"Ready\", status=\"true\"})"}]}, ` +
				`{"title": "Cluster CPU utilisation", "type": "gauge", "width": 3, "unit": "%", "targets": [{"expr": "100 * sum(rate(node_cpu_seconds_total{__CLUSTER__ mode!=\"idle\"}[5m])) / sum(machine_cpu_cores{__CLUSTER__})"}]}, ` +
				`{"title": "Cluster memory utilisation", "type": "gauge", "width": 3, "unit": "%", "targets": [{"expr": "100 * (1 - sum(node_memory_MemAvailable_bytes{__CLUSTER__}) / sum(node_memory_MemTotal_bytes{__CLUSTER__}))"}]}, ` +
				`{"title": "Pod restarts (1h)", "type": "stat", "width": 3, "unit": "restarts", "targets": [{"expr": "sum(increase(kube_pod_container_status_restarts_total{__CLUSTER__}[1h]))"}]}, ` +
				`{"title": "CPU by namespace", "type": "timeseries", "width": 6, "unit": "cores", "targets": [{"expr": "topk(10, sum by (namespace) (rate(container_cpu_usage_seconds_total{__CLUSTER__ container!=\"\"}[5m])))", "legend_format": "{{namespace}}"}]}, ` +
				`{"title": "Memory by namespace", "type": "timeseries", "width": 6, "unit": "bytes", "targets": [{"expr": "topk(10, sum by (namespace) (container_memory_working_set_bytes{__CLUSTER__ container!=\"\"}))", "legend_format": "{{namespace}}"}]}]}`,
			Explanation: "All metrics are ground truth Kubernetes metrics, so no discovery call was needed. Headline tiles first, then per-namespace trends limited with topk.",
		},
		{
			Question: "Redis overview",
			AnswerSteps: []core.NBAgentPromptExampleAnswerStep{
				{Tool: tools.ToolMetricsList, Input: "redis",
					Explanation: "Redis metrics are not in the ground truth list, so discover the names this account actually has."},
			},
			Answer: `{"title": "Redis overview", "description": "Memory, clients and command throughput for Redis.", "panels": [` +
				`{"title": "Connected clients", "type": "stat", "width": 3, "unit": "clients", "targets": [{"expr": "sum(redis_connected_clients{__CLUSTER__})"}]}, ` +
				`{"title": "Memory used", "type": "timeseries", "width": 6, "unit": "bytes", "targets": [{"expr": "topk(10, sum by (instance) (redis_memory_used_bytes{__CLUSTER__}))", "legend_format": "{{instance}}"}]}, ` +
				`{"title": "Commands per second", "type": "timeseries", "width": 6, "unit": "ops/s", "targets": [{"expr": "sum(rate(redis_commands_processed_total{__CLUSTER__}[5m]))"}]}]}`,
			Explanation: "Every metric name came from the metrics_list result. Panels for signals with no matching metric were left out.",
		},
	}
}

// braceEscaper turns template delimiters into template literals. The executor
// renders every system prompt as a Go template (core.GetPromptTemplate(...).Format)
// and, when that fails, runs the agent with an EMPTY system prompt. This prompt
// teaches legend placeholders such as {{namespace}}, which parse as a call to an
// undefined template function — the first runs on dev went out with no
// instructions at all. strings.Replacer works in one non-overlapping pass, so the
// inserted text is not itself rewritten.
var braceEscaper = strings.NewReplacer("{{", `{{"{{"}}`, "}}", `{{"}}"}}`)

func templateLiteral(s string) string { return braceEscaper.Replace(s) }

func templateLiterals(lines []string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = templateLiteral(line)
	}
	return out
}

// Compile-time checks that the writer opts into the hooks the planner looks for.
var (
	_ core.NBAgentIterationProvider           = (*dashboardWriterAgent)(nil)
	_ core.NBAgentMemoryProvider              = (*dashboardWriterAgent)(nil)
	_ core.NBAgentReActPlannerCritiqueSupport = (*dashboardWriterAgent)(nil)
	_ core.DefaultToolsOptOut                 = (*dashboardWriterAgent)(nil)
	_ core.NBAgentKnowledgeModeProvider       = (*dashboardWriterAgent)(nil)
	_ core.NBCustomAgent                      = (*DashboardBuilderAgent)(nil)
)
