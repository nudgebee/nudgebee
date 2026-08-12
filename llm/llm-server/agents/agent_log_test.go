package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// TestLogAgent_BuildToolList: the LLM-visible tool surface must be uniform
// across providers — provider-specific dispatch lives inside fetch_logs.
// Re-introducing any of the mustNotHave tools would expose the per-provider
// pipeline to the LLM and bypass the investigation classifier.
func TestLogAgent_BuildToolList(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()

	mustHave := []string{tools.ToolResourceSearch, FetchLogsAgentName}
	mustNotHave := []string{
		"query_generator",
		"datadog_log_query",
		"kubectl_intent_generator",
		"kubectl_execute",
		// The resource_search AGENT is deliberately not exposed: it costs an
		// extra LLM call to translate natural language into the tool call the
		// logs agent can already make itself, and fans out to cloud/Datadog
		// resource search that a pod lookup never needs. Locked in both
		// directions so a future edit can't silently swap the tool back for
		// the agent. See logAgentToolNames.
		ResourceSearchAgentName,
	}

	cases := []struct {
		name     string
		provider string
	}{
		{"loki provider", "loki"},
		{"signoz provider", "signoz"},
		{"es provider", "es"},
		{"elasticsearch provider", "elasticsearch"},
		{"datadog provider", "datadog"},
		{"no provider falls back to kubectl-only path", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agent := newLogAgent("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
				services_server.ObservabilityProvider{Provider: tc.provider})
			tl := agent.GetSupportedTools(ctx)
			names := toolNamesForTest(tl)

			for _, expected := range mustHave {
				assert.Contains(t, names, expected,
					"provider %q must expose %q (uniform across all providers)", tc.provider, expected)
			}
			for _, forbidden := range mustNotHave {
				assert.NotContains(t, names, forbidden,
					"provider %q must NOT expose %q — it should be hidden behind fetch_logs", tc.provider, forbidden)
			}
		})
	}
}

// TestFetchLogsAgent_Registered ensures fetch_logs is in the system registry
// so LogAgent.GetSupportedTools can resolve it by name.

func TestFetchLogsAgent_Registered(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	agent, ok := core.GetNBAgent(ctx, FetchLogsAgentName, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "")
	assert.True(t, ok, "fetch_logs must be registered as a system agent")
	assert.NotNil(t, agent)
	assert.Equal(t, FetchLogsAgentName, agent.GetName())
}

func toolNamesForTest(ts []toolcore.NBTool) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, strings.TrimSpace(t.Name()))
	}
	return out
}

// TestClassifyLogMode pins the deterministic intent classifier the LogAgent
// uses to decide whether to follow the routine, investigation, or enumeration
// branch of its prompt. The classifier prefers OriginalQuery over Query so
// that a parent planner's paraphrase ("Get logs for X") doesn't downgrade an
// investigation ("Were there issues with X?") to routine.

func TestClassifyLogMode(t *testing.T) {
	cases := []struct {
		name     string
		query    string
		original string
		want     logMode
	}{
		// Routine
		{"tail logs", "show me last logs for api-server", "", logModeRoutine},
		{"recent logs", "get recent logs of llm-server in nudgebee namespace", "", logModeRoutine},
		{"time range only", "logs between 10:00 and 11:00", "", logModeRoutine},

		// Investigation
		{"why is X broken", "why is api-service in app-108 failing", "", logModeInvestigation},
		{"diagnose", "diagnose checkout-api connection refused errors", "", logModeInvestigation},
		{"what caused", "what caused the outage in payment-api", "", logModeInvestigation},
		{"were there issues", "were there issues with cron-scheduler in ns-73b", "", logModeInvestigation},
		{"root cause", "find the root cause of the crash in worker-7", "", logModeInvestigation},

		// Enumeration
		{"list errors", "list all errors in payment-api last 1h", "", logModeEnumeration},
		{"show errors", "show me the errors in my-app-51", "", logModeEnumeration},
		{"distinct errors", "what distinct errors appear in checkout-api", "", logModeEnumeration},
		{"summarise errors", "summarize errors in cron-scheduler", "", logModeEnumeration},

		// OriginalQuery wins over per-step paraphrase — this is the case that
		// regressed in production today: parent emitted "Get logs for pod X"
		// and the LLM treated it as routine, skipping the investigation flow.
		{
			name:     "parent paraphrase loses intent without classifier",
			query:    "Get logs for pod cron-scheduler-xtcwd in namespace-73b",
			original: "Were there issues with the cron-scheduler pod in namespace-73b?",
			want:     logModeInvestigation,
		},
		{
			name:     "empty original falls back to query",
			query:    "why is api-server failing",
			original: "",
			want:     logModeInvestigation,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyLogMode(tc.query, tc.original)
			assert.Equalf(t, tc.want, got,
				"classifyLogMode(%q, %q) = %s; want %s",
				tc.query, tc.original, logModeName(got), logModeName(tc.want))
		})
	}
}

// TestSystemPrompt_NarrowsToOneMode verifies the prompt assembled by
// GetSystemPrompt contains ONLY the matching mode's workflow. With the
// previous "all three modes inline + MODE flag" approach the LLM
// occasionally followed the wrong block; with mode-specific assembly the
// wrong block isn't even visible.

func TestSystemPrompt_NarrowsToOneMode(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	cases := []struct {
		name     string
		query    string
		original string
		wantMode string
		mustHave []string // substrings that must appear
		mustMiss []string // substrings that must NOT appear (other modes' content)
	}{
		{
			name:     "investigation prompt has investigation block, no routine/enumeration",
			query:    "Get logs for pod cron-scheduler-xtcwd in namespace-73b",
			original: "Were there issues with the cron-scheduler pod in namespace-73b?",
			wantMode: "INVESTIGATION",
			mustHave: []string{
				"MODE = INVESTIGATION",
				"last 24h, limit 5000",
				"Mandatory diagnostic sweep",
				"Time-window framing",
				"Label anchor",
				"all logs for app <name>",
				"all logs for pod <name>",
			},
			mustMiss: []string{
				"Workflow (Routine):",
				"Workflow (Enumeration):",
				"Per-signature aggregation pipeline",
			},
		},
		{
			name:     "routine prompt has routine block, no investigation/enumeration",
			query:    "show me last 30m logs for api-server",
			original: "",
			wantMode: "ROUTINE",
			mustHave: []string{
				"MODE = ROUTINE",
				"Workflow (Routine):",
				"Single fetch is enough",
				"Label anchor",
			},
			mustMiss: []string{
				"Mandatory diagnostic sweep",
				"last 24h, limit 5000",
				"Per-signature aggregation pipeline",
				"Time-window framing",
			},
		},
		{
			name:     "enumeration prompt has enumeration block, no routine/investigation",
			query:    "list all errors in service-y",
			original: "",
			wantMode: "ENUMERATION",
			mustHave: []string{
				"MODE = ENUMERATION",
				"Workflow (Enumeration):",
				// New grep-on-text aggregation pipeline (replaces the jq one).
				"grep -iE 'error|fail|warn|exception|fatal|timeout|refused|denied'",
				"sort | uniq -c",
				"Label anchor",
			},
			mustMiss: []string{
				"Workflow (Routine):",
				"Mandatory diagnostic sweep",
				"last 24h, limit 5000",
				"Time-window framing",
				// jq pipeline was removed (broke after JSONL flatten).
				"jq -r '.logs[].message'",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agent := newLogAgent("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", services_server.ObservabilityProvider{Provider: "loki"})
			prompt := agent.GetSystemPrompt(ctx, core.NBAgentRequest{
				Query:         tc.query,
				OriginalQuery: tc.original,
			})
			body := strings.Join(prompt.Instructions, "\n")
			for _, sub := range tc.mustHave {
				assert.Contains(t, body, sub,
					"%s prompt should contain %q", tc.wantMode, sub)
			}
			for _, sub := range tc.mustMiss {
				assert.NotContains(t, body, sub,
					"%s prompt should NOT contain %q (belongs to a different mode)", tc.wantMode, sub)
			}
		})
	}
}

// TestAutoBundleGate_ModeAwareness locks the gating rule that runAuto
// DiagnosticBundle uses to decide whether to fire. The rule is:
//
//   - Investigation intent ("were there issues", "why", "broken",
//     "diagnose", …) → ALWAYS fire. The logs prompt actively tells the
//     LLM to strip error keywords from the fetch question, so keying only
//     on the fetch query text would miss the exact investigations the
//     bundle was built for.
//   - Enumeration intent ("list errors", "show me distinct errors") →
//     ALWAYS fire. That IS the enumeration answer.
//   - Routine intent → fire only when the wording explicitly names error
//     content (get error logs, any failures, …). Plain "tail last 100
//     lines" stays a one-shot fetch.
//
// This mirrors the same decision runAutoDiagnosticBundle makes inline; the
// test asserts the composed behaviour so a regression to a keyword-only gate
// would fail here.
func TestAutoBundleGate_ModeAwareness(t *testing.T) {
	cases := []struct {
		name     string
		query    string // per-step query the parent passed to fetch_logs
		original string // OriginalQuery — the user's verbatim question
		want     bool
	}{
		{"investigation via 'were there issues' fires (fetch query itself has no keyword)",
			"all logs for pod task-scheduler-abc in ns", "Were there issues with the task-scheduler pod", true},
		{"investigation via 'why is X broken' fires",
			"all logs for pod x", "why is service-y broken", true},
		{"enumeration via 'list errors' fires",
			"all logs last 1h", "list errors in service-z", true},
		{"routine plain tail does NOT fire",
			"tail last 100 lines", "", false},
		{"routine with error keyword fires (fallback)",
			"get error logs for pod x", "", true},
		{"routine with no error content stays cold",
			"give me the last 5 mins of logs", "recent logs for cron-scheduler", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode := classifyLogMode(tc.query, tc.original)
			q := strings.TrimSpace(tc.original)
			if q == "" {
				q = strings.TrimSpace(tc.query)
			}
			got := mode != logModeRoutine || autoBundleQueryRE.MatchString(q)
			assert.Equal(t, tc.want, got,
				"mode=%v q=%q — bundle gate mismatch", mode, q)
		})
	}
}

func TestAutoBundleQueryRE(t *testing.T) {
	// Guards the trigger-word list that gates the auto-diagnostic-bundle
	// inside fetch_logs. The list is deliberately narrow — a plain "tail
	// last 100 lines" must NOT trigger the bundle, or we regress fetch's
	// "one-shot, no extra work" semantic for routine viewing. Any
	// addition/removal here should be conscious.
	cases := []struct {
		q    string
		want bool
	}{
		{"get error logs for pod x", true},
		{"any failures in namespace y", true},
		{"show me warnings from last hour", true},
		{"was there a crash on this pod", true},
		{"any exceptions in api-server", true},
		{"timeout occurred?", true},
		{"OOMKilled events for job", true},
		{"tail last 100 lines", false},
		{"recent logs for cron-scheduler", false},
		{"give me the last 5 mins of logs", false},
		{"show pod restart count", false},
	}
	for _, tc := range cases {
		assert.Equalf(t, tc.want, autoBundleQueryRE.MatchString(tc.q),
			"autoBundleQueryRE.MatchString(%q)", tc.q)
	}
}

func TestSystemPrompt_BundleSignalInAllModes(t *testing.T) {
	// The bundle now fires deterministically inside fetch_logs and its
	// result ships in the fetch envelope's `bundle_signal` field — the
	// LLM never calls the tool itself. Every mode's prompt (investigation,
	// enumeration, routine) must tell the LLM to READ `bundle_signal`
	// from the fetch response, and none of them may reference the
	// standard_diagnostic_grep tool (dropping it from the toolset would be
	// misleading if the prompt still asked the LLM to call it).
	prev := config.Config.LogsStandardGrepEnabled
	config.Config.LogsStandardGrepEnabled = true
	defer func() { config.Config.LogsStandardGrepEnabled = prev }()

	ctx := security.NewRequestContextForSuperAdmin()
	agent := newLogAgent("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", services_server.ObservabilityProvider{Provider: "loki"})

	cases := []struct {
		name              string
		query             string
		original          string
		wantsBundleSignal bool
	}{
		{"investigation references bundle_signal", "get logs", "why is cron-scheduler broken", true},
		{"enumeration references bundle_signal", "list errors in service-y", "", true},
		{"routine always references bundle_signal (envelope carries it or not, LLM is told to read it)", "tail last 100 lines", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prompt := agent.GetSystemPrompt(ctx, core.NBAgentRequest{
				Query:         tc.query,
				OriginalQuery: tc.original,
			})
			body := strings.Join(prompt.Instructions, "\n")
			assert.NotContains(t, body, "standard_diagnostic_grep",
				"the bundle tool is no longer exposed to the LLM — prompt must not reference it")
			if tc.wantsBundleSignal {
				assert.Contains(t, body, "bundle_signal",
					"prompt should tell the LLM to read the pre-computed bundle signal from the fetch envelope")
			}
		})
	}
}

// TestLogAgent_BundleToolNotInToolset locks the change: after moving the
// bundle to deterministic auto-fire inside fetch_logs, the LLM's tool list
// must not include standard_diagnostic_grep — the LLM already has the
// bundle output in its fetch response and a redundant tool call would
// waste an LLM turn.
func TestLogAgent_BundleToolNotInToolset(t *testing.T) {
	prev := config.Config.LogsStandardGrepEnabled
	config.Config.LogsStandardGrepEnabled = true
	defer func() { config.Config.LogsStandardGrepEnabled = prev }()

	ctx := security.NewRequestContextForSuperAdmin()
	agent := newLogAgent("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", services_server.ObservabilityProvider{Provider: "loki"})
	names := toolNamesForTest(agent.GetSupportedTools(ctx))
	for _, n := range names {
		assert.NotEqual(t, "standard_diagnostic_grep", n,
			"standard_diagnostic_grep must not be exposed to the LLM — it runs auto inside fetch_logs")
	}
}

// TestSystemPrompt_NoFixtureLeaks fails if specific benchmark fixture names,
// fixture-answer time windows, or literal benchmark prompt phrasings appear
// in the LLM-visible system prompt or the agent description. The prompt
// drives behaviour on real production workloads; if it embeds fixture
// names, the LLM may treat those as canonical and behave differently for
// non-fixture queries.

func TestSystemPrompt_NoFixtureLeaks(t *testing.T) {
	// Names and phrases that exist as benchmark fixtures in
	// llm/benchmark/llm/agents/nubi/fixtures/.
	leaks := []string{
		// Pod / deployment names from the time-window-anomaly fixtures
		"cron-scheduler",
		"task-scheduler",
		// Fixture 51's deployment
		"my-app-51",
		"my-app",
		// Fixture 100a / 101 / 108
		"payment-api",
		"api-service",
		"app-108",
		// Fixture answer windows (would teach the LLM the answer)
		"03:00–03:05",
		"03:00-03:05",
		// Specific pod hashes from prior fixture runs
		"7d8b598678",
		"6fb6dd6b8c",
		// Literal benchmark prompts (verbatim user questions in test_case.yaml)
		"Were there issues with",
	}

	ctx := security.NewRequestContextForSuperAdmin()
	agent := newLogAgent("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", services_server.ObservabilityProvider{Provider: "loki"})
	tool := LogAgentTool{}

	cases := []struct {
		name    string
		surface string
	}{
		{
			"investigation prompt body",
			strings.Join(agent.GetSystemPrompt(ctx, core.NBAgentRequest{
				Query:         "Get logs for service-x",
				OriginalQuery: "why is service-x failing",
			}).Instructions, "\n"),
		},
		{
			"routine prompt body",
			strings.Join(agent.GetSystemPrompt(ctx, core.NBAgentRequest{
				Query: "show me last 30m logs for service-x",
			}).Instructions, "\n"),
		},
		{
			"enumeration prompt body",
			strings.Join(agent.GetSystemPrompt(ctx, core.NBAgentRequest{
				Query: "list errors in service-x",
			}).Instructions, "\n"),
		},
		{"agent description (registry)", agent.GetDescription()},
		{"agent description (tool wrapper)", tool.Description()},
		// fetch_logs's tool description and few-shot examples are also
		// LLM-visible surfaces — round-3 review caught a regression where
		// fixture names re-appeared here after they were scrubbed from
		// agent_log.go. Walk the entire agent_log_fetch.go source file to
		// catch leaks in tool descriptions, prompt strings, and production
		// code comments.
		{"agent_log_fetch.go source (covers tool desc, prompts, comments)", readAgentLogFetchSource(t)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, leak := range leaks {
				assert.NotContains(t, tc.surface, leak,
					"%s leaks fixture identifier %q — replace with an abstract pattern (e.g. `<workload>-<6-10 hex>-<5 alnum>`)",
					tc.name, leak)
			}
		})
	}
}

// readAgentLogFetchSource returns the contents of agent_log_fetch.go so
// TestSystemPrompt_NoFixtureLeaks can scan tool descriptions, few-shot
// examples, and production code comments for fixture-name leakage. Walking
// the source file is a more reliable guard than only sampling
// LLM-visible strings via accessor methods — comments are not LLM-visible
// but they document the LLM-visible code, and comment leaks rot when
// fixtures move.

func readAgentLogFetchSource(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile("agent_log_fetch.go")
	if err != nil {
		t.Fatalf("readAgentLogFetchSource: %v", err)
	}
	return string(body)
}

// fakeLogAgentForCall is a minimal core.NBAgent stand-in for
// TestBuildLogToolResponse_AdditionalDetails — it lets that test exercise
// buildLogToolResponse's branches directly against hand-built
// core.NBAgentResponse values, without running the real LogAgent (which
// requires a DB-backed ExecuteAgentToolCall and live LLM calls).
type fakeLogAgentForCall struct{}

func (fakeLogAgentForCall) GetName() string          { return LogsAgentName }
func (fakeLogAgentForCall) GetNameAliases() []string { return nil }
func (fakeLogAgentForCall) GetDescription() string   { return "" }
func (fakeLogAgentForCall) GetSupportedTools(ctx *security.RequestContext) []toolcore.NBTool {
	return nil
}
func (fakeLogAgentForCall) GetSystemPrompt(ctx *security.RequestContext, query core.NBAgentRequest) core.NBAgentPrompt {
	return core.NBAgentPrompt{}
}
func (fakeLogAgentForCall) GetPlannerType() core.AgentPlannerType { return core.AgentPlannerTypeReAct }

// fakeSummaryToolLogAgent additionally implements
// NBAgentReActPlannerSummaryToolProvider, to exercise buildLogToolResponse's
// summary-tool-provider branch (agent_log.go:545).
type fakeSummaryToolLogAgent struct{ fakeLogAgentForCall }

func (fakeSummaryToolLogAgent) GetSummaryToolName() string { return "shell_execute" }

// TestBuildLogToolResponse_AdditionalDetails is the regression test for bug
// A1 (log_analysis_bugs): LogAgentTool.Call() is a hand-copied variant of
// factory_agent.go's generic nbAgentTool.Call() that drifted and stopped
// setting AdditionalDetails["agent_id"]/["message_id"] — the keys
// planner_callback_handler.go reads to populate child_agent_id, which the UI
// conversation tree needs to nest fetch_logs/shell_execute under the `logs`
// node. It exercises every return path in buildLogToolResponse (the shaping
// logic extracted from Call()) to confirm AdditionalDetails survives all of
// them, not just the happy path.
func TestBuildLogToolResponse_AdditionalDetails(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	nbCtx := toolcore.NbToolContext{Ctx: ctx, AccountId: "acct", ConversationId: "conv"}
	input := toolcore.NBToolCallRequest{Command: "get logs for service-x"}

	const wantAgentId = "child-agent-id"
	const wantMessageId = "child-message-id"

	assertAdditionalDetails := func(t *testing.T, resp toolcore.NBToolResponse) {
		t.Helper()
		require.NotNil(t, resp.AdditionalDetails)
		assert.Equal(t, wantAgentId, resp.AdditionalDetails["agent_id"])
		assert.Equal(t, wantMessageId, resp.AdditionalDetails["message_id"])
	}

	t.Run("ExecuteAgentToolCall error still carries agent_id/message_id", func(t *testing.T) {
		agentResp := core.NBAgentResponse{AgentId: wantAgentId, MessageId: wantMessageId}
		resp, err := buildLogToolResponse(nbCtx, fakeLogAgentForCall{}, input, agentResp, assert.AnError)
		assert.ErrorIs(t, err, assert.AnError)
		assertAdditionalDetails(t, resp)
	})

	t.Run("empty response (ErrUnableToFetchData) still carries agent_id/message_id", func(t *testing.T) {
		agentResp := core.NBAgentResponse{AgentId: wantAgentId, MessageId: wantMessageId}
		resp, err := buildLogToolResponse(nbCtx, fakeLogAgentForCall{}, input, agentResp, nil)
		assert.ErrorIs(t, err, toolcore.ErrUnableToFetchData)
		assertAdditionalDetails(t, resp)
	})

	t.Run("SummaryToolProvider success path carries agent_id/message_id", func(t *testing.T) {
		agentResp := core.NBAgentResponse{AgentId: wantAgentId, MessageId: wantMessageId, Response: []string{"the logs"}}
		resp, err := buildLogToolResponse(nbCtx, fakeSummaryToolLogAgent{}, input, agentResp, nil)
		require.NoError(t, err)
		assert.Equal(t, "the logs", resp.Data)
		assertAdditionalDetails(t, resp)
	})

	t.Run("non-summary agent, matching step-response path carries agent_id/message_id", func(t *testing.T) {
		agentResp := core.NBAgentResponse{
			AgentId:   wantAgentId,
			MessageId: wantMessageId,
			Response:  []string{"the logs"},
			AgentStepResponse: []core.ToolInvocation{
				{Response: llms.ToolCallResponse{Content: "step output"}},
			},
		}
		resp, err := buildLogToolResponse(nbCtx, fakeLogAgentForCall{}, input, agentResp, nil)
		require.NoError(t, err)
		assert.Equal(t, "step output", resp.Data)
		assertAdditionalDetails(t, resp)
	})

	t.Run("non-summary agent, final fallback path carries agent_id/message_id", func(t *testing.T) {
		agentResp := core.NBAgentResponse{AgentId: wantAgentId, MessageId: wantMessageId, Response: []string{"the logs"}}
		resp, err := buildLogToolResponse(nbCtx, fakeLogAgentForCall{}, input, agentResp, nil)
		require.NoError(t, err)
		assert.Equal(t, "the logs", resp.Data)
		assertAdditionalDetails(t, resp)
	})
}

func TestGetLogAgent(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	agent, err := getLogAgent(sc, os.Getenv("TEST_ACCOUNT"))
	assert.Nil(t, err)
	assert.NotNil(t, agent)
	assert.Equal(t, LogsAgentName, agent.GetName())
}
