//go:build e2e

package core

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/require"
)

// TestCustomPlanner_SameToolSiblingActionsProbe isolates custom-planner
// decomposition from tool execution. It makes one paid planner generation and
// stops before running any command. All three evidence inputs are known,
// non-conflicting, and independent; returning one action therefore identifies planner generation
// as the serialization boundary rather than discovery, the executor, or the shell
// adapter.
//
// The ordinary unit suite already covers prompt rendering and parsing plural
// <actions>. This opt-in probe covers the model behavior between those boundaries.
//
//	RUN_LIVE_PLANNER_PROBES=1 TEST_ACCOUNT=<id> TEST_USER=<id> TEST_TENANT=<id> \
//	  go test -tags=e2e -run TestCustomPlanner_SameToolSiblingActionsProbe -v ./agents/core
func TestCustomPlanner_SameToolSiblingActionsProbe(t *testing.T) {
	const query = `Investigate the synthetic service slowdown. The environment is already known. Independently inspect and summarize /tmp/probe-metrics.json, /tmp/probe-logs.json, and /tmp/probe-traces.json. Use one bounded command per evidence source, run independent work in parallel, and do not modify files.`
	const instructions = `You are a general-purpose terminal investigation agent. Use only orca_shell_execute. The shell input is raw text. Run independent, non-conflicting evidence checks in parallel and keep each result bounded.`

	planner, ctx := newCustomParallelPlannerProbe(t, query, instructions)
	assertCustomParallelProbe(t, planner, ctx, query, nil, true)
}

// TestCustomPlanner_NaturalInvestigationDecompositionProbe measures whether the
// planner discovers the same parallel frontier without the user enumerating the
// evidence branches. The custom-agent instructions expose the already-known
// environment capabilities, as a real adapter or agent prompt would; the user asks
// only the natural investigation question.
func TestCustomPlanner_NaturalInvestigationDecompositionProbe(t *testing.T) {
	const query = `Investigate why the synthetic checkout service was unusually slow during the last 30 minutes. Do not modify the environment.`
	const instructions = `You are a general-purpose terminal investigation agent. Use only orca_shell_execute with raw shell input. The environment is already known: bounded telemetry snapshots for the requested window are available at /tmp/probe-metrics.json, /tmp/probe-logs.json, and /tmp/probe-traces.json. Reuse these capabilities without discovery. Collect enough independent evidence to identify the likely cause, and do not modify files.`

	planner, ctx := newCustomParallelPlannerProbe(t, query, instructions)
	assertCustomParallelProbe(t, planner, ctx, query, nil, false)
}

// TestCustomReact4_NaturalInvestigationDecompositionProbe runs the same natural
// decomposition check through the provider-native planner and the dedicated
// react_4 custom base. It stops before tool execution.
func TestCustomReact4_NaturalInvestigationDecompositionProbe(t *testing.T) {
	const query = `Investigate why the synthetic checkout service was unusually slow during the last 30 minutes. Do not modify the environment.`
	const instructions = `You are a general-purpose terminal investigation agent. Use only orca_shell_execute with raw shell input. The environment is already known: bounded telemetry snapshots for the requested window are available at /tmp/probe-metrics.json, /tmp/probe-logs.json, and /tmp/probe-traces.json. Reuse these capabilities without discovery. Collect enough independent evidence to identify the likely cause, and do not modify files.`

	planner, ctx := newCustomParallelPlannerProbeWithEngine(t, query, instructions, true)
	assertCustomParallelProbe(t, planner, ctx, query, nil, false)
}

// TestCustomPlanner_PostDiscoveryDecompositionProbe mirrors the ORCA boundary:
// one shell action has discovered the environment, and the next planner decision
// must turn those capabilities into sibling evidence actions instead of delegating
// one broad sequential investigation.
func TestCustomPlanner_PostDiscoveryDecompositionProbe(t *testing.T) {
	testCustomPostDiscoveryDecompositionProbe(t, false)
}

func testCustomPostDiscoveryDecompositionProbe(t *testing.T, react4 bool) {
	t.Helper()
	const query = `Investigate why the synthetic checkout service was unusually slow during the last 30 minutes. Do not modify the environment.`
	const instructions = `You are a general-purpose terminal investigation agent. Use only orca_shell_execute with raw shell input. Start with bounded capability discovery. Once the environment is known, collect independent evidence in parallel, then synthesize the likely cause. Do not modify files.`

	planner, ctx := newCustomParallelPlannerProbeWithEngine(t, query, instructions, react4)
	discovery := NBAgentPlannerToolActionStep{
		Action: NBAgentPlannerToolAction{
			Tool:      "orca_shell_execute",
			ToolInput: `{"command":"discover telemetry capabilities"}`,
			ToolID:    "probe-discovery",
			DisplayID: "E1",
		},
		Observation: `Environment: Linux; working directory: /app. The requested 30-minute interval is resolved. Bounded telemetry snapshots are available at /tmp/probe-metrics.json, /tmp/probe-logs.json, and /tmp/probe-traces.json. Each file can be queried independently with standard non-interactive shell commands.`,
		Status:      ToolStatusSuccess,
	}

	assertCustomParallelProbe(t, planner, ctx, query, []NBAgentPlannerToolActionStep{discovery}, false)
}

func newCustomParallelPlannerProbe(t *testing.T, query, instructions string) (NBAgentPlanner, *security.RequestContext) {
	return newCustomParallelPlannerProbeWithEngine(t, query, instructions, false)
}

func newCustomParallelPlannerProbeWithEngine(t *testing.T, query, instructions string, react4 bool) (NBAgentPlanner, *security.RequestContext) {
	t.Helper()
	if os.Getenv("RUN_LIVE_PLANNER_PROBES") != "1" {
		t.Skip("set RUN_LIVE_PLANNER_PROBES=1 to make the single paid planner call")
	}
	accountID := strings.TrimSpace(os.Getenv("TEST_ACCOUNT"))
	userID := strings.TrimSpace(os.Getenv("TEST_USER"))
	tenantID := strings.TrimSpace(os.Getenv("TEST_TENANT"))
	if accountID == "" || userID == "" || tenantID == "" {
		t.Skip("TEST_ACCOUNT, TEST_USER, and TEST_TENANT are required")
	}

	req := NBAgentRequest{
		Query:              query,
		OriginalQuery:      query,
		AccountId:          accountID,
		UserId:             userID,
		ConversationSource: ConversationSourceUserInvestigation,
	}
	agent := &nbCustomAgent{
		accountId: accountID,
		agent: AgentDto{
			Name:         "custom_parallel_probe",
			Description:  "Synthetic custom-agent planner decomposition probe",
			ExecutorType: AgentPlannerTypeOrchestrating,
			SystemPrompt: instructions,
		},
		tools: []toolcore.NBTool{customParallelProbeShellTool{}},
	}

	baseCtx := security.NewRequestContextForTenantAccountAdmin(tenantID, userID, []string{accountID})
	goCtx := context.WithValue(baseCtx.GetContext(), ContextKeyModelTier, ModelTierReasoning)
	ctx := security.NewRequestContext(
		goCtx,
		baseCtx.GetSecurityContext(),
		baseCtx.GetLogger(),
		baseCtx.GetTracer(),
		baseCtx.GetMeter(),
	)

	basePrompt := agent.GetSystemPrompt(ctx, req)
	systemMessage, err := GetPromptTemplate(basePrompt, req, AgentPlannerTypeReAct3).
		Format(map[string]any{"history": ""})
	require.NoError(t, err)

	var planner NBAgentPlanner
	if react4 {
		planner, err = NewReActAgent4(ctx, req, agent, systemMessage, nil, "")
	} else {
		planner, err = NewReActAgent3(ctx, req, agent, systemMessage, nil, "")
	}
	require.NoError(t, err)
	return planner, ctx
}

type customParallelProbeShellTool struct{}

func (customParallelProbeShellTool) Name() string { return "orca_shell_execute" }
func (customParallelProbeShellTool) Description() string {
	return "Execute one raw non-interactive shell command in the benchmark environment. Use the command field. Independent, non-conflicting calls may run in parallel."
}
func (customParallelProbeShellTool) Call(_ toolcore.NbToolContext, _ toolcore.NBToolCallRequest) (toolcore.NBToolResponse, error) {
	return toolcore.NBToolResponse{}, nil
}
func (customParallelProbeShellTool) GetType() toolcore.NBToolType { return toolcore.NBToolTypeTool }
func (customParallelProbeShellTool) InputSchema() toolcore.ToolSchema {
	return toolcore.ToolSchema{
		Type: toolcore.ToolSchemaTypeObject,
		Properties: map[string]toolcore.ToolSchemaProperty{
			"command": {
				Type:        toolcore.ToolSchemaTypeString,
				Description: "Raw shell command to execute",
			},
		},
		Required: []string{"command"},
	}
}

func assertCustomParallelProbe(t *testing.T, planner NBAgentPlanner, ctx *security.RequestContext, query string, steps []NBAgentPlannerToolActionStep, exactCount bool) {
	t.Helper()
	started := time.Now()
	actions, finish, err := planner.Plan(ctx.GetContext(), steps, query)
	require.NoError(t, err)
	require.Nil(t, finish, "the first turn should collect evidence, not answer without tools")

	for i, action := range actions {
		t.Logf("action[%d] tool=%s input=%q dependencies=%v", i, action.Tool, action.ToolInput, action.Dependency)
	}
	t.Logf("custom planner generation: actions=%d elapsed=%s", len(actions), time.Since(started))

	if exactCount {
		require.Len(t, actions, 3,
			"three explicitly requested independent evidence sources should become sibling actions")
	} else {
		require.GreaterOrEqual(t, len(actions), 2,
			"the natural investigation should discover a parallel evidence frontier")
		require.LessOrEqual(t, len(actions), 5, "the evidence batch should remain bounded")
	}
	for _, action := range actions {
		require.Equal(t, "orca_shell_execute", action.Tool)
		require.Empty(t, action.Dependency, "independent evidence actions must not depend on one another")
	}

	var inputBuilder strings.Builder
	for _, action := range actions {
		inputBuilder.WriteString(action.ToolInput)
		inputBuilder.WriteByte('\n')
	}
	inputs := strings.ToLower(inputBuilder.String())
	for _, path := range []string{"/tmp/probe-metrics.json", "/tmp/probe-logs.json", "/tmp/probe-traces.json"} {
		require.Contains(t, inputs, path, "the evidence batch must retain every requested source")
	}
}
