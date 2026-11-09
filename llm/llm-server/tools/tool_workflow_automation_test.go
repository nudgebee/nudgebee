package tools

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func automationCandidateFixture(id, name, llmDesc string, inputs ...automationInput) automationCandidate {
	return automationCandidate{
		ID:             id,
		Name:           name,
		LLMDescription: llmDesc,
		Inputs:         inputs,
	}
}

// The tool name is part of what the model selects on, so it carries the
// author's wording rather than an opaque id.
func TestAutomationToolNameUsesTheAuthorsWording(t *testing.T) {
	tools := buildAutomationTools("acct", []automationCandidate{
		automationCandidateFixture("wf-1", "Restart Crashlooping Pods", "Use when pods are crashlooping."),
	})
	require.Len(t, tools, 1)
	assert.Equal(t, "automation_restart_crashlooping_pods", tools[0].Name())
}

// Two automations must never share a tool name — the second would shadow the
// first and the model would silently run the wrong one.
func TestAutomationToolNamesAreUnique(t *testing.T) {
	tools := buildAutomationTools("acct", []automationCandidate{
		automationCandidateFixture("aaaaaaaa-1111-2222-3333-444444444444", "Restart Pods", "First."),
		automationCandidateFixture("bbbbbbbb-5555-6666-7777-888888888888", "restart pods!", "Second, slugs the same."),
	})
	require.Len(t, tools, 2)
	assert.NotEqual(t, tools[0].Name(), tools[1].Name())
	assert.Equal(t, "automation_restart_pods", tools[0].Name())
	assert.Contains(t, tools[1].Name(), "bbbbbbbb", "collision suffix should come from the workflow id")
}

// Odd names must still produce a usable tool name rather than an empty or
// punctuation-only one.
func TestAutomationToolNameHandlesAwkwardNames(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"!!!", "automation_workflow"},
		{"  Spaces   And   Gaps  ", "automation_spaces_and_gaps"},
		{"MiXeD-Case_Stuff", "automation_mixed_case_stuff"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tools := buildAutomationTools("acct", []automationCandidate{
				automationCandidateFixture("wf-1", tc.name, "desc"),
			})
			require.Len(t, tools, 1)
			assert.Equal(t, tc.want, tools[0].Name())
		})
	}
}

// The schema is the reason this exists as much as discovery is: workflow_trigger
// takes a free-form blob the model assembles by hand, and it silently dropped an
// optional input in testing.
func TestAutomationToolSchemaDeclaresTheAutomationsInputs(t *testing.T) {
	tools := buildAutomationTools("acct", []automationCandidate{
		automationCandidateFixture("wf-1", "Restart Pods", "Use when pods crashloop.",
			automationInput{ID: "namespace", Type: "string", Required: true, Description: "Namespace to act on"},
			automationInput{ID: "dry_run", Type: "bool", Default: false},
			automationInput{ID: "replicas", Type: "int", Default: 3},
		),
	})
	require.Len(t, tools, 1)
	schema := tools[0].InputSchema()

	assert.Equal(t, core.ToolSchemaTypeString, schema.Properties["namespace"].Type)
	assert.Equal(t, core.ToolSchemaTypeBoolean, schema.Properties["dry_run"].Type)
	assert.Equal(t, core.ToolSchemaTypeNumber, schema.Properties["replicas"].Type)
	assert.Equal(t, 3, schema.Properties["replicas"].Default)
	assert.Equal(t, "Namespace to act on", schema.Properties["namespace"].Description)

	assert.Contains(t, schema.Required, "namespace", "a required input must be required on the tool too")
	assert.NotContains(t, schema.Required, "dry_run", "an input with a default is genuinely optional")
}

// An input the author left unticked but gave no default is required of the AI.
// The engine does not stop a run over it — it interpolates the empty value into
// whatever the automation does — so an omitted `service_name` restarts nothing,
// or everything. The author's escape hatch is to give the input a default.
func TestAutomationToolSchemaRequiresAnInputWithNoDefault(t *testing.T) {
	tools := buildAutomationTools("acct", []automationCandidate{
		automationCandidateFixture("wf-1", "Restart Service", "Use when a service is wedged.",
			automationInput{ID: "service_name", Type: "string"},
		),
	})
	require.Len(t, tools, 1)

	assert.Contains(t, tools[0].InputSchema().Required, "service_name")
}

// The model must not be able to run an automation without saying why. Enforced
// by schema validation rather than by prompt wording, so it cannot be skipped.
func TestAutomationToolAlwaysRequiresAReason(t *testing.T) {
	tools := buildAutomationTools("acct", []automationCandidate{
		automationCandidateFixture("wf-1", "Restart Pods", "Use when pods crashloop."),
	})
	require.Len(t, tools, 1)
	schema := tools[0].InputSchema()

	assert.Contains(t, schema.Required, defaultReasonArg)
	assert.NotEmpty(t, schema.Properties[defaultReasonArg].Description)
}

// An automation may legitimately declare its own input called "reason" — "reason
// for restart", for an audit trail. Reserving the name unconditionally would
// silently drop that input, which is the failure this PR fixed twice elsewhere.
// The reasoning field yields instead and both survive.
func TestAutomationToolReasonArgYieldsToADeclaredInput(t *testing.T) {
	tools := buildAutomationTools("acct", []automationCandidate{
		automationCandidateFixture("wf-1", "Restart Pods", "Use when pods crashloop.",
			automationInput{ID: defaultReasonArg, Type: "string", Required: true,
				Description: "the automation's own reason field"},
		),
	})
	require.Len(t, tools, 1)
	schema := tools[0].InputSchema()

	// The automation's declared input keeps the name and its own description.
	assert.Equal(t, "the automation's own reason field", schema.Properties[defaultReasonArg].Description)
	assert.Contains(t, schema.Required, defaultReasonArg)

	// The reasoning field moved aside rather than disappearing.
	assert.NotEmpty(t, schema.Properties[fallbackReasonArg].Description)
	assert.Contains(t, schema.Required, fallbackReasonArg)
}

// Both names taken: the automation's inputs are the user's data and must win.
func TestAutomationToolReasonArgKeepsMovingWhenBothNamesAreTaken(t *testing.T) {
	tools := buildAutomationTools("acct", []automationCandidate{
		automationCandidateFixture("wf-1", "Restart Pods", "Use when pods crashloop.",
			automationInput{ID: defaultReasonArg, Type: "string"},
			automationInput{ID: fallbackReasonArg, Type: "string"},
		),
	})
	require.Len(t, tools, 1)
	schema := tools[0].InputSchema()

	assert.Contains(t, schema.Properties, "ai_reason_2")
	assert.Contains(t, schema.Required, "ai_reason_2")
	// Neither declared input was lost.
	assert.Contains(t, schema.Properties, defaultReasonArg)
	assert.Contains(t, schema.Properties, fallbackReasonArg)
}

// Running an automation is a write: it must be classified so the executor's
// ask-before-run confirmation and create RBAC both fire. Unclassified tools
// default to Read, which would skip both.
func TestAutomationToolIsClassifiedAsAWrite(t *testing.T) {
	tools := buildAutomationTools("acct", []automationCandidate{
		automationCandidateFixture("wf-1", "Restart Pods", "Use when pods crashloop."),
	})
	require.Len(t, tools, 1)

	classifier, ok := tools[0].(core.ToolRequestInference)
	require.True(t, ok, "automation tools must implement ToolRequestInference")

	requestType, err := classifier.InferToolRequestType(nil, tools[0].Name(), "")
	require.NoError(t, err)
	assert.Equal(t, core.ToolRequestTypeCreate, requestType)
}

// Auto-approve suppresses the confirmation card, and nothing else. The
// classification stays a write so the RBAC check in the executor is untouched,
// and an automation nobody opted in still confirms.
func TestAutomationToolAutoApproveOnlySkipsTheConfirmation(t *testing.T) {
	auto := automationCandidateFixture("wf-1", "Restart Pods", "Use when pods crashloop.")
	auto.AutoApprove = true
	manual := automationCandidateFixture("wf-2", "Drain Queue", "Use when the queue is stuck.")

	built := buildAutomationTools("acct", []automationCandidate{auto, manual})
	require.Len(t, built, 2)

	exemptTool, ok := built[0].(core.ToolConfirmationExempt)
	require.True(t, ok, "automation tools must implement ToolConfirmationExempt")
	assert.True(t, exemptTool.SkipWriteConfirmation(""))

	confirmingTool, ok := built[1].(core.ToolConfirmationExempt)
	require.True(t, ok)
	assert.False(t, confirmingTool.SkipWriteConfirmation(""), "an automation nobody opted in must still confirm")

	// Both are still writes: the exemption never touches authorization.
	for _, tool := range built {
		classifier, ok := tool.(core.ToolRequestInference)
		require.True(t, ok)
		requestType, err := classifier.InferToolRequestType(nil, tool.Name(), "")
		require.NoError(t, err)
		assert.Equal(t, core.ToolRequestTypeCreate, requestType)
	}

	// And the model is told, so it can say what it is about to run unprompted.
	assert.Contains(t, built[0].Description(), "WITHOUT asking the user to confirm")
	assert.Contains(t, built[1].Description(), "after they confirm")
}

// The description is what drives selection, so it has to carry the author's
// words — and the automation's name, which is how the assistant names it back
// to the user.
func TestAutomationToolDescriptionCarriesTheAuthorsText(t *testing.T) {
	c := automationCandidateFixture("wf-1", "Restart Crashlooping Pods",
		"Use when pods are crashlooping or restarting repeatedly.")
	c.Description = "Restarts stuck payment consumers"
	tools := buildAutomationTools("acct", []automationCandidate{c})
	require.Len(t, tools, 1)

	desc := tools[0].Description()
	assert.Contains(t, desc, "Restart Crashlooping Pods")
	assert.Contains(t, desc, "Use when pods are crashlooping or restarting repeatedly.")
	assert.Contains(t, desc, "Restarts stuck payment consumers")
}

// An automation with no AI description is noise the model cannot select on.
// The server should not return these, so this is a guard rather than an
// expected branch — but a tool with an empty description would poison selection
// for the rest.
func TestAutomationToolSkipsCandidatesWithoutAnAIDescription(t *testing.T) {
	tools := buildAutomationTools("acct", []automationCandidate{
		automationCandidateFixture("wf-1", "No Description", "   "),
		automationCandidateFixture("", "No ID", "has a description"),
		automationCandidateFixture("wf-3", "Good", "Use when pods crashloop."),
	})
	require.Len(t, tools, 1)
	assert.Equal(t, "automation_good", tools[0].Name())
}

// An unbounded per-account list is how a tool catalog degrades later without
// anyone noticing. Real accounts sit well under the cap today.
func TestAutomationToolsAreCapped(t *testing.T) {
	candidates := make([]automationCandidate, 0, maxAutomationTools+5)
	for i := 0; i < maxAutomationTools+5; i++ {
		candidates = append(candidates, automationCandidateFixture(
			string(rune('a'+i%26))+"-id", "Automation "+string(rune('A'+i%26)), "desc"))
	}
	tools := buildAutomationTools("acct", candidates)
	assert.Len(t, tools, maxAutomationTools)
}

// The cache must not be poisoned by an empty account id, and an account with
// nothing opted in must produce no tools rather than a nil-deref.
func TestListAutomationToolsIgnoresEmptyAccount(t *testing.T) {
	assert.Nil(t, listAutomationTools(""))
}

func TestBuildAutomationToolsOnNoCandidates(t *testing.T) {
	assert.Empty(t, buildAutomationTools("acct", nil))
}

// --- request shapes ---
//
// The two places this file talks to runbook-server are where both live bugs
// were, and neither had a test: the pure functions above were all correct while
// the feature was inert. These assert what actually goes over the wire.

// stubRunbookServer points DoRunbookRequest at a local handler and stubs the
// tenant lookup, so the request shapes can be asserted without a metastore.
func stubRunbookServer(t *testing.T, tenantId string, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	originalURL := config.Config.WorkflowServerEndpoint
	config.Config.WorkflowServerEndpoint = srv.URL
	t.Cleanup(func() { config.Config.WorkflowServerEndpoint = originalURL })

	originalResolve := resolveTenantForAccount
	resolveTenantForAccount = func(string) (string, error) { return tenantId, nil }
	t.Cleanup(func() { resolveTenantForAccount = originalResolve })

	automationToolCacheInstance.delete("acct")
	t.Cleanup(func() { automationToolCacheInstance.delete("acct") })
}

// runbook-server refuses any request it cannot attribute to a tenant, so an
// empty X-Tenant-ID 401s and the whole feature goes silently inert — no tools
// built, nothing downstream reachable, only a warning per enumeration.
func TestFetchAutomationsSendsTheTenant(t *testing.T) {
	var gotTenant, gotPath string
	stubRunbookServer(t, "tenant-1", func(w http.ResponseWriter, r *http.Request) {
		gotTenant = r.Header.Get("X-Tenant-ID")
		gotPath = r.URL.RequestURI()
		_ = json.NewEncoder(w).Encode(automationSearchResponse{
			Workflows: []automationCandidate{
				automationCandidateFixture("wf-1", "Restart Pods", "Use when pods crashloop."),
			},
		})
	})

	tools := listAutomationTools("acct")
	require.Len(t, tools, 1)
	assert.Equal(t, "tenant-1", gotTenant, "an empty tenant makes every fetch 401")
	assert.Contains(t, gotPath, "workflows/ai-search")
}

// One past the cap, so an account that overflows is detectable rather than
// silently truncated by the server's own clamp.
func TestFetchAutomationsAsksForOneMoreThanTheCap(t *testing.T) {
	var gotQuery string
	stubRunbookServer(t, "tenant-1", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("limit")
		_ = json.NewEncoder(w).Encode(automationSearchResponse{})
	})

	listAutomationTools("acct")
	assert.Equal(t, strconv.Itoa(maxAutomationTools+1), gotQuery)
}

// ...but asking for one more than the cap only detects an overflow if the server
// will actually return it. runbook-server clamps the limit to its own
// maxAISearchLimit, so with the two constants equal — which is how this shipped
// — the +1 was clamped away, candidates could never exceed the cap, and both the
// truncation branch and the warning it exists to emit were unreachable. The test
// above passed throughout, because a well-formed request and a useful one are
// different claims.
//
// The constant is duplicated rather than imported because it lives in another Go
// module. That makes this a canary, not an enforcement: if runbook-server's clamp
// is ever lowered, this keeps passing while the detection silently dies again —
// hence the pointer back here in the comment on maxAISearchLimit.
func TestAutomationFetchLimitStaysBelowServerClamp(t *testing.T) {
	const runbookServerMaxAISearchLimit = 25 // runbook-server/internal/workflow/ai_search.go
	assert.LessOrEqual(t, maxAutomationTools+1, runbookServerMaxAISearchLimit,
		"the fetch limit must survive runbook-server's clamp, or overflow can never be detected")
}

// A failing account re-hit runbook-server on every enumeration — 14 times in one
// observed conversation. Cached briefly, so the "a transient error must not
// blank an account for the full TTL" property survives without the hammering.
func TestFailedAutomationLoadIsCachedBriefly(t *testing.T) {
	var calls int32
	stubRunbookServer(t, "tenant-1", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusUnauthorized)
	})

	assert.Nil(t, listAutomationTools("acct"))
	assert.Nil(t, listAutomationTools("acct"))
	assert.Nil(t, listAutomationTools("acct"))
	assert.Equal(t, int32(1), atomic.LoadInt32(&calls), "a failing account must not re-hit on every enumeration")
}

// The trigger endpoint binds the body directly as the inputs map. Wrapping it in
// an "inputs" key means a required input fails the run outright, and an
// automation with only optional inputs runs silently on its defaults.
func TestAutomationToolSendsInputsUnwrapped(t *testing.T) {
	var triggerBody map[string]any
	stubRunbookServer(t, "tenant-1", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/trigger") {
			_ = json.NewDecoder(r.Body).Decode(&triggerBody)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"workflow_id": "wf-1", "execution_id": "exec-1",
			})
			return
		}
		// The wait poll that follows the trigger.
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "COMPLETED"})
	})

	tool := &automationTool{
		accountId: "acct", id: "wf-1", name: "Restart Pods", toolName: "automation_restart_pods",
		description: "Use when pods crashloop.", reasonArg: defaultReasonArg,
		inputs: []automationInput{
			{ID: "namespace", Type: "string", Required: true},
			{ID: "service", Type: "string"},
		},
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	secCtx := security.NewSecurityContextForTenantAccountAdmin("tenant-1", "user-1", []string{"acct"})
	toolCtx := core.NbToolContext{
		AccountId: "acct",
		Ctx:       security.NewRequestContext(context.Background(), secCtx, logger, nil, nil),
	}

	_, err := tool.Call(toolCtx, core.NBToolCallRequest{Arguments: map[string]any{
		defaultReasonArg: "pod events show CrashLoopBackOff",
		"namespace":      "demo",
		"service":        "payment-service",
	}})
	require.NoError(t, err)

	require.NotNil(t, triggerBody)
	assert.Equal(t, "demo", triggerBody["namespace"], "inputs must be sent flat, not under an \"inputs\" key")
	assert.Equal(t, "payment-service", triggerBody["service"])
	assert.NotContains(t, triggerBody, "inputs")
	// The reasoning field is llm-server's, not an input the automation declared.
	assert.NotContains(t, triggerBody, defaultReasonArg)
}

// Without a reason the run must not happen at all — the check fires before any
// request is made.
func TestAutomationToolRefusesWithoutAReason(t *testing.T) {
	var called int32
	stubRunbookServer(t, "tenant-1", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&called, 1)
		_ = json.NewEncoder(w).Encode(map[string]any{})
	})

	tool := &automationTool{accountId: "acct", id: "wf-1", name: "Restart Pods",
		toolName: "automation_restart_pods", reasonArg: defaultReasonArg}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	secCtx := security.NewSecurityContextForTenantAccountAdmin("tenant-1", "user-1", []string{"acct"})
	toolCtx := core.NbToolContext{
		AccountId: "acct",
		Ctx:       security.NewRequestContext(context.Background(), secCtx, logger, nil, nil),
	}

	_, err := tool.Call(toolCtx, core.NBToolCallRequest{Arguments: map[string]any{"namespace": "demo"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), defaultReasonArg)
	assert.Equal(t, int32(0), atomic.LoadInt32(&called), "must refuse before touching the server")
}

// A misspelled parameter must be named, not dropped. Only declared inputs are
// forwarded to the trigger, so an argument the model invented would otherwise
// vanish and the automation would run with fewer inputs than it believed it
// passed — surfacing as an unresolved-template failure from the engine ("child
// workflow execution error"), which names neither the parameter nor the
// automation. Observed live: the model sent "service" for an automation
// declaring "service_name", the run started with no inputs at all and failed
// with an error nobody could act on.
func TestAutomationToolRejectsUnknownArguments(t *testing.T) {
	var called int32
	stubRunbookServer(t, "tenant-1", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&called, 1)
		_ = json.NewEncoder(w).Encode(map[string]any{})
	})

	tool := &automationTool{accountId: "acct", id: "wf-1", name: "Restart Service",
		toolName: "automation_restart_service", reasonArg: defaultReasonArg,
		inputs: []automationInput{{ID: "service_name", Type: "string"}}}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	secCtx := security.NewSecurityContextForTenantAccountAdmin("tenant-1", "user-1", []string{"acct"})
	toolCtx := core.NbToolContext{
		AccountId: "acct",
		Ctx:       security.NewRequestContext(context.Background(), secCtx, logger, nil, nil),
	}

	_, err := tool.Call(toolCtx, core.NBToolCallRequest{Arguments: map[string]any{
		defaultReasonArg: "the user asked for this",
		"service":        "payment",
	}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "service", "the rejected parameter must be named")
	assert.Contains(t, err.Error(), "service_name", "the accepted names must be offered so the model can correct itself")
	assert.Equal(t, int32(0), atomic.LoadInt32(&called), "must refuse before triggering the automation")
}

// The reasoning field is llm-server's own, not a declared input, so it must not
// be mistaken for an unknown argument.
func TestAutomationToolAcceptsTheReasonArgAsKnown(t *testing.T) {
	stubRunbookServer(t, "tenant-1", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "COMPLETED"})
	})

	tool := &automationTool{accountId: "acct", id: "wf-1", name: "Restart Service",
		toolName: "automation_restart_service", reasonArg: defaultReasonArg,
		inputs: []automationInput{{ID: "service_name", Type: "string"}}}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	secCtx := security.NewSecurityContextForTenantAccountAdmin("tenant-1", "user-1", []string{"acct"})
	toolCtx := core.NbToolContext{
		AccountId: "acct",
		Ctx:       security.NewRequestContext(context.Background(), secCtx, logger, nil, nil),
	}

	_, err := tool.Call(toolCtx, core.NBToolCallRequest{Arguments: map[string]any{
		defaultReasonArg: "the user asked for this",
		"service_name":   "payment",
	}})
	require.NoError(t, err)
}

// The approval card is what the user says yes to. The default one called a
// print-only automation "trying to create cluster resources" and showed the
// tool's internal name and raw arguments; it must name the automation and say
// what it does instead.
func TestAutomationToolConfirmationNamesTheAutomation(t *testing.T) {
	tools := buildAutomationTools("acct", []automationCandidate{
		automationCandidateFixture("wf-1", "QA sanity print check", "Prints one line. Use it to check that automations run."),
	})
	require.Len(t, tools, 1)
	prompter, ok := tools[0].(core.ToolConfirmationPrompt)
	require.True(t, ok, "without the prompt hook the executor falls back to the generic card")

	got := prompter.ConfirmationQuestion(`{"reason":"the user asked for this"}`)
	assert.Equal(t, "Run the automation \"QA sanity print check\"?\n"+
		"Prints one line. Use it to check that automations run.\n"+
		"Reason: the user asked for this", got)
	for _, leaked := range []string{"cluster resources", "Tool(", "Command - ", "automation_qa_sanity", `{"`} {
		assert.NotContains(t, got, leaked)
	}

	// Arguments that are not valid JSON still get a card that names the automation.
	assert.Equal(t, "Run the automation \"QA sanity print check\"?\n"+
		"Prints one line. Use it to check that automations run.", prompter.ConfirmationQuestion("not json"))
}

// The values the automation will run with are part of what is being approved,
// so they are listed — readably, in a stable order — rather than dropped.
func TestAutomationToolConfirmationListsInputs(t *testing.T) {
	tools := buildAutomationTools("acct", []automationCandidate{
		automationCandidateFixture("wf-1", "Restart Service", "Restarts one service.",
			// The automation declares its own "reason", so the assistant's
			// reasoning moves to the fallback name and both must show.
			automationInput{ID: "reason", Type: "string"},
			automationInput{ID: "namespace", Type: "string"},
			automationInput{ID: "replicas", Type: "int"},
			automationInput{ID: "labels", Type: "json"}),
	})
	require.Len(t, tools, 1)

	got := tools[0].(core.ToolConfirmationPrompt).ConfirmationQuestion(
		`{"ai_reason":"pods are crashlooping","reason":"audit: INC-12","namespace":"demo","replicas":2,"labels":{"app":"pay"}}`)
	assert.Equal(t, "Run the automation \"Restart Service\"?\n"+
		"Restarts one service.\n"+
		"Inputs:\n"+
		"- labels: {\"app\":\"pay\"}\n"+
		"- namespace: demo\n"+
		"- reason: audit: INC-12\n"+
		"- replicas: 2\n"+
		"Reason: pods are crashlooping", got)
}

// workflow_trigger is handed only an id, which tells the approver nothing. The
// card resolves it to the automation's name and description.
func TestWorkflowTriggerConfirmationResolvesTheAutomationName(t *testing.T) {
	automationToolCacheInstance.set("acct-confirm", buildAutomationTools("acct-confirm", []automationCandidate{
		automationCandidateFixture("fd6b3a25-6bce-44b9-911d-dac919dafb03", "QA sanity print check", "Prints one line."),
	}), time.Minute)
	t.Cleanup(func() { automationToolCacheInstance.delete("acct-confirm") })

	var tool core.NBTool = WorkflowTriggerTool{accountId: "acct-confirm"}
	prompter, ok := tool.(core.ToolConfirmationPrompt)
	require.True(t, ok, "without the prompt hook the executor falls back to the generic card")

	got := prompter.ConfirmationQuestion(`{"id":"fd6b3a25-6bce-44b9-911d-dac919dafb03"}`)
	assert.Equal(t, "Run the automation \"QA sanity print check\"?\nPrints one line.", got)
	assert.NotContains(t, got, "fd6b3a25", "a resolved automation is named, not shown as an id")

	got = prompter.ConfirmationQuestion(`{"id":"fd6b3a25-6bce-44b9-911d-dac919dafb03","inputs":{"namespace":"demo"}}`)
	assert.Equal(t, "Run the automation \"QA sanity print check\"?\nPrints one line.\nInputs:\n- namespace: demo", got)

	// An id outside the account's AI-invocable list still gets plain words.
	got = prompter.ConfirmationQuestion(`{"id":"wf-unknown"}`)
	assert.Equal(t, "Run the automation with ID wf-unknown?", got)

	// No id: nothing to name, so the executor keeps its default card.
	assert.Empty(t, prompter.ConfirmationQuestion(`{}`))
	assert.Empty(t, prompter.ConfirmationQuestion("not json"))
}

// The name lookup is best-effort: when the automation list cannot be loaded the
// card still appears, and still makes no claim about cluster resources.
func TestWorkflowTriggerConfirmationSurvivesAFailedLookup(t *testing.T) {
	originalResolve := resolveTenantForAccount
	resolveTenantForAccount = func(string) (string, error) { return "", errors.New("metastore unavailable") }
	t.Cleanup(func() { resolveTenantForAccount = originalResolve })
	automationToolCacheInstance.delete("acct-down")
	t.Cleanup(func() { automationToolCacheInstance.delete("acct-down") })

	assert.Equal(t, "Run the automation with ID wf-1?",
		WorkflowTriggerTool{accountId: "acct-down"}.ConfirmationQuestion(`{"id":"wf-1"}`))
	// A tool built without an account (not through the factory) does no lookup.
	assert.Equal(t, "Run the automation with ID wf-1?", WorkflowTriggerTool{}.ConfirmationQuestion(`{"id":" wf-1 "}`))
}
