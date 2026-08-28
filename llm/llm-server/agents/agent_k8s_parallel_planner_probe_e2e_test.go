//go:build e2e

package agents

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
)

type k8sPlannerProbe struct {
	planner             *core.NBReActPlanner3
	ctx                 *security.RequestContext
	promptElapsed       time.Duration
	plannerSetupElapsed time.Duration
	testStarted         time.Time
}

func newK8sPlannerProbe(t *testing.T, query string) k8sPlannerProbe {
	t.Helper()
	if os.Getenv("RUN_LIVE_PLANNER_PROBES") != "1" {
		t.Skip("set RUN_LIVE_PLANNER_PROBES=1 to make the single paid planner call")
	}
	skipIfNoFixtureEnv(t)

	accountID := os.Getenv("TEST_ACCOUNT")
	userID := os.Getenv("TEST_USER")
	req := core.NBAgentRequest{
		Query:              query,
		OriginalQuery:      query,
		AccountId:          accountID,
		UserId:             userID,
		ConversationSource: core.ConversationSourceUserInvestigation,
	}
	// Pin the lean implementation so a local k8s_orchestrator mode override
	// cannot silently turn this React3 probe into a different agent comparison.
	agent := newK8sLeanAgentNamed(accountID, AgentK8sOrchestratorName)
	testStarted := time.Now()

	baseCtx := security.NewRequestContextForTenantAccountAdmin(
		os.Getenv("TEST_TENANT"), userID, []string{accountID},
	)
	goCtx := context.WithValue(baseCtx.GetContext(), core.ContextKeyModelTier, core.ModelTierReasoning)
	ctx := security.NewRequestContext(
		goCtx,
		baseCtx.GetSecurityContext(),
		baseCtx.GetLogger(),
		baseCtx.GetTracer(),
		baseCtx.GetMeter(),
	)

	promptStarted := time.Now()
	basePrompt := agent.GetSystemPrompt(ctx, req)
	systemMessage, err := core.GetPromptTemplate(basePrompt, req, core.AgentPlannerTypeReAct3).
		Format(map[string]any{"history": ""})
	require.NoError(t, err)
	promptElapsed := time.Since(promptStarted)

	plannerStarted := time.Now()
	planner, err := core.NewReActAgent3(ctx, req, agent, systemMessage, nil, "")
	require.NoError(t, err)

	return k8sPlannerProbe{
		planner:             planner,
		ctx:                 ctx,
		promptElapsed:       promptElapsed,
		plannerSetupElapsed: time.Since(plannerStarted),
		testStarted:         testStarted,
	}
}

func assertParallelPlannerActions(t *testing.T, namespace string, actions []core.NBAgentPlannerToolAction) {
	t.Helper()
	for i, action := range actions {
		t.Logf("action[%d] tool=%s input=%q", i, action.Tool, action.ToolInput)
	}
	require.GreaterOrEqual(t, len(actions), 2,
		"expected one parallel batch; a single action means the planner serialized independent checks")
	require.LessOrEqual(t, len(actions), 5, "parallel evidence batch should remain bounded")
	for _, action := range actions {
		require.Contains(t, strings.ToLower(action.ToolInput), strings.ToLower(namespace),
			"each evidence check should retain the supplied namespace")
	}
}

func logPlannerActionShape(t *testing.T, namespace string, actions []core.NBAgentPlannerToolAction) {
	t.Helper()
	for i, action := range actions {
		t.Logf("action[%d] tool=%s input=%q dependencies=%v", i, action.Tool, action.ToolInput, action.Dependency)
		require.Contains(t, strings.ToLower(action.ToolInput), strings.ToLower(namespace),
			"each evidence check should retain the discovered namespace")
	}
	require.NotEmpty(t, actions, "the planner should collect evidence before answering")
	require.LessOrEqual(t, len(actions), 5, "evidence batch should remain bounded")
	if len(actions) == 1 {
		t.Log("planner shape: sequential (one runnable action)")
		return
	}
	t.Logf("planner shape: fan-out candidate (%d actions)", len(actions))
}

// TestK8sPlanner_FirstTurnParallelProbe isolates the model-to-planner boundary:
// it makes exactly one real planner generation and deliberately does not execute
// the returned tools. The query supplies every input needed by four independent
// read-only checks, so a single action indicates a planning/generation problem,
// not a discovery dependency or executor downgrade.
//
// This probe is opt-in because it calls the configured LLM provider and incurs
// cost. It does not execute the planned Kubernetes calls; constructing the real
// agent may still attempt integration discovery, so unavailable local services
// can add setup latency without invalidating the planner result.
//
//	RUN_LIVE_PLANNER_PROBES=1 TEST_ACCOUNT=<id> TEST_USER=<id> TEST_TENANT=<id> \
//	  go test -tags=e2e -run TestK8sPlanner_FirstTurnParallelProbe -v ./agents
func TestK8sPlanner_FirstTurnParallelProbe(t *testing.T) {
	namespace := strings.TrimSpace(os.Getenv("TEST_K8S_NAMESPACE"))
	if namespace == "" {
		namespace = "nudgebee"
	}
	query := fmt.Sprintf(
		"Inspect namespace %q and report independently on deployment readiness, pod restart state, recent warning events, and service endpoint coverage. All targets and the namespace are already known. Use read-only Kubernetes checks and do not remediate.",
		namespace,
	)

	probe := newK8sPlannerProbe(t, query)

	started := time.Now()
	actions, finish, err := probe.planner.Plan(probe.ctx.GetContext(), nil, query)
	elapsed := time.Since(started)
	require.NoError(t, err)
	require.Nil(t, finish, "the first turn should collect live evidence, not answer without tools")

	t.Logf("first planner generation: actions=%d prompt_setup=%s planner_setup=%s generation=%s total=%s",
		len(actions), probe.promptElapsed, probe.plannerSetupElapsed, elapsed, time.Since(probe.testStarted))
	assertParallelPlannerActions(t, namespace, actions)
}

// TestK8sPlanner_PostDiscoveryNaturalProbe characterizes the planner after a
// realistic scope-discovery result. Unlike the positive control above, neither
// the query nor the observation names evidence branches or asks for independent
// checks. The result therefore shows whether the planner discovers a parallel
// frontier without being handed one.
//
// This probe deliberately does not require multiple actions: model output is
// stochastic, and the behavior under measurement is the baseline rather than a
// contract. The action shape is emitted in the test log for comparison across
// prompt versions and repeated runs.
func TestK8sPlanner_PostDiscoveryNaturalProbe(t *testing.T) {
	const namespace = "parallel-probe"
	const query = "Investigate why checkout-api is unavailable in namespace parallel-probe."
	probe := newK8sPlannerProbe(t, query)

	discovery := core.NBAgentPlannerToolActionStep{
		Action: core.NBAgentPlannerToolAction{
			Tool:       "kubectl_execute",
			ToolInput:  `{"command":"kubectl get deployment,pod,service -n parallel-probe -l app=checkout-api -o wide"}`,
			ToolID:     "probe-discovery",
			DisplayID:  "E1",
			Dependency: []string{},
		},
		Observation: `NAME                              READY   UP-TO-DATE   AVAILABLE   AGE
deployment.apps/checkout-api      1/2     2            1           3d

NAME                                   READY   STATUS    RESTARTS   AGE   IP
pod/checkout-api-7b9f5c6d8-x2k4p      0/1     Running   3          27m   10.42.1.17

NAME                   TYPE        CLUSTER-IP   EXTERNAL-IP   PORT(S)    AGE
service/checkout-api   ClusterIP   10.43.8.91   <none>        8080/TCP   3d`,
		Status: core.ToolStatusSuccess,
	}

	started := time.Now()
	actions, finish, err := probe.planner.Plan(probe.ctx.GetContext(), []core.NBAgentPlannerToolActionStep{discovery}, query)
	elapsed := time.Since(started)
	require.NoError(t, err)
	require.Nil(t, finish, "discovery established the symptom but not its cause")

	t.Logf("post-discovery planner generation: actions=%d prompt_setup=%s planner_setup=%s generation=%s total=%s",
		len(actions), probe.promptElapsed, probe.plannerSetupElapsed, elapsed, time.Since(probe.testStarted))
	logPlannerActionShape(t, namespace, actions)
}

// TestK8sPlanner_PostEvidenceWaveNaturalProbe characterizes the next planning
// turn after independent discovery and evidence actions have completed. The
// observations leave two plausible leads (probe configuration and downstream
// connectivity) without prescribing how to investigate them. This distinguishes
// healthy convergence from a planner that always falls back to depth-first work
// after its first parallel batch.
func TestK8sPlanner_PostEvidenceWaveNaturalProbe(t *testing.T) {
	const namespace = "parallel-probe"
	const query = "Investigate why checkout-api is unavailable in namespace parallel-probe."
	probe := newK8sPlannerProbe(t, query)

	steps := []core.NBAgentPlannerToolActionStep{
		{
			Action: core.NBAgentPlannerToolAction{
				Tool:      "kubectl_execute",
				ToolInput: `{"command":"kubectl get deployment,pod,service -n parallel-probe -l app=checkout-api -o wide"}`,
				ToolID:    "probe-discovery",
				DisplayID: "E1",
			},
			Observation: `deployment.apps/checkout-api 1/2; pod/checkout-api-7b9f5c6d8-x2k4p 0/1 Running restarts=3; service/checkout-api ClusterIP 10.43.8.91:8080`,
			Status:      core.ToolStatusSuccess,
		},
		{
			Action: core.NBAgentPlannerToolAction{
				Tool:       "kubectl_execute",
				ToolInput:  `{"command":"kubectl describe pod checkout-api-7b9f5c6d8-x2k4p -n parallel-probe"}`,
				ToolID:     "probe-describe",
				DisplayID:  "E2",
				Dependency: []string{},
			},
			Observation: `Ready=False. Readiness probe GET http://:8080/health returned connection refused. Limits: cpu 500m, memory 512Mi. Last state terminated with exit code 1.`,
			Status:      core.ToolStatusSuccess,
		},
		{
			Action: core.NBAgentPlannerToolAction{
				Tool:       "logs",
				ToolInput:  `{"command":"Show checkout-api errors in namespace parallel-probe"}`,
				ToolID:     "probe-logs",
				DisplayID:  "E3",
				Dependency: []string{},
			},
			Observation: `checkout-api: startup retry: dial tcp payments-api.parallel-probe.svc:9090: connect: connection refused; retrying`,
			Status:      core.ToolStatusSuccess,
		},
		{
			Action: core.NBAgentPlannerToolAction{
				Tool:       "events",
				ToolInput:  `{"command":"Get checkout-api warning events in namespace parallel-probe"}`,
				ToolID:     "probe-events",
				DisplayID:  "E4",
				Dependency: []string{},
			},
			Observation: `Warning Unhealthy: Readiness probe failed: connect: connection refused. No scheduling, image-pull, eviction, or volume warnings.`,
			Status:      core.ToolStatusSuccess,
		},
		{
			Action: core.NBAgentPlannerToolAction{
				Tool:       "kubectl_execute",
				ToolInput:  `{"command":"kubectl top pod checkout-api-7b9f5c6d8-x2k4p -n parallel-probe"}`,
				ToolID:     "probe-resources",
				DisplayID:  "E5",
				Dependency: []string{},
			},
			Observation: `checkout-api-7b9f5c6d8-x2k4p 22m 96Mi`,
			Status:      core.ToolStatusSuccess,
		},
	}

	started := time.Now()
	actions, finish, err := probe.planner.Plan(probe.ctx.GetContext(), steps, query)
	elapsed := time.Since(started)
	require.NoError(t, err)
	require.Nil(t, finish, "the evidence identifies leads but has not established the cause")

	t.Logf("post-evidence planner generation: actions=%d prompt_setup=%s planner_setup=%s generation=%s total=%s",
		len(actions), probe.promptElapsed, probe.plannerSetupElapsed, elapsed, time.Since(probe.testStarted))
	logPlannerActionShape(t, namespace, actions)
}
