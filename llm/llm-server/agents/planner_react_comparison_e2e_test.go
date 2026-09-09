//go:build e2e

package agents

import (
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
)

// react4ComparisonCase is one query run through both planners for a
// react_3-vs-react_4 regression comparison.
type react4ComparisonCase struct {
	name  string // subtest name
	agent string // registered agent name (must be a ReAct/Orchestrating agent)
	query string // the user question
	// mustHitTools, when set, are tool names react_4 is expected to still invoke.
	// Use for cases where dropping a tool is a clear regression (e.g. an RCA that
	// must run kubectl). Leave nil to only compare scores/outcome.
	mustHitTools []string
}

// react4ComparisonCases is a SEED set — extend it with cases representative of
// your traffic (RCA, cloud spend, logs, metrics, multi-tool investigations).
// Keep agent names to ReAct/Orchestrating agents; Tool/Custom agents never route
// to react_4 so a comparison there is meaningless.
var react4ComparisonCases = []react4ComparisonCase{
	{
		name:         "k8s_pod_rca",
		agent:        "k8s_orchestrator",
		query:        "Why is the checkout-service pod crash-looping in the production namespace? Give the root cause.",
		mustHitTools: []string{"kubectl"},
	},
	{
		name:  "k8s_logs_investigation",
		agent: "k8s_orchestrator",
		query: "Find and summarize the errors in the api-server logs over the last hour.",
	},
	{
		name:  "aws_cost_investigation",
		agent: "aws_orchestrator",
		query: "Which AWS service drove the biggest cost increase this month and why?",
	},
	{
		name:  "parallel_multitool",
		agent: "k8s_orchestrator",
		query: "Check the pods, services, and recent events in the payments namespace and tell me if anything looks unhealthy.",
	},
}

type react4RunResult struct {
	ok        bool
	err       error
	eval      core.AgentResponseEvaluationResult
	toolNames []string
	steps     int
	latency   time.Duration
	answer    string

	// Token usage for this run, summed across the conversation's agents from
	// llm_conversation_token_usage (populated by GenerateAndTrackLLMContent).
	inputTokens       int // total input (cached + non-cached + cache-creation)
	outputTokens      int
	cachedInputTokens int
	cost              float64
}

func (r react4RunResult) invokedTool(name string) bool {
	for _, t := range r.toolNames {
		if t == name {
			return true
		}
	}
	return false
}

// TestReAct3VsReAct4Comparison runs each seed case through react_3 (flag off) and
// react_4 (flag on) against the same live backend, scores both with the eval
// framework, and fails on a regression: react_4 erroring where react_3 succeeded,
// a correctness drop beyond threshold, or a must-hit tool going uncalled.
//
// Requires a live test environment. Run with:
//
//	TEST_ACCOUNT=<id> TEST_USER=<id> go test -tags e2e -run TestReAct3VsReAct4Comparison -v ./agents/core/
func TestReAct3VsReAct4Comparison(t *testing.T) {
	skipIfNoFixtureEnv(t)
	accountID := os.Getenv("TEST_ACCOUNT")
	userID := os.Getenv("TEST_USER")
	sc := security.NewRequestContextForSuperAdmin()

	// react_4 only engages on a native-tools-capable provider; otherwise both
	// runs would execute react_3 and the comparison is vacuous.
	provider := core.GetLLMProvider(sc, accountID, "", false, "")
	if !core.SupportsNativeTools(provider, "") {
		t.Skipf("provider %q is not native-tools capable — react_4 would fall back to react_3; nothing to compare", provider)
	}

	origFlag := config.Config.LlmServerReAct4Enabled
	t.Cleanup(func() { config.Config.LlmServerReAct4Enabled = origFlag })

	// Correctness may drop this much before it counts as a regression — native
	// tool calling can legitimately reword answers. Tune to taste.
	const correctnessRegressionThreshold = 0.15

	var summary []string
	// Aggregate token/cost totals across all comparable cases — the headline
	// "how much are we saving" answer.
	var r3TotalIn, r3TotalOut, r4TotalIn, r4TotalOut int
	var r3TotalCost, r4TotalCost float64
	for _, c := range react4ComparisonCases {
		t.Run(c.name, func(t *testing.T) {
			r3 := runReact4ComparisonCase(t, sc, accountID, userID, c, false)
			r4 := runReact4ComparisonCase(t, sc, accountID, userID, c, true)

			slog.Info("react4 comparison",
				"case", c.name,
				"r3_ok", r3.ok, "r4_ok", r4.ok,
				"r3_correctness", r3.eval.QueryResponseMetrics.Correctness,
				"r4_correctness", r4.eval.QueryResponseMetrics.Correctness,
				"r3_completeness", r3.eval.QueryResponseMetrics.Completeness,
				"r4_completeness", r4.eval.QueryResponseMetrics.Completeness,
				"r3_tools", r3.toolNames, "r4_tools", r4.toolNames,
				"r3_steps", r3.steps, "r4_steps", r4.steps,
				"r3_in_tokens", r3.inputTokens, "r4_in_tokens", r4.inputTokens,
				"r3_out_tokens", r3.outputTokens, "r4_out_tokens", r4.outputTokens,
				"r3_cached_in", r3.cachedInputTokens, "r4_cached_in", r4.cachedInputTokens,
				"r3_cost", r3.cost, "r4_cost", r4.cost,
				"r3_latency", r3.latency.String(), "r4_latency", r4.latency.String(),
			)
			summary = append(summary, fmt.Sprintf(
				"%-28s r3(ok=%v corr=%.2f in=%d out=%d $%.4f) r4(ok=%v corr=%.2f in=%d out=%d $%.4f) tokΔ=%s",
				c.name,
				r3.ok, r3.eval.QueryResponseMetrics.Correctness, r3.inputTokens, r3.outputTokens, r3.cost,
				r4.ok, r4.eval.QueryResponseMetrics.Correctness, r4.inputTokens, r4.outputTokens, r4.cost,
				pctDelta(r3.inputTokens+r3.outputTokens, r4.inputTokens+r4.outputTokens),
			))
			if r3.ok && r4.ok {
				r3TotalIn += r3.inputTokens
				r3TotalOut += r3.outputTokens
				r4TotalIn += r4.inputTokens
				r4TotalOut += r4.outputTokens
				r3TotalCost += r3.cost
				r4TotalCost += r4.cost
			}

			// Hard regression: react_4 failed where react_3 succeeded.
			if r3.ok && !r4.ok {
				t.Errorf("HARD REGRESSION [%s]: react_4 failed where react_3 succeeded: %v", c.name, r4.err)
				return
			}
			if !r3.ok {
				t.Skipf("react_3 baseline itself failed (%v) — cannot compare; fix the case or environment", r3.err)
			}

			// Correctness regression beyond threshold.
			if drop := r3.eval.QueryResponseMetrics.Correctness - r4.eval.QueryResponseMetrics.Correctness; drop > correctnessRegressionThreshold {
				t.Errorf("CORRECTNESS REGRESSION [%s]: react_3=%.2f react_4=%.2f (drop %.2f > %.2f)",
					c.name, r3.eval.QueryResponseMetrics.Correctness, r4.eval.QueryResponseMetrics.Correctness, drop, correctnessRegressionThreshold)
			}

			// Must-hit tool trajectory.
			for _, tool := range c.mustHitTools {
				if r3.invokedTool(tool) && !r4.invokedTool(tool) {
					t.Errorf("TOOL REGRESSION [%s]: react_3 invoked %q but react_4 did not", c.name, tool)
				}
			}
		})
	}

	slog.Info("react4 comparison summary")
	for _, line := range summary {
		slog.Info(line)
	}

	// Headline token/cost verdict across all comparable (both-succeeded) cases.
	r3Total := r3TotalIn + r3TotalOut
	r4Total := r4TotalIn + r4TotalOut
	slog.Info("react4 TOKEN SAVINGS (react_3 -> react_4, both-succeeded cases only)",
		"input_tokens", fmt.Sprintf("%d -> %d (%s)", r3TotalIn, r4TotalIn, pctDelta(r3TotalIn, r4TotalIn)),
		"output_tokens", fmt.Sprintf("%d -> %d (%s)", r3TotalOut, r4TotalOut, pctDelta(r3TotalOut, r4TotalOut)),
		"total_tokens", fmt.Sprintf("%d -> %d (%s)", r3Total, r4Total, pctDelta(r3Total, r4Total)),
		"cost", fmt.Sprintf("$%.4f -> $%.4f (%s)", r3TotalCost, r4TotalCost, pctDeltaF(r3TotalCost, r4TotalCost)),
	)
}

// pctDelta renders the change from old to new as a signed percentage; negative
// means react_4 used fewer (a saving).
func pctDelta(oldV, newV int) string {
	if oldV == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%+.1f%%", 100*float64(newV-oldV)/float64(oldV))
}

func pctDeltaF(oldV, newV float64) string {
	if oldV == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%+.1f%%", 100*(newV-oldV)/oldV)
}

// runReact4ComparisonCase executes one case under the requested planner and
// scores the response. A fresh conversation id isolates the two runs so neither
// planner sees the other's history.
func runReact4ComparisonCase(t *testing.T, sc *security.RequestContext, accountID, userID string, c react4ComparisonCase, react4 bool) react4RunResult {
	t.Helper()
	config.Config.LlmServerReAct4Enabled = react4

	planner := "react3"
	if react4 {
		planner = "react4"
	}

	agent, ok := core.GetNBAgent(sc, c.agent, accountID, core.AgentStatusEnabled)
	if !ok {
		return react4RunResult{err: fmt.Errorf("agent %q not found/enabled", c.agent)}
	}

	// Distinct session per (case, planner) so neither planner ever sees the
	// other's history, and reset it so a rerun starts clean.
	sessionID := fmt.Sprintf("react4cmp-%s-%s", c.name, planner)
	_ = core.DeleteConversationBySession(sessionID, accountID, userID)

	opts := []core.ConversationSessionRequestConfig{
		core.ConversationSessionRequestWithSource(core.ConversationSourceInvestigation),
		core.ConversationSessionRequestWithEnableCritique(true),
	}

	start := time.Now()
	resp, err := core.HandleConversationSessionRequest(sc, agent, userID, accountID, sessionID, c.query, opts...)
	latency := time.Since(start)
	if err != nil {
		return react4RunResult{err: err, latency: latency}
	}

	res := react4RunResult{
		ok:      resp.Status == core.ConversationStatusCompleted,
		latency: latency,
		steps:   len(resp.AgentStepResponse),
		answer:  firstNonEmpty(resp.Response),
	}
	for _, step := range resp.AgentStepResponse {
		if step.Call.FunctionCall != nil {
			res.toolNames = append(res.toolNames, step.Call.FunctionCall.Name)
		}
	}

	// Actual token usage for this run (the whole point of the comparison — is
	// react_4 cheaper?). Read from the persisted usage rows for this run's
	// isolated conversation id.
	// resp.ConversationId is only populated when the CALLER supplied a
	// conversation id (see conversation.go's ConversationId ternary); this
	// harness lets the session create one, so it comes back empty and the usage
	// query silently matched zero rows — reporting 0 tokens / $0 for runs that
	// had really spent 10 and 53 usage rows. Resolve the real id by session.
	conversationID := resp.ConversationId
	if conversationID == "" {
		if conv, cErr := core.GetConversationDao().GetConversationBySession(accountID, sessionID); cErr == nil {
			conversationID = conv.ID.String()
		} else {
			slog.Warn("react4 comparison: conversation lookup by session failed",
				"case", c.name, "planner", planner, "session", sessionID, "error", cErr)
		}
	}
	// Usage rows are written as the run proceeds but the last ones land around
	// (and just after) the response returning, so an immediate read races them and
	// silently reports 0 tokens / $0 — the query succeeds, it just matches nothing.
	// Poll until the count stops growing rather than sleeping a fixed amount:
	// a 4-call run settles in a second, an 80-call run does not.
	var metrics []core.TokenMetrics
	prevCount := -1
	for attempt := 0; attempt < 10; attempt++ {
		m, tErr := core.GetConversationDao().GetConversationTokenUsage(conversationID, accountID)
		if tErr != nil {
			slog.Warn("react4 comparison: token usage read failed", "case", c.name, "planner", planner, "error", tErr)
			break
		}
		if len(m) > 0 && len(m) == prevCount {
			metrics = m
			break
		}
		prevCount = len(m)
		metrics = m
		time.Sleep(2 * time.Second)
	}
	if len(metrics) == 0 {
		slog.Warn("react4 comparison: no token usage rows found — cost/token figures for this run are NOT trustworthy",
			"case", c.name, "planner", planner, "conversation_id", conversationID)
	}
	for _, m := range metrics {
		res.inputTokens += m.InputTokens
		res.outputTokens += m.OutputTokens
		res.cachedInputTokens += m.CachedInputTokens
		res.cost += m.Cost
	}

	eval, evalErr := core.EvaluateAgentResponse(sc, resp, accountID, agent.GetSupportedTools(sc), userID)
	if evalErr != nil {
		slog.Warn("react4 comparison: eval failed", "case", c.name, "planner", planner, "error", evalErr)
	} else {
		res.eval = eval
	}
	return res
}

func firstNonEmpty(ss []string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
