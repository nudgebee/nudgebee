//go:build e2e

package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/common"
	"nudgebee/llm/config"
	llmruntime "nudgebee/llm/llms/runtime"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
)

// Notebook v2 (structured plan, #41443) live comparison: every case runs on
// ReAct4 once with the free-text notebook (v1) and once with the structured
// plan (v2), and one JSON line per run is appended to NOTEBOOK_V2_REPORT
// (default notebook_v2_report.jsonl) for aggregation.
//
//	TEST_ACCOUNT=<id> TEST_USER=<id> TEST_TENANT=<id> NOTEBOOK_V2_CASES=k8s_restart_rca,simple_list \
//	  go test -tags e2e -run TestNotebookV2LiveComparison -v -timeout 90m ./agents/
//
// NOTEBOOK_V2_VARIANTS=v2 runs only the plan variant.
type notebookV2Case struct {
	name    string
	agent   string
	query   string
	eventID string
}

var notebookV2Cases = []notebookV2Case{
	{name: "k8s_restart_rca", agent: "k8s_orchestrator", query: "Why have pods in the nudgebee namespace restarted in the last 6 hours? Give the root cause."},
	{name: "k8s_memory_rca", agent: "k8s_orchestrator", query: "Investigate why llm-server pods are using so much memory and whether they are close to their limits."},
	{name: "k8s_oom_check", agent: "k8s_orchestrator", query: "Were any pods OOMKilled in the last 24 hours? If so, why?"},
	{name: "k8s_health_sweep", agent: "k8s_orchestrator", query: "Check the pods, services and recent warning events in the nudgebee namespace and tell me if anything looks unhealthy."},
	{name: "k8s_simple_list", agent: "k8s_orchestrator", query: "List the deployments in the nudgebee namespace with their ready replica counts."},
	{name: "k8s_simple_count", agent: "k8s_orchestrator", query: "How many nodes does this cluster have?"},
	{name: "event_rca_1", agent: "k8s_orchestrator", query: "Identify RootCause of the event with id 8103549d-63c2-46fd-a410-4b40d72bd7e8", eventID: "8103549d-63c2-46fd-a410-4b40d72bd7e8"},
	{name: "event_rca_2", agent: "k8s_orchestrator", query: "Identify RootCause of the event with id 9ea8b3a2-210b-498a-8f10-887e3c52f16c", eventID: "9ea8b3a2-210b-498a-8f10-887e3c52f16c"},
}

type notebookV2Run struct {
	Case            string   `json:"case"`
	Variant         string   `json:"variant"`
	OK              bool     `json:"ok"`
	Error           string   `json:"error,omitempty"`
	ConversationID  string   `json:"conversation_id"`
	LatencySec      float64  `json:"latency_sec"`
	ToolCalls       int      `json:"tool_calls"`
	TotalCalls      int      `json:"total_calls"` // including sub-agents (the task budget)
	PlanReasks      int64    `json:"plan_reasks"`
	CritiqueRefines int      `json:"critique_refines"`
	NotebookCalls   int      `json:"notebook_calls"`
	FirstNotebookIt int      `json:"first_notebook_iteration"`
	Iterations      int      `json:"iterations"`
	TaggedCalls     int      `json:"tagged_calls"`
	NamedCalls      int      `json:"named_calls"` // tagged by the model itself, not the in-progress fallback
	PlanSteps       int      `json:"plan_steps"`
	Hypotheses      int      `json:"hypotheses"`
	OpenStepsEnd    int      `json:"open_steps_end"`
	GateRejections  int64    `json:"gate_rejections"`
	GateBudgetUsed  int64    `json:"gate_budget_used"`
	NotVerifiedLine bool     `json:"not_verified_line"`
	PlanInAnswer    bool     `json:"plan_in_answer"`
	InputTokens     int      `json:"input_tokens"`
	OutputTokens    int      `json:"output_tokens"`
	Cost            float64  `json:"cost"`
	Correctness     float64  `json:"correctness"`
	Completeness    float64  `json:"completeness"`
	Tools           []string `json:"tools"`
	Answer          string   `json:"answer"`
}

// gateCounter counts the plan-gate log lines so each run's rejections can be
// read as a delta (runs are sequential).
type gateCounter struct {
	slog.Handler
	rejected, budgetUsed, reasked *atomic.Int64
}

func (h gateCounter) Handle(ctx context.Context, r slog.Record) error {
	switch r.Message {
	case "react4: plan gate sent the answer back":
		h.rejected.Add(1)
	case "react4: plan gate budget used; answering with open steps":
		h.budgetUsed.Add(1)
	case "react4: plan requirement re-asked":
		h.reasked.Add(1)
	}
	return h.Handler.Handle(ctx, r)
}

func (h gateCounter) WithAttrs(a []slog.Attr) slog.Handler {
	return gateCounter{Handler: h.Handler.WithAttrs(a), rejected: h.rejected, budgetUsed: h.budgetUsed, reasked: h.reasked}
}

func (h gateCounter) WithGroup(n string) slog.Handler {
	return gateCounter{Handler: h.Handler.WithGroup(n), rejected: h.rejected, budgetUsed: h.budgetUsed, reasked: h.reasked}
}

func TestNotebookV2LiveComparison(t *testing.T) {
	skipIfNoFixtureEnv(t)
	accountID := os.Getenv("TEST_ACCOUNT")
	userID := os.Getenv("TEST_USER")
	var rejected, budgetUsed, reasked atomic.Int64
	// Wrap a fresh stderr handler: wrapping slog.Default()'s own handler and
	// installing it as the default deadlocks (that handler logs through the
	// log package, which calls back into the new default).
	prevLogger := slog.Default()
	inner := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(gateCounter{Handler: inner, rejected: &rejected, budgetUsed: &budgetUsed, reasked: &reasked}))
	// Created after the counting logger is installed: request contexts keep the
	// default logger they were created with.
	// A tenant + user scoped context, as production runs have: a bare super-admin
	// context has no tenant, so shell target lookup fails (empty tenant uuid) and
	// nudgebee_* tools refuse, pushing the model into sub-agent detours.
	sc := security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), userID, []string{accountID})
	if !core.SupportsNativeTools(llmruntime.GetLLMProvider(sc, accountID, "", false, ""), "") {
		t.Skip("provider is not native-tools capable; ReAct4 would not run")
	}
	origEngine, origV2 := config.Config.LlmServerPlannerEngine, config.Config.NotebookV2Enabled
	t.Cleanup(func() {
		slog.SetDefault(prevLogger)
		config.Config.LlmServerPlannerEngine, config.Config.NotebookV2Enabled = origEngine, origV2
	})
	config.Config.LlmServerPlannerEngine = core.PlannerEngineReAct4

	reportPath := os.Getenv("NOTEBOOK_V2_REPORT")
	if reportPath == "" {
		reportPath = "notebook_v2_report.jsonl"
	}
	report, err := os.OpenFile(reportPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open report: %v", err)
	}
	defer report.Close()

	selected := map[string]bool{}
	for _, n := range strings.Split(os.Getenv("NOTEBOOK_V2_CASES"), ",") {
		if n = strings.TrimSpace(n); n != "" {
			selected[n] = true
		}
	}
	// NOTEBOOK_V2_EVENTS=<id>,<id> replays real event investigations: each uses
	// the exact root-cause prompt the event pipeline last sent for that event.
	cases := notebookV2Cases
	if ev := strings.TrimSpace(os.Getenv("NOTEBOOK_V2_EVENTS")); ev != "" {
		cases = realEventCases(t, strings.Split(ev, ","))
		selected = map[string]bool{}
	}
	// NOTEBOOK_V2_QUERY runs one ad-hoc chat question (as a user would ask it).
	if q := strings.TrimSpace(os.Getenv("NOTEBOOK_V2_QUERY")); q != "" {
		name := os.Getenv("NOTEBOOK_V2_CASE_NAME")
		if name == "" {
			name = "adhoc"
		}
		cases = []notebookV2Case{{name: name, agent: "k8s_orchestrator", query: q}}
		selected = map[string]bool{}
	}
	variants := []string{"v1", "v2"}
	if v := os.Getenv("NOTEBOOK_V2_VARIANTS"); v != "" {
		variants = strings.Split(v, ",")
	}

	for _, c := range cases {
		if len(selected) > 0 && !selected[c.name] {
			continue
		}
		for _, variant := range variants {
			t.Run(c.name+"/"+variant, func(t *testing.T) {
				config.Config.NotebookV2Enabled = variant == "v2"
				r0, b0, q0 := rejected.Load(), budgetUsed.Load(), reasked.Load()
				run := runNotebookV2Case(t, sc, accountID, userID, c, variant)
				run.GateRejections, run.GateBudgetUsed = rejected.Load()-r0, budgetUsed.Load()-b0
				run.PlanReasks = reasked.Load() - q0
				line, _ := json.Marshal(run)
				_, _ = report.Write(append(line, '\n'))
				slog.Info("notebook v2 run", "case", run.Case, "variant", run.Variant, "ok", run.OK,
					"notebook_calls", run.NotebookCalls, "first_notebook_iteration", run.FirstNotebookIt,
					"tagged", fmt.Sprintf("%d/%d", run.TaggedCalls, run.ToolCalls), "plan_steps", run.PlanSteps,
					"open_end", run.OpenStepsEnd, "gate_rejections", run.GateRejections, "latency_sec", run.LatencySec,
					"tokens", run.InputTokens+run.OutputTokens, "correctness", run.Correctness)
			})
		}
	}
}

func runNotebookV2Case(t *testing.T, sc *security.RequestContext, accountID, userID string, c notebookV2Case, variant string) notebookV2Run {
	t.Helper()
	run := notebookV2Run{Case: c.name, Variant: variant}
	agent, ok := core.GetNBAgent(sc, c.agent, accountID, core.AgentStatusEnabled)
	if !ok {
		run.Error = "agent not found"
		return run
	}
	sessionID := fmt.Sprintf("nbv2-%s-%s-%d", c.name, variant, time.Now().Unix())
	// Only event runs carry the Investigation source, as in production; chat
	// questions keep the default so the planner's own investigation check decides.
	opts := []core.ConversationSessionRequestConfig{core.ConversationSessionRequestWithEnableCritique(true)}
	if c.eventID != "" {
		opts = append(opts,
			core.ConversationSessionRequestWithSource(core.ConversationSourceInvestigation),
			core.ConversationSessionRequestWithConfig(toolcore.NBQueryConfig{EventId: c.eventID}))
	}

	start := time.Now()
	resp, err := core.HandleConversationSessionRequest(sc, agent, userID, accountID, sessionID, c.query, opts...)
	run.LatencySec = time.Since(start).Seconds()
	if err != nil {
		run.Error = err.Error()
		return run
	}
	run.OK = resp.Status == core.ConversationStatusCompleted
	run.Answer = firstNonEmpty(resp.Response)
	run.NotVerifiedLine = strings.Contains(run.Answer, "_Not verified:")
	run.PlanInAnswer = strings.Contains(run.Answer, "## Plan") || strings.Contains(run.Answer, "[IN_PROGRESS]")

	run.ConversationID = resp.ConversationId
	if run.ConversationID == "" {
		if conv, cErr := core.GetConversationDao().GetConversationBySession(accountID, sessionID); cErr == nil {
			run.ConversationID = conv.ID.String()
		}
	}
	time.Sleep(3 * time.Second) // last rows land just after the response
	collectNotebookV2Metrics(t, &run)

	for _, m := range settledTokenUsage(sessionID, accountID) {
		run.InputTokens += m.InputTokens
		run.OutputTokens += m.OutputTokens
		run.Cost += m.Cost
	}
	if eval, evalErr := core.EvaluateAgentResponse(sc, resp, accountID, agent.GetSupportedTools(sc), userID); evalErr == nil {
		run.Correctness = eval.QueryResponseMetrics.Correctness
		run.Completeness = eval.QueryResponseMetrics.Completeness
	}
	return run
}

// settledTokenUsage polls until the usage row count stops growing. Note that
// GetConversationTokenUsage filters on the SESSION id despite its parameter name.
func settledTokenUsage(sessionID, accountID string) []core.TokenMetrics {
	var metrics []core.TokenMetrics
	prev := -1
	for attempt := 0; attempt < 10; attempt++ {
		m, err := core.GetConversationDao().GetConversationTokenUsage(sessionID, accountID)
		if err != nil {
			return metrics
		}
		if len(m) > 0 && len(m) == prev {
			return m
		}
		prev, metrics = len(m), m
		time.Sleep(2 * time.Second)
	}
	return metrics
}

func collectNotebookV2Metrics(t *testing.T, run *notebookV2Run) {
	t.Helper()
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil || run.ConversationID == "" {
		return
	}
	// Top-level agent only: sub-agents keep the v1 notebook by design, and
	// their calls are not plan work of the top-level run.
	rows, err := db.Db.Query(`SELECT t.tool_name, COALESCE(t.metadata::text, '') FROM llm_conversation_tool_calls t
		JOIN llm_conversation_agent a ON a.id = t.agent_id
		WHERE t.conversation_id = $1 AND a.agent_name LIKE '%orchestrator%' ORDER BY t.created_at`, run.ConversationID)
	if err != nil {
		t.Logf("tool call read failed: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var name, meta string
		if rows.Scan(&name, &meta) != nil {
			continue
		}
		var md struct {
			PlannerIteration int    `json:"planner_iteration"`
			PlanStep         string `json:"plan_step"`
			Inferred         bool   `json:"plan_step_inferred"`
		}
		_ = json.Unmarshal([]byte(meta), &md)
		if md.PlannerIteration > run.Iterations {
			run.Iterations = md.PlannerIteration
		}
		if name == toolcore.NotebookToolName || name == toolcore.NotebookV2ToolName {
			run.NotebookCalls++
			if run.FirstNotebookIt == 0 {
				run.FirstNotebookIt = md.PlannerIteration
			}
			continue
		}
		run.ToolCalls++
		run.Tools = append(run.Tools, name)
		if md.PlanStep != "" {
			run.TaggedCalls++
			if !md.Inferred {
				run.NamedCalls++
			}
		}
	}

	_ = db.Db.QueryRow(`SELECT count(*) FROM llm_conversation_tool_calls WHERE conversation_id = $1`, run.ConversationID).Scan(&run.TotalCalls)
	_ = db.Db.QueryRow(`SELECT count(*) FROM llm_conversation_agent_critiques WHERE conversation_id = $1 AND decision = 'refine'`, run.ConversationID).Scan(&run.CritiqueRefines)

	var state string
	_ = db.Db.QueryRow(`SELECT COALESCE(state, '') FROM llm_conversation_agent
		WHERE conversation_id = $1 AND agent_name = 'notebook' ORDER BY updated_at DESC LIMIT 1`, run.ConversationID).Scan(&state)
	var plan struct {
		Steps []toolcore.PlanStep `json:"steps"`
	}
	if json.Unmarshal([]byte(state), &plan) == nil {
		run.PlanSteps = len(plan.Steps)
		for _, s := range plan.Steps {
			if s.Kind == toolcore.PlanStepKindHypothesis {
				run.Hypotheses++
			}
			if toolcore.PlanStepIsOpen(s) {
				run.OpenStepsEnd++
			}
		}
	}
}

func realEventCases(t *testing.T, ids []string) []notebookV2Case {
	t.Helper()
	db, err := common.GetDatabaseManager(common.Metastore)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	var out []notebookV2Case
	for _, id := range ids {
		id = strings.TrimSpace(id)
		var msg string
		if err := db.Db.QueryRow(`SELECT message FROM llm_conversation_messages
			WHERE message_config::jsonb ->> 'event_id' = $1 AND message LIKE 'Identify RootCause of the event%'
			ORDER BY created_at DESC LIMIT 1`, id).Scan(&msg); err != nil {
			t.Fatalf("no root-cause prompt for event %s: %v", id, err)
		}
		out = append(out, notebookV2Case{name: "event_" + id[:8], agent: "k8s_orchestrator", query: msg, eventID: id})
	}
	return out
}
