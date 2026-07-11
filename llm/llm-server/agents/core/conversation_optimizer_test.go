package core

import (
	"testing"

	"nudgebee/llm/common"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAssembleFindings_DeterministicAndDeconflicted is the core test: deterministic
// waste findings are emitted, LLM proposals are priced server-side, and the total
// de-conflicts to one change per agent (largest wins, others overlap).
func TestAssembleFindings_DeterministicAndDeconflicted(t *testing.T) {
	idx := &optSavingsIndex{
		totalCost:     100,
		failureByType: map[string]float64{"a": 2},
		retryByType:   map[string]float64{"a": 1},
		directByType:  map[string]float64{"a": 50, "b": 20},
		callsByType:   map[string]int{"a": 10, "b": 5},
		byTypeModel: map[string]optTok{
			tmKey("a", "pro"): {nonCached: 1_000_000, output: 0, calls: 10, cost: 50},
		},
		byModelName: map[string]modelPricing{
			"flash": {CostPerMillionInput: 1.0, CostPerMillionOutput: 1.0},
		},
	}
	llmResp := llmOptResponse{
		Summary: "test",
		Findings: []llmFindingProposal{
			{Type: "model_downgrade", AgentName: "a", Model: "pro", SuggestedModel: "flash", Confidence: "medium"},
			{Type: "agent_redundant", AgentName: "b", Confidence: "low"},
			// invalid: suggested model not priced → must be dropped
			{Type: "model_downgrade", AgentName: "a", Model: "pro", SuggestedModel: "nonexistent"},
		},
	}

	findings, summary, total := assembleFindings(OptimizationProfile{}, idx, llmResp)
	assert.Equal(t, "test", summary)

	byType := map[string]OptFinding{}
	for _, f := range findings {
		byType[f.Type] = f
	}
	// downgrade: 1M input at flash $1/Mtok = $1; saving = 50 - 1 = 49
	require.Contains(t, byType, "model_downgrade")
	assert.InDelta(t, 49.0, byType["model_downgrade"].EstimatedSavingsUsd, 1e-9)
	assert.Equal(t, 10, byType["model_downgrade"].Target.CallCount)
	// redundant b = its direct cost 20
	assert.InDelta(t, 20.0, byType["agent_redundant"].EstimatedSavingsUsd, 1e-9)
	// deterministic waste present
	assert.InDelta(t, 2.0, byType["failure_waste"].EstimatedSavingsUsd, 1e-9)
	assert.InDelta(t, 1.0, byType["retry_waste"].EstimatedSavingsUsd, 1e-9)
	// invalid downgrade dropped → exactly 4 findings
	assert.Len(t, findings, 4)

	// every finding carries server-derived supporting evidence (not LLM numbers)
	for _, f := range findings {
		assert.NotEmpty(t, f.SupportingEvidence, "finding %s should carry supporting evidence", f.Type)
	}
	// downgrade evidence cites the call count + the price comparison
	dg := byType["model_downgrade"]
	labels := map[string]string{}
	for _, e := range dg.SupportingEvidence {
		labels[e.Label] = e.Value
	}
	assert.Equal(t, "10", labels["Calls on this model"])
	assert.Contains(t, labels, "Current model rate")
	assert.Contains(t, labels, "Suggested model rate")

	// de-conflict: agent "a" has failure(2)+retry(1)+downgrade(49) → keep 49 only;
	// agent "b" → 20. total = 49 + 20 = 69.
	assert.InDelta(t, 69.0, total, 1e-9)
	// the failure/retry findings overlap with the downgrade finding (a's winner)
	assert.NotEmpty(t, byType["failure_waste"].OverlapsWith)
	assert.NotEmpty(t, byType["retry_waste"].OverlapsWith)
	assert.Empty(t, byType["model_downgrade"].OverlapsWith)
}

// TestAssembleFindings_TotalCappedAtCost ensures the headline never exceeds spend.
func TestAssembleFindings_TotalCappedAtCost(t *testing.T) {
	idx := &optSavingsIndex{
		totalCost:    10,
		directByType: map[string]float64{"a": 8, "b": 8},
	}
	llmResp := llmOptResponse{Findings: []llmFindingProposal{
		{Type: "agent_redundant", AgentName: "a"},
		{Type: "agent_redundant", AgentName: "b"},
	}}
	_, _, total := assembleFindings(OptimizationProfile{}, idx, llmResp)
	assert.InDelta(t, 10.0, total, 1e-9) // 8+8=16 capped at 10
}

// TestAssembleFindings_AdvisoryDoNotInflateTotal verifies that the advisory finding
// types (context_bloat, failure_root_cause) carry no dollar saving, are excluded from
// the headline total, and never suppress a competing dollar-bearing change on the same
// agent (no overlap marking, and they don't take the per-agent slot).
func TestAssembleFindings_AdvisoryDoNotInflateTotal(t *testing.T) {
	idx := &optSavingsIndex{
		totalCost:     100,
		failureByType: map[string]float64{"a": 5},
		directByType:  map[string]float64{"a": 40},
		callsByType:   map[string]int{"a": 8},
	}
	llmResp := llmOptResponse{
		Findings: []llmFindingProposal{
			{Type: "agent_redundant", AgentName: "a", Confidence: "low"},
			{Type: "context_bloat", AgentName: "a", Model: "pro", Confidence: "low"},
			{Type: "failure_root_cause", AgentName: "a", Confidence: "medium"},
			{Type: "excessive_iteration", AgentName: "a", Confidence: "low"},
			{Type: "cache_underutilization", AgentName: "a", Model: "pro", Confidence: "low"},
			// invalid: failure_root_cause for an agent with no failure spend → dropped
			{Type: "failure_root_cause", AgentName: "ghost"},
			// invalid: context_bloat for an agent that made no model calls → dropped
			{Type: "context_bloat", AgentName: "ghost"},
			// invalid: excessive_iteration for an agent with no model-call cost → dropped
			{Type: "excessive_iteration", AgentName: "ghost"},
		},
	}

	findings, _, total := assembleFindings(OptimizationProfile{}, idx, llmResp)

	byType := map[string]OptFinding{}
	for _, f := range findings {
		byType[f.Type] = f
	}
	// every advisory type assembled, all flagged advisory with zero saving + no overlap
	for _, ty := range []string{"context_bloat", "failure_root_cause", "excessive_iteration", "cache_underutilization"} {
		require.Contains(t, byType, ty)
		assert.True(t, byType[ty].Advisory, "%s must be advisory", ty)
		assert.Zero(t, byType[ty].EstimatedSavingsUsd, "%s must carry no saving", ty)
		assert.Empty(t, byType[ty].OverlapsWith, "%s must not overlap-suppress", ty)
	}
	// ghost proposals dropped: agent_redundant + failure_waste + 4 advisory = 6
	assert.Len(t, findings, 6)
	// total = only the dollar-bearing redundant(40) + deterministic failure_waste(5),
	// de-conflicted to one change on "a" → keep the larger (40). Advisory adds nothing.
	assert.InDelta(t, 40.0, total, 1e-9)
}

// TestAssembleFindings_GranularEvidence verifies the auditable layer: token
// distribution (min/median/max), backing instance ids (priciest first, no empty),
// and exemplar calls with real numbers + task. Also asserts the (unattributed)
// bucket is never turned into an LLM finding.
func TestAssembleFindings_GranularEvidence(t *testing.T) {
	idx := &optSavingsIndex{
		totalCost:    100,
		directByType: map[string]float64{"a": 90, optUnattributed: 10},
		callsByType:  map[string]int{"a": 3},
		granByType: map[string]*optGroupGran{
			"a": {
				inTokens:  []int{100, 200, 9000},
				outTokens: []int{10, 20, 30},
				agentCost: map[string]float64{"id-cheap": 5, "id-rich": 70, "": 15},
				samples: []optCallSample{
					{agentID: "id-cheap", model: "pro", inputTokens: 100, outputTokens: 10, cost: 5},
					{agentID: "id-rich", model: "pro", inputTokens: 9000, outputTokens: 30, cost: 70},
				},
			},
		},
		taskByAgentID:    map[string]string{"id-rich": "summarise the big log"},
		outcomeByAgentID: map[string]string{"id-rich": "done"},
	}
	llmResp := llmOptResponse{Findings: []llmFindingProposal{
		{Type: "agent_redundant", AgentName: "a", Confidence: "low"},
		// must be suppressed: judgment finding on the unattributed bucket
		{Type: "context_bloat", AgentName: optUnattributed},
	}}

	findings, _, _ := assembleFindings(OptimizationProfile{}, idx, llmResp)

	var ar *OptFinding
	for i := range findings {
		assert.NotEqual(t, optUnattributed, findings[i].Target.AgentName, "(unattributed) must not be a finding target")
		if findings[i].Type == "agent_redundant" {
			ar = &findings[i]
		}
	}
	require.NotNil(t, ar)
	// backing ids: priciest first, empty id excluded
	assert.Equal(t, []string{"id-rich", "id-cheap"}, ar.BackingAgentIDs)
	// exemplars: priciest first, real numbers + task carried through
	require.NotEmpty(t, ar.Exemplars)
	assert.Equal(t, "id-rich", ar.Exemplars[0].AgentID)
	assert.Equal(t, 9000, ar.Exemplars[0].InputTokens)
	assert.Equal(t, "summarise the big log", ar.Exemplars[0].Task)
	// distribution fact present and exposes the 9000 outlier vs the small calls
	var dist string
	for _, e := range ar.SupportingEvidence {
		if e.Label == "Input tokens (min/median/max)" {
			dist = e.Value
		}
	}
	assert.Contains(t, dist, "100 / 200 / 9000")
}

// TestIsToolNoData covers the empty/trivial-body classification.
func TestIsToolNoData(t *testing.T) {
	for _, s := range []string{"", "  ", "[]", "{}", "null", "No Data", `{"data":[]}`} {
		assert.True(t, isToolNoData(s), "expected no_data for %q", s)
	}
	for _, s := range []string{"0", "false", "pods listed", `{"items":[1]}`, "error: boom"} {
		assert.False(t, isToolNoData(s), "expected usable for %q", s)
	}
}

// TestParseOptResponse_ToleratesFences extracts JSON from a fenced/prose reply.
func TestParseOptResponse_ToleratesFences(t *testing.T) {
	raw := "Here is the analysis:\n```json\n{\"summary\":\"s\",\"findings\":[{\"type\":\"agent_redundant\",\"agent_name\":\"x\"}]}\n```\nThanks."
	r := parseOptResponse(raw)
	assert.Equal(t, "s", r.Summary)
	require.Len(t, r.Findings, 1)
	assert.Equal(t, "x", r.Findings[0].AgentName)
}

// TestGetConversationOptimizationProfile_Aggregates validates the profile builder
// on a small live-shaped dataset via sqlmock.
func TestGetConversationOptimizationProfile_Aggregates(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	dao := &ConversationDao{dbManager: &common.DatabaseManager{Db: sqlx.NewDb(db, "postgres")}}

	// 1) pricing
	mock.ExpectQuery("FROM llm_model_pricing").WillReturnRows(sqlmock.NewRows([]string{
		"provider_name", "model_name",
		"cost_per_million_input_tokens", "cost_per_million_output_tokens",
		"cache_cost_per_million_tokens_per_hour", "cost_per_million_cached_input_tokens",
		"cost_per_million_cache_creation_tokens", "cost_per_million_cached_storage_per_hour",
		"context_threshold_tokens", "cost_per_million_input_tokens_long_ctx",
		"cost_per_million_output_tokens_long_ctx", "cost_per_million_cached_input_tokens_long_ctx",
		"cost_per_million_cache_creation_tokens_long_ctx",
	}).AddRow("googleai", "pro", 10.0, 30.0, nil, nil, nil, nil, nil, nil, nil, nil, nil).
		AddRow("googleai", "flash", 1.0, 2.0, nil, nil, nil, nil, nil, nil, nil, nil, nil))

	// 2) model calls: agent A1 (k8s_debug) 1 success pro + 1 retry; agent A2 (events) 1 failure
	mock.ExpectQuery("FROM llm_conversation_token_usage").WillReturnRows(sqlmock.NewRows([]string{
		"id", "agent_id", "llm_model", "llm_provider", "input_tokens", "output_tokens",
		"cached_input_tokens", "cache_creation_tokens", "thinking_tokens", "latency_seconds",
		"retry_attempt", "request_status", "error_message",
	}).
		AddRow("m1", "A1", "pro", "googleai", 1000000, 0, 0, 0, 0, 2.0, 0, "success", "").
		AddRow("m2", "A1", "pro", "googleai", 1000000, 0, 0, 0, 0, 4.0, 1, "success", "").
		AddRow("m3", "A2", "flash", "googleai", 1000000, 0, 0, 0, 0, 1.0, 0, "error", "boom"))

	// 3) agents
	mock.ExpectQuery("FROM llm_conversation_agent").WillReturnRows(sqlmock.NewRows([]string{
		"id", "agent_name", "parent_agent_id", "status", "query", "response_summary", "response",
	}).
		AddRow("A1", "k8s_debug", "", "success", "investigate", "found it", "long response").
		AddRow("A2", "events", "A1", "success", "get events", "", ""))

	// 4) tool calls
	mock.ExpectQuery("FROM llm_conversation_tool_calls").WillReturnRows(sqlmock.NewRows([]string{
		"agent_id", "tool_name", "status", "response", "child_agent_id", "duration_seconds",
	}).AddRow("A1", "kubectl", "SUCCESS", "pods listed", "A2", 3.5))

	// 5) header
	mock.ExpectQuery("FROM llm_conversations").WillReturnRows(
		sqlmock.NewRows([]string{"id", "title"}).AddRow("conv-1", "My Investigation"))

	profile, idx, err := dao.GetConversationOptimizationProfile("sess-1", "acc-1")
	require.NoError(t, err)

	// pro: 2 calls × 1M input × $10/Mtok = $20 ; flash: 1 call × $1 = $1 → total $21
	assert.InDelta(t, 21.0, idx.totalCost, 1e-9)
	assert.InDelta(t, 21.0, profile.Totals.CostUsd, 1e-9)
	assert.Equal(t, 3, profile.Totals.ModelCalls)
	assert.Equal(t, 1, profile.Totals.ToolCalls)
	assert.Equal(t, 2, profile.Totals.Agents)
	// retry waste = the 1 retried pro call = $10 ; failure waste = the flash error = $1
	assert.InDelta(t, 10.0, profile.Totals.RetryWasteUsd, 1e-9)
	assert.InDelta(t, 1.0, profile.Totals.FailureWasteUsd, 1e-9)
	// agents_by_type: k8s_debug $20 (top), events $1
	require.GreaterOrEqual(t, len(profile.AgentsByType), 2)
	assert.Equal(t, "k8s_debug", profile.AgentsByType[0].Agent)
	assert.InDelta(t, 20.0, profile.AgentsByType[0].CostUsd, 1e-9)
	// spawn graph: k8s_debug → events
	require.NotEmpty(t, profile.SpawnGraph)
	assert.Equal(t, "k8s_debug", profile.SpawnGraph[0].From)
	assert.Equal(t, "events", profile.SpawnGraph[0].To)
	// downgrade basis present for (k8s_debug, pro)
	tk := idx.byTypeModel[tmKey("k8s_debug", "pro")]
	assert.Equal(t, 2, tk.calls)
	// latency: model latency totals 2+4+1=7s, tool duration 3.5s
	assert.InDelta(t, 7.0, profile.Totals.ModelLatencySec, 1e-9)
	assert.InDelta(t, 3.5, profile.Totals.ToolDurationSec, 1e-9)
	// k8s_debug (A1): 2 pro calls latency 2+4=6s → avg 3000ms; carries the 3.5s tool
	assert.Equal(t, "k8s_debug", profile.AgentsByType[0].Agent)
	assert.InDelta(t, 6.0, profile.AgentsByType[0].ModelLatencySec, 1e-9)
	assert.Equal(t, int64(3000), profile.AgentsByType[0].AvgLatencyMs)
	assert.InDelta(t, 3.5, profile.AgentsByType[0].ToolDurationSec, 1e-9)
	// top cost agent A1: model latency 6s + tool 3.5s = 9.5s
	require.NotEmpty(t, profile.TopCostAgents)
	assert.InDelta(t, 9.5, profile.TopCostAgents[0].LatencySec, 1e-9)
	assert.NoError(t, mock.ExpectationsWereMet())
}
