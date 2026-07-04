package core

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"nudgebee/llm/agents/prompts_repo"
	"nudgebee/llm/security"

	"github.com/tmc/langchaingo/llms"
)

// conversation_optimizer.go backs ai_generate_conversation_optimization — the
// "export & analyze" button on a finished conversation. It does NOT send the
// transcript to the LLM (a conversation can be 900+ model calls); instead it
// builds a bounded PROFILE — cost/flow/latency rolled up by agent type + model,
// plus a thin semantic layer (task + outcome summary) for the top agents by cost —
// and asks the analyzer for judgment findings: model downgrades, redundant agents,
// context bloat, failure root-cause, excessive iteration/fan-out, and cache
// under-utilization. Mechanical waste (retries, failures) is
// found deterministically here, not by the LLM. Every dollar figure is computed by
// us from llm_model_pricing; the LLM only selects targets and writes the prose.

const (
	optTopAgentCount  = 20  // agents (by cost) that get the semantic task/outcome layer
	optTaskMaxChars   = 300 // truncate the (large) agent query to its task portion
	optOutcomeMaxChar = 300 // truncate the agent response when no summary exists
	optExemplarCount  = 3   // real calls surfaced inline per finding (priciest first)
	optBackingIDCount = 5   // backing agent instances linked per finding

	// optUnattributed is the synthetic bucket for model calls whose agent_id did not
	// resolve to an agent. It is real cost (kept in rollups) but has no drill-able
	// instance, so it is never the target of an LLM judgment finding.
	optUnattributed = "(unattributed)"
)

// --- profile (the bounded export, also returned in the response) ---

type OptTotals struct {
	CostUsd         float64 `json:"cost_usd"`
	ModelCalls      int     `json:"model_calls"`
	ToolCalls       int     `json:"tool_calls"`
	Agents          int     `json:"agents"`
	RetryWasteUsd   float64 `json:"retry_waste_usd"`
	FailureWasteUsd float64 `json:"failure_waste_usd"`
	CacheSavingsUsd float64 `json:"cache_savings_usd"`
	// ModelLatencySec is the SUM of per-call model latencies (compute-seconds), and
	// ToolDurationSec the sum of tool wall-times. Because steps run in parallel these
	// are totals of work done, NOT conversation wall-clock — do not present as elapsed time.
	ModelLatencySec float64 `json:"model_latency_sec"`
	ToolDurationSec float64 `json:"tool_duration_sec"`
}

type OptAgentType struct {
	Agent           string   `json:"agent"`
	Instances       int      `json:"instances"`
	ModelCalls      int      `json:"model_calls"`
	ToolCalls       int      `json:"tool_calls"`
	CostUsd         float64  `json:"cost_usd"`
	Models          []string `json:"models"`
	Errors          int      `json:"errors"`
	Retries         int      `json:"retries"`
	AvgInputTokens  int64    `json:"avg_input_tokens"`
	AvgOutputTokens int64    `json:"avg_output_tokens"`
	AvgLatencyMs    int64    `json:"avg_latency_ms"`    // mean per model call
	ModelLatencySec float64  `json:"model_latency_sec"` // sum of model latencies
	ToolDurationSec float64  `json:"tool_duration_sec"` // sum of tool wall-times
}

type OptModelUsage struct {
	Model           string  `json:"model"`
	Provider        string  `json:"provider"`
	Calls           int     `json:"calls"`
	CostUsd         float64 `json:"cost_usd"`
	AvgInputTokens  int64   `json:"avg_input_tokens"`
	AvgOutputTokens int64   `json:"avg_output_tokens"`
	AvgLatencyMs    int64   `json:"avg_latency_ms"` // mean per-call latency for this model
}

type OptSpawnEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Count int    `json:"count"`
}

// OptTopAgent carries the thin semantic layer for one expensive agent instance.
type OptTopAgent struct {
	AgentID    string   `json:"agent_id"`
	Agent      string   `json:"agent"`
	CostUsd    float64  `json:"cost_usd"`
	ToolCalls  int      `json:"tool_calls"`
	Status     string   `json:"status"`
	Task       string   `json:"task"`        // truncated query — what it was asked
	Outcome    string   `json:"outcome"`     // response_summary, or truncated response
	Tools      []string `json:"tools"`       // "name(ok)" / "name(no_data)" / "name(error)"
	LatencySec float64  `json:"latency_sec"` // this instance's model latency + tool wall-time
}

type OptPricing struct {
	Model         string  `json:"model"`
	Provider      string  `json:"provider"`
	InputPerMtok  float64 `json:"input_per_mtok"`
	OutputPerMtok float64 `json:"output_per_mtok"`
}

// OptimizationProfile is the bounded representation sent to the analyzer and
// echoed back so the UI can show/download the "export".
type OptimizationProfile struct {
	ConversationID string          `json:"conversation_id"`
	SessionID      string          `json:"session_id"`
	Title          string          `json:"title"`
	Totals         OptTotals       `json:"totals"`
	AgentsByType   []OptAgentType  `json:"agents_by_type"`
	Models         []OptModelUsage `json:"models"`
	SpawnGraph     []OptSpawnEdge  `json:"spawn_graph"`
	TopCostAgents  []OptTopAgent   `json:"top_cost_agents"`
	Pricing        []OptPricing    `json:"pricing"`
}

// --- findings (the analysis output) ---

// OptEvidenceFact is one verifiable, server-derived data point backing a finding.
// Built from the profile/index (never the LLM), each fact cites the exact source
// path so the hypothesis can be checked against the raw numbers — the prose
// Evidence is the analyst's reasoning, SupportingEvidence is the proof.
type OptEvidenceFact struct {
	Label  string `json:"label"`
	Value  string `json:"value"`
	Source string `json:"source,omitempty"` // profile path the value came from
}

type OptTarget struct {
	Kind      string `json:"kind"` // agent | agent_model
	AgentName string `json:"agent_name,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
	Model     string `json:"model,omitempty"`
	CallCount int    `json:"call_count,omitempty"`
}

// OptExemplar is one concrete, real model call backing a finding — shown inline so
// the most common verification ("is the average representative?") needs no click.
// The priciest calls in the group are surfaced; numbers are this call's actuals.
type OptExemplar struct {
	AgentID      string  `json:"agent_id"`
	Model        string  `json:"model"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	CostUsd      float64 `json:"cost_usd"`
	Task         string  `json:"task,omitempty"`    // what the owning agent was asked
	Outcome      string  `json:"outcome,omitempty"` // what it produced
}

type OptFinding struct {
	ID       string    `json:"id"`
	Type     string    `json:"type"` // retry_waste | failure_waste | model_downgrade | agent_redundant | context_bloat | failure_root_cause | excessive_iteration | cache_underutilization
	Title    string    `json:"title"`
	Target   OptTarget `json:"target"`
	Evidence string    `json:"evidence"`
	// SupportingEvidence is the verifiable proof behind this finding — facts the
	// server derives from the profile/index, not the LLM. Always populated.
	SupportingEvidence []OptEvidenceFact `json:"supporting_evidence,omitempty"`
	// BackingAgentIDs are the agent instances this finding's aggregate was computed
	// from (priciest first, capped). The UI deep-links each into ai_get_conversation_agent
	// so a hypothesis can be traced to the raw per-call rows.
	BackingAgentIDs []string `json:"backing_agent_ids,omitempty"`
	// Exemplars are a few real calls (priciest) backing the finding, with their
	// actual tokens/cost/task — inline ground truth for quick verification.
	Exemplars           []OptExemplar `json:"exemplars,omitempty"`
	Recommendation      string        `json:"recommendation"`
	SuggestedModel      string        `json:"suggested_model,omitempty"`
	CurrentCostUsd      float64       `json:"current_cost_usd"`
	EstimatedSavingsUsd float64       `json:"estimated_savings_usd"`
	EstimatedSavingsPct float64       `json:"estimated_savings_pct"`
	Confidence          string        `json:"confidence"`
	OverlapsWith        []string      `json:"overlaps_with,omitempty"`
	// Category groups the finding by the trade-off it addresses (cost_only,
	// cost_and_accuracy, cost_and_latency, reliability) so the UI can filter.
	Category string `json:"category,omitempty"`
	// Impact records the directional effect on each axis: "improves"/"neutral"/
	// "degrades" for keys cost, latency, accuracy. Supplied by the analyzer.
	Impact map[string]string `json:"impact,omitempty"`
	// Advisory marks a qualitative finding that carries NO recomputed dollar
	// saving (context_bloat, failure_root_cause). Advisory findings are excluded
	// from the de-conflicted headline total and from per-agent overlap suppression
	// — they complement, not compete with, the dollar-bearing changes.
	Advisory bool `json:"advisory,omitempty"`
}

// ConversationOptimization is the ai_generate_conversation_optimization payload.
type ConversationOptimization struct {
	ConversationID           string              `json:"conversation_id"`
	CurrentCostUsd           float64             `json:"current_cost_usd"`
	TotalPotentialSavingsUsd float64             `json:"total_potential_savings_usd"`
	TotalPotentialSavingsPct float64             `json:"total_potential_savings_pct"`
	Summary                  string              `json:"summary"`
	Findings                 []OptFinding        `json:"findings"`
	Profile                  OptimizationProfile `json:"profile"`
}

// --- internal recompute index (NOT serialized) ---

type optTok struct {
	nonCached, cached, creation, output, thinking int
	calls                                         int
	cost                                          float64
}

// optSavingsIndex holds the raw aggregates needed to recompute every finding's
// dollar value server-side, so the LLM never supplies a number.
type optSavingsIndex struct {
	totalCost     float64
	retryByType   map[string]float64 // cost of calls with retry_attempt>0
	failureByType map[string]float64 // cost of failed calls
	directByType  map[string]float64 // direct model-call cost (agent_redundant saving basis)
	callsByType   map[string]int
	// per (agentName \x00 model): token sums + current cost (model_downgrade basis)
	byTypeModel map[string]optTok
	pricing     map[string]modelPricing // key provider:model
	byModelName map[string]modelPricing // key model name (for suggested_model lookup)

	// granular per-group data for verifiable evidence (token distribution, backing
	// instance ids, exemplar calls). Keyed the same way findings are grouped.
	granByType       map[string]*optGroupGran // key agent_name
	granByTypeModel  map[string]*optGroupGran // key tmKey(agent_name, model)
	taskByAgentID    map[string]string        // agent_id -> task (for exemplars)
	outcomeByAgentID map[string]string        // agent_id -> outcome (for exemplars)
}

// optCallSample is one model call retained for exemplar selection.
type optCallSample struct {
	agentID      string
	model        string
	inputTokens  int
	outputTokens int
	cost         float64
}

// optGroupGran accumulates the per-call detail a finding needs to be auditable:
// the input/output token distributions, per-instance cost (to rank backing ids),
// and the calls themselves (to pick the priciest as exemplars).
type optGroupGran struct {
	inTokens, outTokens []int
	agentCost           map[string]float64
	samples             []optCallSample
}

func (g *optGroupGran) add(s optCallSample) {
	g.inTokens = append(g.inTokens, s.inputTokens)
	g.outTokens = append(g.outTokens, s.outputTokens)
	if g.agentCost == nil {
		g.agentCost = map[string]float64{}
	}
	g.agentCost[s.agentID] += s.cost
	g.samples = append(g.samples, s)
}

func tmKey(agent, model string) string { return agent + "\x00" + model }

// GetConversationOptimizationProfile builds the bounded profile + the savings
// index for one conversation (session) within an account.
func (chat *ConversationDao) GetConversationOptimizationProfile(sessionID, accountID string) (OptimizationProfile, *optSavingsIndex, error) {
	if sessionID == "" || accountID == "" {
		return OptimizationProfile{}, nil, fmt.Errorf("GetConversationOptimizationProfile: session_id and account_id are required")
	}
	args := []any{sessionID, accountID}

	pricing, err := chat.GetConversationCosts(nil)
	if err != nil {
		return OptimizationProfile{}, nil, fmt.Errorf("GetConversationOptimizationProfile pricing: %w", err)
	}

	// --- model calls ---
	var mcScans []struct {
		ID                  string  `db:"id"`
		AgentID             string  `db:"agent_id"`
		Model               string  `db:"llm_model"`
		Provider            string  `db:"llm_provider"`
		InputTokens         int     `db:"input_tokens"`
		OutputTokens        int     `db:"output_tokens"`
		CachedInputTokens   int     `db:"cached_input_tokens"`
		CacheCreationTokens int     `db:"cache_creation_tokens"`
		ThinkingTokens      int     `db:"thinking_tokens"`
		LatencySeconds      float64 `db:"latency_seconds"`
		RetryAttempt        int     `db:"retry_attempt"`
		RequestStatus       string  `db:"request_status"`
		ErrorMessage        string  `db:"error_message"`
	}
	mcQuery := fmt.Sprintf(`
		SELECT id::text, COALESCE(agent_id::text, '') AS agent_id, llm_model, llm_provider,
			input_tokens, output_tokens, cached_input_tokens, cache_creation_tokens,
			COALESCE(thinking_tokens, 0) AS thinking_tokens,
			COALESCE(latency_seconds, 0) AS latency_seconds,
			retry_attempt, request_status,
			COALESCE(error_message, '') AS error_message
		FROM llm_conversation_token_usage
		WHERE %s`, convScopeCTE)
	if err := chat.dbManager.Db.Select(&mcScans, mcQuery, args...); err != nil {
		slog.Error("GetConversationOptimizationProfile: model_calls query failed", "error", err)
		return OptimizationProfile{}, nil, fmt.Errorf("GetConversationOptimizationProfile model_calls: %w", err)
	}

	// --- agents (name, parent, semantic content) ---
	var agentScans []struct {
		ID              string `db:"id"`
		AgentName       string `db:"agent_name"`
		ParentAgentID   string `db:"parent_agent_id"`
		Status          string `db:"status"`
		Query           string `db:"query"`
		ResponseSummary string `db:"response_summary"`
		Response        string `db:"response"`
	}
	agentQuery := fmt.Sprintf(`
		SELECT id::text, COALESCE(agent_name, '') AS agent_name,
			COALESCE(parent_agent_id::text, '') AS parent_agent_id,
			COALESCE(status, '') AS status, COALESCE(query, '') AS query,
			COALESCE(response_summary, '') AS response_summary,
			COALESCE(response, '') AS response
		FROM llm_conversation_agent
		WHERE %s`, convScopeCTE)
	if err := chat.dbManager.Db.Select(&agentScans, agentQuery, args...); err != nil {
		slog.Error("GetConversationOptimizationProfile: agents query failed", "error", err)
		return OptimizationProfile{}, nil, fmt.Errorf("GetConversationOptimizationProfile agents: %w", err)
	}

	// --- tool calls ---
	var toolScans []struct {
		AgentID         string  `db:"agent_id"`
		ToolName        string  `db:"tool_name"`
		Status          string  `db:"status"`
		Response        string  `db:"response"`
		ChildAgentID    string  `db:"child_agent_id"`
		DurationSeconds float64 `db:"duration_seconds"`
	}
	toolQuery := fmt.Sprintf(`
		SELECT COALESCE(agent_id::text, '') AS agent_id, COALESCE(tool_name, '') AS tool_name,
			COALESCE(status, '') AS status, COALESCE(response, '') AS response,
			COALESCE(child_agent_id::text, '') AS child_agent_id,
			COALESCE(EXTRACT(EPOCH FROM (updated_at - created_at)), 0) AS duration_seconds
		FROM llm_conversation_tool_calls
		WHERE %s`, convScopeCTE)
	if err := chat.dbManager.Db.Select(&toolScans, toolQuery, args...); err != nil {
		slog.Error("GetConversationOptimizationProfile: tool_calls query failed", "error", err)
		return OptimizationProfile{}, nil, fmt.Errorf("GetConversationOptimizationProfile tool_calls: %w", err)
	}

	// --- conversation header (id + title) ---
	var hdr struct {
		ID    string `db:"id"`
		Title string `db:"title"`
	}
	hdrQuery := `SELECT (array_agg(id::text ORDER BY created_at))[1] AS id,
		(array_agg(COALESCE(title, '') ORDER BY created_at DESC))[1] AS title
		FROM llm_conversations WHERE session_id = $1 AND account_id = $2::uuid GROUP BY session_id`
	_ = chat.dbManager.Db.Get(&hdr, hdrQuery, args...) // title is best-effort

	// === aggregate ===
	idx := &optSavingsIndex{
		retryByType:      map[string]float64{},
		failureByType:    map[string]float64{},
		directByType:     map[string]float64{},
		callsByType:      map[string]int{},
		byTypeModel:      map[string]optTok{},
		pricing:          pricing,
		byModelName:      map[string]modelPricing{},
		granByType:       map[string]*optGroupGran{},
		granByTypeModel:  map[string]*optGroupGran{},
		taskByAgentID:    map[string]string{},
		outcomeByAgentID: map[string]string{},
	}
	gran := func(m map[string]*optGroupGran, key string) *optGroupGran {
		g := m[key]
		if g == nil {
			g = &optGroupGran{}
			m[key] = g
		}
		return g
	}
	for k, p := range pricing {
		// key is provider:model — index by model name too for suggested_model lookups
		if i := strings.IndexByte(k, ':'); i >= 0 {
			idx.byModelName[k[i+1:]] = p
		}
	}

	var profileCacheSavings float64

	nameByAgentID := map[string]string{}
	for _, a := range agentScans {
		nameByAgentID[a.ID] = a.AgentName
		idx.taskByAgentID[a.ID] = truncate(a.Query, optTaskMaxChars)
		outcome := a.ResponseSummary
		if outcome == "" {
			outcome = truncate(a.Response, optOutcomeMaxChar)
		}
		idx.outcomeByAgentID[a.ID] = outcome
	}

	type typeAgg struct {
		instances                              map[string]bool
		modelCalls, toolCalls, errors, retries int
		cost                                   float64
		models                                 map[string]bool
		inTok, outTok                          int64
		latencySec, toolDurSec                 float64
	}
	byType := map[string]*typeAgg{}
	getType := func(name string) *typeAgg {
		t := byType[name]
		if t == nil {
			t = &typeAgg{instances: map[string]bool{}, models: map[string]bool{}}
			byType[name] = t
		}
		return t
	}

	type modelAgg struct {
		provider      string
		calls         int
		cost          float64
		inTok, outTok int64
		latencySec    float64
	}
	byModel := map[string]*modelAgg{}
	costByAgentID := map[string]float64{}
	latencyByAgentID := map[string]float64{} // model latency per agent instance
	toolDurByAgentID := map[string]float64{} // tool wall-time per agent instance

	for _, m := range mcScans {
		p := pricing[m.Provider+":"+m.Model]
		nonCached := m.InputTokens - m.CachedInputTokens
		if nonCached < 0 {
			nonCached = 0
		}
		cost := CalculateTotalCost(&p, nonCached, m.CachedInputTokens, m.CacheCreationTokens, m.OutputTokens, m.ThinkingTokens)
		idx.totalCost += cost
		costByAgentID[m.AgentID] += cost
		latencyByAgentID[m.AgentID] += m.LatencySeconds

		name := nameByAgentID[m.AgentID]
		if name == "" {
			name = optUnattributed
		}
		// granular per-call detail for verifiable evidence (distribution/exemplars/refs)
		sample := optCallSample{agentID: m.AgentID, model: m.Model, inputTokens: m.InputTokens, outputTokens: m.OutputTokens, cost: cost}
		gran(idx.granByType, name).add(sample)
		gran(idx.granByTypeModel, tmKey(name, m.Model)).add(sample)
		t := getType(name)
		if m.AgentID != "" {
			t.instances[m.AgentID] = true
		}
		t.modelCalls++
		t.cost += cost
		t.models[m.Model] = true
		t.inTok += int64(m.InputTokens)
		t.outTok += int64(m.OutputTokens)
		t.latencySec += m.LatencySeconds
		idx.directByType[name] += cost
		idx.callsByType[name]++
		if m.RetryAttempt > 0 {
			t.retries++
			idx.retryByType[name] += cost
		}
		if isModelError(m.RequestStatus, m.ErrorMessage) {
			t.errors++
			idx.failureByType[name] += cost
		}

		// per (type, model) token+cost for downgrade recompute
		k := tmKey(name, m.Model)
		tk := idx.byTypeModel[k]
		tk.nonCached += nonCached
		tk.cached += m.CachedInputTokens
		tk.creation += m.CacheCreationTokens
		tk.output += m.OutputTokens
		tk.thinking += m.ThinkingTokens
		tk.calls++
		tk.cost += cost
		idx.byTypeModel[k] = tk

		ma := byModel[m.Model]
		if ma == nil {
			ma = &modelAgg{provider: m.Provider}
			byModel[m.Model] = ma
		}
		ma.calls++
		ma.cost += cost
		ma.inTok += int64(m.InputTokens)
		ma.outTok += int64(m.OutputTokens)
		ma.latencySec += m.LatencySeconds

		// cache savings = value of cached input at the full input rate
		profileCacheSavings += float64(m.CachedInputTokens) / 1_000_000.0 * p.CostPerMillionInput
	}

	// tool calls per agent type
	toolCountByAgentID := map[string]int{}
	for _, tc := range toolScans {
		name := nameByAgentID[tc.AgentID]
		if name == "" {
			name = optUnattributed
		}
		t := getType(name)
		t.toolCalls++
		t.toolDurSec += tc.DurationSeconds
		if tc.AgentID != "" {
			toolCountByAgentID[tc.AgentID]++
			toolDurByAgentID[tc.AgentID] += tc.DurationSeconds
		}
	}

	// --- build profile.agents_by_type ---
	profile := OptimizationProfile{
		ConversationID: hdr.ID,
		SessionID:      sessionID,
		Title:          hdr.Title,
	}
	totalToolCalls := len(toolScans)
	for name, t := range byType {
		at := OptAgentType{
			Agent:      name,
			Instances:  len(t.instances),
			ModelCalls: t.modelCalls,
			ToolCalls:  t.toolCalls,
			CostUsd:    t.cost,
			Models:     sortedKeys(t.models),
			Errors:     t.errors,
			Retries:    t.retries,
		}
		if t.modelCalls > 0 {
			at.AvgInputTokens = t.inTok / int64(t.modelCalls)
			at.AvgOutputTokens = t.outTok / int64(t.modelCalls)
			at.AvgLatencyMs = int64(t.latencySec / float64(t.modelCalls) * 1000)
		}
		at.ModelLatencySec = t.latencySec
		at.ToolDurationSec = t.toolDurSec
		profile.AgentsByType = append(profile.AgentsByType, at)
	}
	sort.Slice(profile.AgentsByType, func(i, j int) bool {
		return profile.AgentsByType[i].CostUsd > profile.AgentsByType[j].CostUsd
	})

	// --- models ---
	for model, ma := range byModel {
		mu := OptModelUsage{Model: model, Provider: ma.provider, Calls: ma.calls, CostUsd: ma.cost}
		if ma.calls > 0 {
			mu.AvgInputTokens = ma.inTok / int64(ma.calls)
			mu.AvgOutputTokens = ma.outTok / int64(ma.calls)
			mu.AvgLatencyMs = int64(ma.latencySec / float64(ma.calls) * 1000)
		}
		profile.Models = append(profile.Models, mu)
	}
	sort.Slice(profile.Models, func(i, j int) bool { return profile.Models[i].CostUsd > profile.Models[j].CostUsd })

	// --- spawn graph (type-level edges) ---
	edge := map[string]int{}
	addEdge := func(parentID, childID string) {
		pn, cn := nameByAgentID[parentID], nameByAgentID[childID]
		if pn == "" || cn == "" || parentID == childID {
			return
		}
		edge[pn+"\x00"+cn]++
	}
	for _, a := range agentScans {
		addEdge(a.ParentAgentID, a.ID)
	}
	for _, tc := range toolScans {
		addEdge(tc.AgentID, tc.ChildAgentID)
	}
	for k, c := range edge {
		parts := strings.SplitN(k, "\x00", 2)
		profile.SpawnGraph = append(profile.SpawnGraph, OptSpawnEdge{From: parts[0], To: parts[1], Count: c})
	}
	sort.Slice(profile.SpawnGraph, func(i, j int) bool { return profile.SpawnGraph[i].Count > profile.SpawnGraph[j].Count })

	// --- top cost agents (semantic layer) ---
	toolsByAgentID := map[string][]string{}
	for _, tc := range toolScans {
		if tc.AgentID == "" {
			continue
		}
		flag := "ok"
		if isToolError(tc.Status) {
			flag = "error"
		} else if isToolNoData(tc.Response) {
			flag = "no_data"
		}
		toolsByAgentID[tc.AgentID] = append(toolsByAgentID[tc.AgentID], fmt.Sprintf("%s(%s)", tc.ToolName, flag))
	}
	agentByID := map[string]int{}
	for i, a := range agentScans {
		agentByID[a.ID] = i
	}
	type idCost struct {
		id   string
		cost float64
	}
	ranked := make([]idCost, 0, len(costByAgentID))
	for id, c := range costByAgentID {
		if id == "" {
			continue
		}
		ranked = append(ranked, idCost{id, c})
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].cost > ranked[j].cost })
	for i, rc := range ranked {
		if i >= optTopAgentCount {
			break
		}
		ai, ok := agentByID[rc.id]
		if !ok {
			continue
		}
		a := agentScans[ai]
		outcome := a.ResponseSummary
		if outcome == "" {
			outcome = truncate(a.Response, optOutcomeMaxChar)
		}
		tools := toolsByAgentID[rc.id]
		if len(tools) > 12 {
			tools = append(tools[:12], fmt.Sprintf("+%d more", len(tools)-12))
		}
		profile.TopCostAgents = append(profile.TopCostAgents, OptTopAgent{
			AgentID:    rc.id,
			Agent:      a.AgentName,
			CostUsd:    rc.cost,
			ToolCalls:  toolCountByAgentID[rc.id],
			Status:     a.Status,
			Task:       truncate(a.Query, optTaskMaxChars),
			Outcome:    outcome,
			Tools:      tools,
			LatencySec: latencyByAgentID[rc.id] + toolDurByAgentID[rc.id],
		})
	}

	// --- pricing (expose every priced model — incl. cheaper alternatives the
	// analyzer may suggest beyond the ones already used) ---
	for k, p := range pricing {
		i := strings.IndexByte(k, ':')
		if i < 0 {
			continue
		}
		model := k[i+1:]
		profile.Pricing = append(profile.Pricing, OptPricing{
			Model: model, Provider: k[:i],
			InputPerMtok: p.CostPerMillionInput, OutputPerMtok: p.CostPerMillionOutput,
		})
	}
	sort.Slice(profile.Pricing, func(i, j int) bool { return profile.Pricing[i].InputPerMtok < profile.Pricing[j].InputPerMtok })

	var totalModelLatency, totalToolDur float64
	for _, t := range byType {
		totalModelLatency += t.latencySec
		totalToolDur += t.toolDurSec
	}
	profile.Totals = OptTotals{
		CostUsd:         idx.totalCost,
		ModelCalls:      len(mcScans),
		ToolCalls:       totalToolCalls,
		Agents:          len(agentScans),
		RetryWasteUsd:   sumMap(idx.retryByType),
		FailureWasteUsd: sumMap(idx.failureByType),
		CacheSavingsUsd: profileCacheSavings,
		ModelLatencySec: totalModelLatency,
		ToolDurationSec: totalToolDur,
	}

	return profile, idx, nil
}

// isToolNoData reports whether a (non-error) tool response carried no usable
// payload — an empty string or a trivial "nothing here" body. It is a signal for
// redundant tool calls: a tool that ran, cost an agent turn, and returned nothing.
// Conservative on purpose (only obviously-empty bodies) so legitimate small
// results ("0", "false", a single id) are not mislabelled.
func isToolNoData(response string) bool {
	s := strings.ToLower(strings.TrimSpace(response))
	switch s {
	case "", "[]", "{}", "null", "none", "no data", "no results", "n/a", `""`, `{"data":[]}`, `{"results":[]}`:
		return true
	}
	return false
}

// truncate clips s to n bytes on a rune-safe boundary, appending an ellipsis.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "…"
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sumMap(m map[string]float64) float64 {
	var s float64
	for _, v := range m {
		s += v
	}
	return s
}

// --- analysis ---

// llmFindingProposal is what the analyzer returns; it selects targets and writes
// prose only — never numbers. We validate and price everything ourselves.
type llmFindingProposal struct {
	Type           string            `json:"type"`
	AgentName      string            `json:"agent_name"`
	AgentID        string            `json:"agent_id"`
	Model          string            `json:"model"`
	SuggestedModel string            `json:"suggested_model"`
	Title          string            `json:"title"`
	Evidence       string            `json:"evidence"`
	Recommendation string            `json:"recommendation"`
	Confidence     string            `json:"confidence"`
	Category       string            `json:"category"`
	Impact         map[string]string `json:"impact"`
}

type llmOptResponse struct {
	Summary  string               `json:"summary"`
	Findings []llmFindingProposal `json:"findings"`
}

// GenerateConversationOptimization builds the profile, asks the analyzer for
// downgrade/redundant opportunities, recomputes every dollar server-side, and
// returns the de-conflicted recommendations + the exported profile.
// trackConversationID / trackMessageID are where the analysis call's OWN token
// usage is recorded — the optimizer conversation (session=optimizer-<target>),
// NOT the conversation being analyzed. They come from the /chat flow that hosts
// the cost_optimizer agent. Empty (e.g. tests) → the call isn't tracked.
func GenerateConversationOptimization(ctx *security.RequestContext, sessionID, accountID, userID, trackConversationID, trackMessageID string) (ConversationOptimization, error) {
	if sessionID == "" || accountID == "" {
		return ConversationOptimization{}, fmt.Errorf("GenerateConversationOptimization: session_id and account_id are required")
	}

	profile, idx, err := GetConversationDao().GetConversationOptimizationProfile(sessionID, accountID)
	if err != nil {
		return ConversationOptimization{}, err
	}
	result := ConversationOptimization{
		ConversationID: profile.ConversationID,
		CurrentCostUsd: idx.totalCost,
		Profile:        profile,
		Findings:       []OptFinding{},
	}
	// Nothing ran (or nothing cost anything) → return an empty, valid result.
	if idx.totalCost <= 0 {
		result.Summary = "No billable model calls found for this conversation."
		return result, nil
	}

	profileJSON, err := json.Marshal(profile)
	if err != nil {
		return ConversationOptimization{}, fmt.Errorf("GenerateConversationOptimization marshal: %w", err)
	}

	messages := []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeSystem, prompts_repo.GetPrompt(prompts_repo.PromptCostOptimization)),
		llms.TextParts(llms.ChatMessageTypeHuman, string(profileJSON)),
	}
	// Track this analysis call's token usage against the OPTIMIZER conversation
	// (session=optimizer-<target>, source=Optimize) — never the analyzed
	// conversation. The analyser excludes source=Optimize, so this cost is recorded
	// and attributable to the optimizer without inflating the numbers it reports.
	// Empty track ids (e.g. tests / no host conversation) → the call isn't tracked.
	//
	// Temperature 0 + JSON mode: this is a repeatable audit — the same profile must
	// yield the same findings. Sampling at the provider default made identical inputs
	// surface different (overlapping) subsets run-to-run; pinning temperature removes
	// that variance, matching every other deterministic analyzer call in this package.
	// Cap output: the findings JSON is bounded by the number of agent types/models,
	// so 16k is ample. Without a cap the agent inherits the model's 64k default and
	// can spend minutes generating (a sync caller then times out). 16k keeps the
	// call to tens of seconds while leaving room for every finding + its evidence.
	completion, err := GenerateAndTrackLLMContent(ctx, userID, accountID, trackConversationID, trackMessageID, "cost_optimizer", false, messages, false,
		llms.WithTemperature(0.0), llms.WithJSONMode(), llms.WithMaxTokens(16384))
	var llmResp llmOptResponse
	if err != nil {
		// Degrade gracefully: still return the deterministic (waste) findings.
		ctx.GetLogger().Error("GenerateConversationOptimization: analyzer call failed", "error", err)
	} else if completion != nil && len(completion.Choices) > 0 {
		llmResp = parseOptResponse(completion.Choices[0].Content)
	}

	findings, summary, total := assembleFindings(profile, idx, llmResp)
	result.Findings = findings
	result.Summary = summary
	result.TotalPotentialSavingsUsd = total
	if idx.totalCost > 0 {
		result.TotalPotentialSavingsPct = total / idx.totalCost * 100
	}
	return result, nil
}

// parseOptResponse extracts the JSON object from the model's reply (tolerates
// ```json fences / surrounding prose) and unmarshals it.
func parseOptResponse(content string) llmOptResponse {
	var r llmOptResponse
	s := strings.TrimSpace(content)
	if i := strings.IndexByte(s, '{'); i >= 0 {
		if j := strings.LastIndexByte(s, '}'); j > i {
			s = s[i : j+1]
		}
	}
	if err := json.Unmarshal([]byte(s), &r); err != nil {
		slog.Warn("parseOptResponse: could not parse analyzer JSON", "error", err)
	}
	return r
}

// assembleFindings turns the deterministic waste + the validated LLM proposals
// into priced findings, then de-conflicts the total (one change per agent).
// Pure function over the index — unit-testable without an LLM or DB.
func assembleFindings(profile OptimizationProfile, idx *optSavingsIndex, llmResp llmOptResponse) ([]OptFinding, string, float64) {
	total := idx.totalCost
	pct := func(v float64) float64 {
		if total <= 0 {
			return 0
		}
		return v / total * 100
	}

	// Lookups for building server-derived supporting evidence. These pull the raw
	// numbers the analyzer's hypothesis rests on straight from the profile/index,
	// so every finding ships with verifiable proof (never the LLM's own numbers).
	atByName := make(map[string]OptAgentType, len(profile.AgentsByType))
	for _, at := range profile.AgentsByType {
		atByName[at.Agent] = at
	}
	topByID := make(map[string]OptTopAgent, len(profile.TopCostAgents))
	topByName := map[string]OptTopAgent{}
	for _, ta := range profile.TopCostAgents {
		topByID[ta.AgentID] = ta
		if _, seen := topByName[ta.Agent]; !seen {
			topByName[ta.Agent] = ta // first (highest-cost) instance of this type
		}
	}
	// topAgentFor returns the most relevant top-cost instance for a proposal:
	// the exact instance if agent_id is given and known, else the priciest of the type.
	topAgentFor := func(name, agentID string) (OptTopAgent, bool) {
		if agentID != "" {
			if ta, ok := topByID[agentID]; ok {
				return ta, true
			}
		}
		ta, ok := topByName[name]
		return ta, ok
	}

	var findings []OptFinding

	// 1) deterministic mechanical waste (no LLM) — high confidence.
	for name, c := range idx.failureByType {
		if c <= 0 {
			continue
		}
		ev := []OptEvidenceFact{evf("Failed-call cost", fmt.Sprintf("$%.4f", c), "index.failureByType")}
		if at, ok := atByName[name]; ok {
			ev = append(ev, evf("Errored model calls", fmt.Sprintf("%d", at.Errors), "agents_by_type.errors"))
		}
		findings = append(findings, OptFinding{
			Type:                "failure_waste",
			Title:               fmt.Sprintf("Failed model calls in %s", name),
			Target:              OptTarget{Kind: "agent", AgentName: name},
			Evidence:            "Cost spent on calls that returned an error.",
			SupportingEvidence:  ev,
			Recommendation:      "Investigate the failures; failed calls are billed but produce nothing.",
			CurrentCostUsd:      c,
			EstimatedSavingsUsd: c,
			EstimatedSavingsPct: pct(c),
			Confidence:          "high",
		})
	}
	for name, c := range idx.retryByType {
		if c <= 0 {
			continue
		}
		ev := []OptEvidenceFact{evf("Retry cost", fmt.Sprintf("$%.4f", c), "index.retryByType")}
		if at, ok := atByName[name]; ok {
			ev = append(ev, evf("Retried model calls", fmt.Sprintf("%d", at.Retries), "agents_by_type.retries"))
		}
		findings = append(findings, OptFinding{
			Type:                "retry_waste",
			Title:               fmt.Sprintf("Retried model calls in %s", name),
			Target:              OptTarget{Kind: "agent", AgentName: name},
			Evidence:            "Cost spent on retry attempts beyond the first.",
			SupportingEvidence:  ev,
			Recommendation:      "Reduce retries (tighten prompts/timeouts) — retries re-bill the call.",
			CurrentCostUsd:      c,
			EstimatedSavingsUsd: c,
			EstimatedSavingsPct: pct(c),
			Confidence:          "high",
		})
	}

	// 2) LLM-proposed judgment findings — validated + priced by us.
	for _, p := range llmResp.Findings {
		// The (unattributed) bucket has no drill-able instance, so a judgment finding
		// on it can't be verified or acted on — drop it (its cost stays in the rollup).
		if p.AgentName == optUnattributed {
			continue
		}
		switch p.Type {
		case "model_downgrade":
			tk, ok := idx.byTypeModel[tmKey(p.AgentName, p.Model)]
			if !ok || p.SuggestedModel == "" {
				continue
			}
			target, ok := idx.byModelName[p.SuggestedModel]
			if !ok { // suggested model must be priced
				continue
			}
			newCost := CalculateTotalCost(&target, tk.nonCached, tk.cached, tk.creation, tk.output, tk.thinking)
			saving := tk.cost - newCost
			if saving <= 0 {
				continue
			}
			ev := []OptEvidenceFact{
				evf("Calls on this model", fmt.Sprintf("%d", tk.calls), "byTypeModel.calls"),
				evf("Group cost", fmt.Sprintf("$%.4f", tk.cost), "byTypeModel.cost"),
				evf("Current model rate", fmtRate(idx.byModelName[p.Model]), "pricing."+p.Model),
				evf("Suggested model rate", fmtRate(target), "pricing."+p.SuggestedModel),
			}
			if at, ok := atByName[p.AgentName]; ok {
				ev = append(ev,
					evf("Avg output tokens", fmt.Sprintf("%d", at.AvgOutputTokens), "agents_by_type.avg_output_tokens"),
					evf("Avg input tokens", fmt.Sprintf("%d", at.AvgInputTokens), "agents_by_type.avg_input_tokens"),
					evf("Avg latency", fmt.Sprintf("%d ms", at.AvgLatencyMs), "agents_by_type.avg_latency_ms"))
			}
			findings = append(findings, OptFinding{
				Type:                "model_downgrade",
				Title:               orDefault(p.Title, fmt.Sprintf("Downgrade %s in %s", p.Model, p.AgentName)),
				Target:              OptTarget{Kind: "agent_model", AgentName: p.AgentName, Model: p.Model, CallCount: tk.calls},
				Evidence:            p.Evidence,
				SupportingEvidence:  ev,
				Recommendation:      orDefault(p.Recommendation, fmt.Sprintf("Use %s for these calls.", p.SuggestedModel)),
				SuggestedModel:      p.SuggestedModel,
				CurrentCostUsd:      tk.cost,
				EstimatedSavingsUsd: saving,
				EstimatedSavingsPct: pct(saving),
				Confidence:          orDefault(p.Confidence, "medium"),
				Category:            orDefault(p.Category, "cost_only"),
				Impact:              p.Impact,
			})
		case "agent_redundant":
			c, ok := idx.directByType[p.AgentName]
			if !ok || c <= 0 {
				continue
			}
			ev := []OptEvidenceFact{evf("Direct model-call cost", fmt.Sprintf("$%.4f", c), "index.directByType")}
			if at, ok := atByName[p.AgentName]; ok {
				ev = append(ev, evf("Instances", fmt.Sprintf("%d", at.Instances), "agents_by_type.instances"))
			}
			if ta, ok := topAgentFor(p.AgentName, p.AgentID); ok {
				ev = evidenceOutcome(ev, ta)
				if okc, nd, errc := parseToolFlags(ta.Tools); okc+nd+errc > 0 {
					ev = append(ev, evf("Tool results", fmt.Sprintf("ok=%d no_data=%d error=%d", okc, nd, errc), "top_cost_agents.tools"))
				}
			}
			findings = append(findings, OptFinding{
				Type:                "agent_redundant",
				Title:               orDefault(p.Title, fmt.Sprintf("Agent %s may be redundant", p.AgentName)),
				Target:              OptTarget{Kind: "agent", AgentName: p.AgentName, AgentID: p.AgentID},
				Evidence:            p.Evidence,
				SupportingEvidence:  ev,
				Recommendation:      orDefault(p.Recommendation, fmt.Sprintf("Consider excluding %s from this flow.", p.AgentName)),
				CurrentCostUsd:      c,
				EstimatedSavingsUsd: c,
				EstimatedSavingsPct: pct(c),
				Confidence:          orDefault(p.Confidence, "low"),
				Category:            orDefault(p.Category, "cost_only"),
				Impact:              p.Impact,
			})
		case "context_bloat":
			// Advisory: an agent/model group spends heavily on input tokens (large
			// prompts) relative to the work produced. We surface the addressable
			// input-heavy spend but assign NO dollar saving — we cannot know how much
			// context is safely trimmable, and the engine never fabricates a number.
			c, ok := idx.directByType[p.AgentName]
			if !ok || c <= 0 {
				continue
			}
			var ev []OptEvidenceFact
			if at, ok := atByName[p.AgentName]; ok {
				ev = append(ev,
					evf("Avg input tokens", fmt.Sprintf("%d", at.AvgInputTokens), "agents_by_type.avg_input_tokens"),
					evf("Avg output tokens", fmt.Sprintf("%d", at.AvgOutputTokens), "agents_by_type.avg_output_tokens"),
					evf("Model calls", fmt.Sprintf("%d", at.ModelCalls), "agents_by_type.model_calls"),
					evf("Avg latency", fmt.Sprintf("%d ms", at.AvgLatencyMs), "agents_by_type.avg_latency_ms"))
				if at.AvgOutputTokens > 0 {
					ev = append(ev, evf("Input:output ratio", fmt.Sprintf("%.0f:1", float64(at.AvgInputTokens)/float64(at.AvgOutputTokens)), "agents_by_type"))
				}
			}
			findings = append(findings, OptFinding{
				Type:                "context_bloat",
				Title:               orDefault(p.Title, fmt.Sprintf("Trim input context for %s", p.AgentName)),
				Target:              OptTarget{Kind: "agent", AgentName: p.AgentName, Model: p.Model, AgentID: p.AgentID},
				Evidence:            p.Evidence,
				SupportingEvidence:  ev,
				Recommendation:      orDefault(p.Recommendation, fmt.Sprintf("Reduce the prompt/context sent on %s calls (prune history, summarise observations, scope tool output).", p.AgentName)),
				CurrentCostUsd:      c,
				EstimatedSavingsUsd: 0,
				EstimatedSavingsPct: 0,
				Confidence:          orDefault(p.Confidence, "low"),
				Category:            orDefault(p.Category, "cost_and_latency"),
				Impact:              p.Impact,
				Advisory:            true,
			})
		case "failure_root_cause":
			// Advisory: explains WHY an agent's calls failed. The wasted dollars are
			// already counted once in the deterministic failure_waste finding, so this
			// adds no incremental saving — it adds the diagnosis + a prevention step.
			c, ok := idx.failureByType[p.AgentName]
			if !ok || c <= 0 {
				continue
			}
			ev := []OptEvidenceFact{evf("Failed-call cost", fmt.Sprintf("$%.4f", c), "index.failureByType")}
			if at, ok := atByName[p.AgentName]; ok {
				ev = append(ev,
					evf("Errored model calls", fmt.Sprintf("%d", at.Errors), "agents_by_type.errors"),
					evf("Retries", fmt.Sprintf("%d", at.Retries), "agents_by_type.retries"))
			}
			if ta, ok := topAgentFor(p.AgentName, p.AgentID); ok {
				if ta.Status != "" {
					ev = append(ev, evf("Status", ta.Status, "top_cost_agents.status"))
				}
				if _, _, errc := parseToolFlags(ta.Tools); errc > 0 {
					ev = append(ev, evf("Errored tool calls", fmt.Sprintf("%d", errc), "top_cost_agents.tools"))
				}
			}
			findings = append(findings, OptFinding{
				Type:                "failure_root_cause",
				Title:               orDefault(p.Title, fmt.Sprintf("Why %s calls failed", p.AgentName)),
				Target:              OptTarget{Kind: "agent", AgentName: p.AgentName, AgentID: p.AgentID},
				Evidence:            p.Evidence,
				SupportingEvidence:  ev,
				Recommendation:      orDefault(p.Recommendation, "Fix the underlying cause so these calls stop being billed for nothing."),
				CurrentCostUsd:      c,
				EstimatedSavingsUsd: 0,
				EstimatedSavingsPct: 0,
				Confidence:          orDefault(p.Confidence, "medium"),
				Category:            orDefault(p.Category, "reliability"),
				Impact:              p.Impact,
				Advisory:            true,
			})
		case "excessive_iteration":
			// Advisory: an agent ran far more model calls (ReAct loops) or spawned far
			// more children than its outcome warranted. We surface the addressable spend
			// but assign NO dollar saving — the right number of iterations is unknown, so
			// the engine never fabricates a reduction fraction.
			c, ok := idx.directByType[p.AgentName]
			if !ok || c <= 0 {
				continue
			}
			ev := []OptEvidenceFact{evf("Direct model-call cost", fmt.Sprintf("$%.4f", c), "index.directByType")}
			if at, ok := atByName[p.AgentName]; ok {
				ev = append(ev,
					evf("Model calls", fmt.Sprintf("%d", at.ModelCalls), "agents_by_type.model_calls"),
					evf("Instances", fmt.Sprintf("%d", at.Instances), "agents_by_type.instances"))
				if at.Instances > 0 {
					ev = append(ev, evf("Model calls / instance", fmt.Sprintf("%d", at.ModelCalls/at.Instances), "agents_by_type"))
				}
			}
			if ta, ok := topAgentFor(p.AgentName, p.AgentID); ok {
				ev = evidenceOutcome(ev, ta)
			}
			findings = append(findings, OptFinding{
				Type:                "excessive_iteration",
				Title:               orDefault(p.Title, fmt.Sprintf("Reduce iterations/fan-out in %s", p.AgentName)),
				Target:              OptTarget{Kind: "agent", AgentName: p.AgentName, AgentID: p.AgentID},
				Evidence:            p.Evidence,
				SupportingEvidence:  ev,
				Recommendation:      orDefault(p.Recommendation, fmt.Sprintf("Cap iterations or consolidate the sub-agents %s spawns — extra loops add cost without new signal.", p.AgentName)),
				CurrentCostUsd:      c,
				EstimatedSavingsUsd: 0,
				EstimatedSavingsPct: 0,
				Confidence:          orDefault(p.Confidence, "low"),
				Category:            orDefault(p.Category, "cost_and_latency"),
				Impact:              p.Impact,
				Advisory:            true,
			})
		case "cache_underutilization":
			// Advisory: a high-input group runs many calls but the conversation realised
			// little cache benefit, i.e. the repeated prompt prefix is not being cached.
			// No dollar figure — the cacheable-prefix fraction is unknown.
			c, ok := idx.directByType[p.AgentName]
			if !ok || c <= 0 {
				continue
			}
			ev := []OptEvidenceFact{evf("Direct model-call cost", fmt.Sprintf("$%.4f", c), "index.directByType")}
			if at, ok := atByName[p.AgentName]; ok {
				ev = append(ev,
					evf("Avg input tokens", fmt.Sprintf("%d", at.AvgInputTokens), "agents_by_type.avg_input_tokens"),
					evf("Model calls", fmt.Sprintf("%d", at.ModelCalls), "agents_by_type.model_calls"))
			}
			ev = append(ev, evf("Cache savings (conversation)", fmt.Sprintf("$%.4f", profile.Totals.CacheSavingsUsd), "totals.cache_savings_usd"))
			findings = append(findings, OptFinding{
				Type:                "cache_underutilization",
				Title:               orDefault(p.Title, fmt.Sprintf("Cache the prompt prefix for %s", p.AgentName)),
				Target:              OptTarget{Kind: "agent", AgentName: p.AgentName, Model: p.Model, AgentID: p.AgentID},
				Evidence:            p.Evidence,
				SupportingEvidence:  ev,
				Recommendation:      orDefault(p.Recommendation, fmt.Sprintf("Enable/extend prompt caching of the static prefix on %s calls so repeated input is not re-billed at the full rate.", p.AgentName)),
				CurrentCostUsd:      c,
				EstimatedSavingsUsd: 0,
				EstimatedSavingsPct: 0,
				Confidence:          orDefault(p.Confidence, "low"),
				Category:            orDefault(p.Category, "cost_only"),
				Impact:              p.Impact,
				Advisory:            true,
			})
		}
	}

	// 2b) attach granular, auditable evidence to every finding: the token
	// distribution (so a reader can see whether the average is representative),
	// the backing instance ids (deep-link into ai_get_conversation_agent), and a
	// few exemplar calls with their real numbers.
	for i := range findings {
		attachGranularEvidence(&findings[i], idx)
	}

	// assign ids + sort by saving
	sort.SliceStable(findings, func(i, j int) bool {
		return findings[i].EstimatedSavingsUsd > findings[j].EstimatedSavingsUsd
	})
	for i := range findings {
		findings[i].ID = fmt.Sprintf("f%d", i+1)
	}

	// 3) de-conflict the headline total: at most one change per agent (keep the
	// largest), since you can't both exclude an agent and downgrade its model.
	bestPerAgent := map[string]string{} // agent_name -> winning finding id
	for _, f := range findings {
		if f.Advisory { // advisory findings don't compete for the per-agent slot
			continue
		}
		name := f.Target.AgentName
		if name == "" {
			continue
		}
		if _, seen := bestPerAgent[name]; !seen {
			bestPerAgent[name] = f.ID // findings are sorted desc, so first seen is largest
		}
	}
	deconflicted := 0.0
	for i := range findings {
		if findings[i].Advisory { // contributes no dollars, never marked as overlap
			continue
		}
		name := findings[i].Target.AgentName
		if name != "" && bestPerAgent[name] != findings[i].ID {
			findings[i].OverlapsWith = []string{bestPerAgent[name]}
			continue
		}
		deconflicted += findings[i].EstimatedSavingsUsd
	}
	if deconflicted > total {
		deconflicted = total
	}

	summary := llmResp.Summary
	if summary == "" {
		summary = fmt.Sprintf("Found %d optimization opportunit%s worth up to $%.4f of the $%.4f spend.",
			len(findings), plural(len(findings)), deconflicted, total)
	}
	return findings, summary, deconflicted
}

func orDefault(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// evf is a terse constructor for a supporting-evidence fact.
func evf(label, value, source string) OptEvidenceFact {
	return OptEvidenceFact{Label: label, Value: value, Source: source}
}

// parseToolFlags counts the "name(flag)" suffixes a top-cost agent's tools carry.
func parseToolFlags(tools []string) (ok, noData, errc int) {
	for _, t := range tools {
		switch {
		case strings.HasSuffix(t, "(error)"):
			errc++
		case strings.HasSuffix(t, "(no_data)"):
			noData++
		case strings.HasSuffix(t, "(ok)"):
			ok++
		}
	}
	return ok, noData, errc
}

// fmtRate renders a model's price as "in / out per Mtok".
func fmtRate(p modelPricing) string {
	return fmt.Sprintf("$%.2f in / $%.2f out per Mtok", p.CostPerMillionInput, p.CostPerMillionOutput)
}

// evidenceOutcome appends a trimmed outcome snippet when one exists (strong
// evidence for redundancy / failure findings).
func evidenceOutcome(facts []OptEvidenceFact, ta OptTopAgent) []OptEvidenceFact {
	o := strings.TrimSpace(ta.Outcome)
	if o == "" {
		return append(facts, evf("Outcome", "(empty)", "top_cost_agents.outcome"))
	}
	return append(facts, evf("Outcome", truncate(o, 160), "top_cost_agents.outcome"))
}

// tokenStats returns min/median/max of a token-count slice. The median exposes
// whether an average is representative or hides an outlier-skewed distribution.
func tokenStats(vals []int) (min, median, max int) {
	if len(vals) == 0 {
		return 0, 0, 0
	}
	s := append([]int(nil), vals...)
	sort.Ints(s)
	return s[0], s[len(s)/2], s[len(s)-1]
}

// topBackingAgentIDs returns the priciest real (non-empty) instance ids in a group.
func topBackingAgentIDs(g *optGroupGran, n int) []string {
	if g == nil {
		return nil
	}
	type ic struct {
		id   string
		cost float64
	}
	ranked := make([]ic, 0, len(g.agentCost))
	for id, c := range g.agentCost {
		if id == "" {
			continue
		}
		ranked = append(ranked, ic{id, c})
	}
	sort.Slice(ranked, func(i, j int) bool { return ranked[i].cost > ranked[j].cost })
	out := make([]string, 0, n)
	for i := 0; i < len(ranked) && i < n; i++ {
		out = append(out, ranked[i].id)
	}
	return out
}

// attachGranularEvidence adds the token distribution, backing instance ids, and
// exemplar calls to a finding, sourced from the per-group granular index. It uses
// the (agent,model) group when the finding names a model, else the agent group.
func attachGranularEvidence(f *OptFinding, idx *optSavingsIndex) {
	name := f.Target.AgentName
	if name == "" {
		return
	}
	g := idx.granByType[name]
	if f.Target.Model != "" {
		if gm := idx.granByTypeModel[tmKey(name, f.Target.Model)]; gm != nil {
			g = gm
		}
	}
	if g == nil || len(g.samples) == 0 {
		return
	}

	// distribution — only meaningful with more than one call
	if len(g.samples) > 1 {
		inMin, inMed, inMax := tokenStats(g.inTokens)
		outMin, outMed, outMax := tokenStats(g.outTokens)
		f.SupportingEvidence = append(f.SupportingEvidence,
			evf("Input tokens (min/median/max)", fmt.Sprintf("%d / %d / %d over %d calls", inMin, inMed, inMax, len(g.inTokens)), "model_calls.input_tokens"),
			evf("Output tokens (min/median/max)", fmt.Sprintf("%d / %d / %d", outMin, outMed, outMax), "model_calls.output_tokens"))
	}

	// backing instances — deep-link targets into ai_get_conversation_agent
	f.BackingAgentIDs = topBackingAgentIDs(g, optBackingIDCount)

	// exemplars — the priciest real calls, with their actual numbers + task/outcome
	samples := append([]optCallSample(nil), g.samples...)
	sort.Slice(samples, func(i, j int) bool { return samples[i].cost > samples[j].cost })
	for i := 0; i < len(samples) && len(f.Exemplars) < optExemplarCount; i++ {
		s := samples[i]
		f.Exemplars = append(f.Exemplars, OptExemplar{
			AgentID:      s.agentID,
			Model:        s.model,
			InputTokens:  s.inputTokens,
			OutputTokens: s.outputTokens,
			CostUsd:      s.cost,
			Task:         idx.taskByAgentID[s.agentID],
			Outcome:      idx.outcomeByAgentID[s.agentID],
		})
	}
}

func plural(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// The optimizer's only front door is the cost_optimizer agent via /chat (session
// = optimizer-<target>, source=Optimize) — that path creates the conversation +
// message the token-usage rows require (both NOT NULL) so the analysis cost is
// tracked. A standalone read-only endpoint can't satisfy that, so it was removed;
// GenerateConversationOptimization above is the shared engine the agent calls.
