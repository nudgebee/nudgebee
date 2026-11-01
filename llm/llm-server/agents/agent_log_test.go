package agents

import (
	"os"
	"strings"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tmc/langchaingo/llms"
)

// TestLogAgent_BuildToolList: the LLM-visible tool surface must be uniform
// across providers, and must expose fetch_logs_direct (not the v1/v2
// fetch_logs agent-as-tool) plus the plain resource_search_execute tool (not
// the resource_search agent-as-tool, which fans out to Datadog/cloud
// unnecessarily for a Kubernetes-only agent).
func TestLogAgent_BuildToolList(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()

	mustHave := []string{tools.ToolResourceSearch, FetchLogsDirectToolName, toolcore.ToolExecuteShellCommand}
	mustNotHave := []string{
		FetchLogsAgentName, // the v1/v2 agent-as-tool must not leak into this agent's toolset
		"query_generator",
		"datadog_log_query",
		"kubectl_intent_generator",
		"kubectl_execute",
	}

	cases := []struct {
		name     string
		provider string
	}{
		{"loki provider", "loki"},
		{"signoz provider", "signoz"},
		{"es provider", "es"},
		{"datadog provider", "datadog"},
		{"no provider falls back to kubectl-only path", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agent := newLogAgent("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
				services_server.ObservabilityProvider{Provider: tc.provider})
			names := toolNamesForTest(agent.GetSupportedTools(ctx))

			for _, expected := range mustHave {
				assert.Contains(t, names, expected,
					"provider %q must expose %q", tc.provider, expected)
			}
			for _, forbidden := range mustNotHave {
				assert.NotContains(t, names, forbidden,
					"provider %q must NOT expose %q", tc.provider, forbidden)
			}
		})
	}
}

// toolNamesForTest extracts plain tool names from a tool list, so a test can
// lock a resource-search split without standing up a populated tool registry.
func toolNamesForTest(ts []toolcore.NBTool) []string {
	out := make([]string, 0, len(ts))
	for _, t := range ts {
		out = append(out, strings.TrimSpace(t.Name()))
	}
	return out
}

// TestFetchLogsDirectTool_Registered ensures fetch_logs_direct is in the system tool
// registry so LogAgent.GetSupportedTools can resolve it by name.
func TestFetchLogsDirectTool_Registered(t *testing.T) {
	tool, ok := toolcore.GetNBTool("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", FetchLogsDirectToolName)
	require.True(t, ok, "fetch_logs_direct must be registered as a system tool")
	require.NotNil(t, tool)
	assert.Equal(t, FetchLogsDirectToolName, tool.Name())
	assert.Equal(t, toolcore.NBToolTypeTool, tool.GetType())
}

// TestFetchLogsV3Alias_ResolvesToFetchLogsDirect guards the back-compat alias:
// stored conversation history referencing the pre-rename tool name must keep
// resolving to the same tool as fetch_logs_direct.
func TestFetchLogsV3Alias_ResolvesToFetchLogsDirect(t *testing.T) {
	tool, ok := toolcore.GetNBTool("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "fetch_logs_v3")
	require.True(t, ok, "fetch_logs_v3 alias must still resolve")
	require.NotNil(t, tool)
	assert.Equal(t, FetchLogsDirectToolName, tool.Name())
}

// TestLogAgent_Registered mirrors TestFetchLogsAgent_Registered for the
// top-level agent.
func TestLogAgent_Registered(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	agent, ok := core.GetNBAgent(ctx, LogsAgentName, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "")
	assert.True(t, ok, "logs must be registered as a system agent")
	assert.NotNil(t, agent)
	assert.Equal(t, LogsAgentName, agent.GetName())
}

func TestLogAgent_RegisteredAsTool(t *testing.T) {
	tool, ok := toolcore.GetNBTool("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", LogsAgentName)
	require.True(t, ok, "logs must be registered as a system tool so other agents can delegate to it")
	require.NotNil(t, tool)
	assert.Equal(t, LogsAgentName, tool.Name())
}

func TestGetLogAgent(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	agent, err := getLogAgent(sc, os.Getenv("TEST_ACCOUNT"))
	assert.Nil(t, err)
	assert.NotNil(t, agent)
	assert.Equal(t, LogsAgentName, agent.GetName())
}

// TestLogAgent_SystemPrompt_MatchesModeClassification confirms the mode
// classifier narrows the prompt to exactly one mode's workflow.
func TestLogAgent_SystemPrompt_MatchesModeClassification(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	agent := newLogAgent("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", services_server.ObservabilityProvider{Provider: "loki"})

	cases := []struct {
		name     string
		query    string
		wantMode string
	}{
		{"investigation wording", "why is checkout failing", "INVESTIGATION"},
		{"enumeration wording", "list all distinct errors in checkout", "ENUMERATION"},
		{"routine wording", "show me recent logs for checkout", "ROUTINE"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prompt := agent.GetSystemPrompt(ctx, core.NBAgentRequest{Query: tc.query})
			require.NotEmpty(t, prompt.Instructions)
			assert.Contains(t, prompt.Instructions[0], "MODE = "+tc.wantMode)
		})
	}
}

// The original question is per-request text (an event investigation carries the
// whole event payload). Everything before it must be identical across requests
// in the same mode so the provider can reuse the cached prompt across runs.
func TestLogAgent_SystemPrompt_OriginalQuestionIsLast(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	agent := newLogAgent("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", services_server.ObservabilityProvider{Provider: "loki"})
	req := func(orig string) core.NBAgentRequest {
		return core.NBAgentRequest{Query: "find errors in checkout logs", OriginalQuery: orig, ParentAgentId: "11111111-1111-1111-1111-111111111111", AgentId: "22222222-2222-2222-2222-222222222222"}
	}
	a := agent.GetSystemPrompt(ctx, req("Identify RootCause of the event with id 1: checkout 5xx spike at 06:02 UTC"))
	b := agent.GetSystemPrompt(ctx, req("Identify RootCause of the event with id 2: payments OOMKilled at 07:15 UTC"))

	require.NotEmpty(t, a.Instructions)
	assert.Contains(t, a.Instructions[len(a.Instructions)-1], "**Original user question:**")
	assert.Contains(t, a.Instructions[len(a.Instructions)-1], "checkout 5xx spike")
	assert.Equal(t, a.Instructions[:len(a.Instructions)-1], b.Instructions[:len(b.Instructions)-1], "everything before the original question is request-independent")
	assert.Equal(t, a.Constraints, b.Constraints)
	assert.Equal(t, a.Role, b.Role)

	// The rendered system prompt (what the provider sees) must also share
	// everything up to the original question.
	ra := core.GetPromptTemplate(a, req(""), core.AgentPlannerTypeReAct).Template
	rb := core.GetPromptTemplate(b, req(""), core.AgentPlannerTypeReAct).Template
	q := strings.Index(ra, "**Original user question:**")
	require.Positive(t, q)
	assert.Equal(t, ra[:q], rb[:q], "rendered prompt is identical up to the original question")
	assert.Greater(t, q, len(ra)*3/4, "the per-request text sits in the last part of the rendered prompt")

	same := agent.GetSystemPrompt(ctx, core.NBAgentRequest{Query: "find errors in checkout logs", OriginalQuery: "find errors in checkout logs"})
	assert.NotContains(t, strings.Join(same.Instructions, "\n"), "**Original user question:**", "not repeated when it equals the query")
}

func TestLogAgent_SystemPrompt_ReusesResolvedPodsAndExplainsArtifactFormat(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	agent := newLogAgent("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", services_server.ObservabilityProvider{Provider: "loki"})
	prompt := agent.GetSystemPrompt(ctx, core.NBAgentRequest{Query: "why is checkout failing"})
	body := strings.Join(prompt.Instructions, "\n")

	assert.Contains(t, body, "framework-generated `<resolved_targets>` block")
	assert.Contains(t, body, "A `candidate` target alone never suppresses discovery")
	assert.Contains(t, body, "Step 2a remains mandatory")
	assert.Contains(t, body, "logs_format_hint")
	assert.Contains(t, body, "never JSON-decode the whole file")
	assert.Contains(t, body, "kubectl_disclosure")
	assert.Contains(t, body, "read_coverage")
	assert.Contains(t, body, "all fetched lines are shown above", "the prompt quotes the marker the fetch response emits")
}

// TestLogAgent_FastPathAppAnchor_RoutineOnly pins that the app-anchor
// canonical-query guidance appears for ROUTINE mode only, never for
// investigation/enumeration, so resource_search still runs first for those
// modes exactly as sharedHeaderAndWorkflow's step 1 requires.
func TestLogAgent_FastPathAppAnchor_RoutineOnly(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	agent := newLogAgent("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", services_server.ObservabilityProvider{Provider: "loki"})

	cases := []struct {
		name      string
		query     string
		wantFound bool
	}{
		{"routine wording gets the override", "get me logs for relay_server", true},
		{"investigation wording does not", "why is relay_server failing", false},
		{"enumeration wording does not", "list all distinct errors in relay_server", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prompt := agent.GetSystemPrompt(ctx, core.NBAgentRequest{Query: tc.query})
			found := false
			for _, line := range prompt.Instructions {
				if strings.Contains(line, "This refines workflow step 1c") {
					found = true
					break
				}
			}
			assert.Equal(t, tc.wantFound, found)
		})
	}
}

// TestBuildFetchLogsDirectToolResponse pins the NBAgentResponse -> NBToolResponse
// mapping fetchLogsDirectTool.Call() delegates to. In particular: a FAILED/
// TERMINATED/WAITING terminal status must propagate to the tool response
// status instead of being silently reported as success (the A2/A3 bug class
// from the v1 bespoke wrapper — see log_analysis_bugs notes).
func TestBuildFetchLogsDirectToolResponse(t *testing.T) {
	t.Run("Execute error maps to ERROR status and propagates the error", func(t *testing.T) {
		resp, err := buildFetchLogsDirectToolResponse(core.NBAgentResponse{}, assert.AnError)
		assert.ErrorIs(t, err, assert.AnError)
		assert.Equal(t, toolcore.NBToolResponseStatusError, resp.Status)
	})

	t.Run("ConversationStatusFailed maps to ERROR status with no Go error", func(t *testing.T) {
		agentResp := core.NBAgentResponse{Status: core.ConversationStatusFailed, Response: []string{"kubectl fetch failed: NotFound"}}
		resp, err := buildFetchLogsDirectToolResponse(agentResp, nil)
		require.NoError(t, err)
		assert.Equal(t, toolcore.NBToolResponseStatusError, resp.Status)
		assert.Equal(t, "kubectl fetch failed: NotFound", resp.Data)
	})

	t.Run("ConversationStatusTerminated propagates", func(t *testing.T) {
		agentResp := core.NBAgentResponse{Status: core.ConversationStatusTerminated}
		resp, err := buildFetchLogsDirectToolResponse(agentResp, nil)
		require.NoError(t, err)
		assert.Equal(t, toolcore.NBToolResponseStatusTerminated, resp.Status)
	})

	t.Run("ConversationStatusWaiting propagates", func(t *testing.T) {
		agentResp := core.NBAgentResponse{Status: core.ConversationStatusWaiting}
		resp, err := buildFetchLogsDirectToolResponse(agentResp, nil)
		require.NoError(t, err)
		assert.Equal(t, toolcore.NBToolResponseStatusWaiting, resp.Status)
	})

	t.Run("ConversationStatusCompleted maps to SUCCESS with data and references", func(t *testing.T) {
		agentResp := core.NBAgentResponse{
			Status:     core.ConversationStatusCompleted,
			Response:   []string{`{"query":"{app=\"x\"}","logs":"...","file_ref":"logs_loki_1.txt"}`},
			References: []toolcore.NBToolResponseReference{{Text: "logs_loki_1.txt", Url: "logs_loki_1.txt", Type: "file"}},
		}
		resp, err := buildFetchLogsDirectToolResponse(agentResp, nil)
		require.NoError(t, err)
		assert.Equal(t, toolcore.NBToolResponseStatusSuccess, resp.Status)
		assert.Equal(t, toolcore.NBToolResponseTypeJson, resp.Type)
		assert.Contains(t, resp.Data, "logs_loki_1.txt")
		require.Len(t, resp.References, 1)
		assert.Equal(t, "logs_loki_1.txt", resp.References[0].Url)
	})

	t.Run("empty Response does not panic, yields empty data", func(t *testing.T) {
		resp, err := buildFetchLogsDirectToolResponse(core.NBAgentResponse{Status: core.ConversationStatusCompleted}, nil)
		require.NoError(t, err)
		assert.Empty(t, resp.Data)
		assert.Equal(t, toolcore.NBToolResponseStatusSuccess, resp.Status)
	})
}

// buildLogIntentMessages depends on OriginalQuery/AccountPrompt reaching the
// translator call; this guards against silently dropping them again.
func TestBuildFetchLogsDirectRequest_PropagatesOriginalQueryAndAccountPrompt(t *testing.T) {
	nbCtx := toolcore.NbToolContext{
		AccountId:      "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		ConversationId: "conv-1",
		ParentAgentId:  "parent-agent-1",
		MessageId:      "msg-1",
		UserId:         "user-1",
		SessionId:      "session-1",
		OriginalQuery:  "why is checkout-service throwing 500s",
		AccountContext: "cluster is prod-us-east-1",
		AccountPrompt:  "log field for pod name is k8s_pod_name, not pod",
	}
	input := toolcore.NBToolCallRequest{Command: "get recent logs for checkout-service"}

	request := buildFetchLogsDirectRequest(nbCtx, input)

	assert.Equal(t, "why is checkout-service throwing 500s", request.OriginalQuery)
	assert.Equal(t, "cluster is prod-us-east-1", request.AccountContext)
	assert.Equal(t, "log field for pod name is k8s_pod_name, not pod", request.AccountPrompt)
	assert.Equal(t, "get recent logs for checkout-service", request.Query)
	assert.Equal(t, "parent-agent-1", request.AgentId)
	assert.Equal(t, "parent-agent-1", request.ParentAgentId)
}

// TestFetchLogsDirectTool_InputSchema pins the tool's LLM-facing contract: a
// single required "command" string, matching the calling convention
// LogAgent's prompt (labelAnchorRules, sharedHeaderAndWorkflow) teaches.
func TestFetchLogsDirectTool_InputSchema(t *testing.T) {
	tool := &fetchLogsDirectTool{accountId: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
	schema := tool.InputSchema()
	assert.Equal(t, toolcore.ToolSchemaTypeObject, schema.Type)
	assert.Contains(t, schema.Properties, "command")
	assert.Contains(t, schema.Properties, "knowledge_context")
	assert.Equal(t, []string{"command"}, schema.Required)
}

// TestPreBuiltCanonicalQuery pins the canonical shape check used to validate
// planner output. A natural-language command is never valid JSON with a
// top-level "where" key.
func TestPreBuiltCanonicalQuery(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantOK  bool
		wantOut string
	}{
		{
			name:   "natural language command",
			input:  "all logs for app relay-server in namespace nudgebee",
			wantOK: false,
		},
		{
			name:    "canonical JSON with where clause",
			input:   `{"where": {"app": {"_eq": "relay-server"}}, "time_range": "30m", "limit": 1000}`,
			wantOK:  true,
			wantOut: `{"where": {"app": {"_eq": "relay-server"}}, "time_range": "30m", "limit": 1000}`,
		},
		{
			name:   "JSON object without a where key",
			input:  `{"time_range": "30m", "limit": 1000}`,
			wantOK: false,
		},
		{
			name:   "malformed JSON",
			input:  `{"where": {"app": {"_eq": "relay-server"}}`,
			wantOK: false,
		},
		{
			name:   "empty string",
			input:  "",
			wantOK: false,
		},
		{
			name:    "leading/trailing whitespace is trimmed",
			input:   "  " + `{"where": {"app": {"_eq": "relay-server"}}}` + "  ",
			wantOK:  true,
			wantOut: `{"where": {"app": {"_eq": "relay-server"}}}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, ok := preBuiltCanonicalQuery(tc.input)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				assert.Equal(t, tc.wantOut, out)
			}
		})
	}
}

// TestCanonicalQueryAuthoringForRoutine checks the ROUTINE-mode canonical-query
// instructions carry the same correctness-critical rules
// buildCanonicalLogQueryPrompt enforces for the internal translator (field
// names come only from the advertised list, namespace vs workload isn't
// conflated); the unified planner also validates the generated query.
func TestCanonicalQueryAuthoringForRoutine(t *testing.T) {
	provider := services_server.ObservabilityProvider{
		Provider: "loki",
		Capabilities: services_server.ProviderCapabilities{
			LabelMappings: map[string]string{
				"app":       "deployment.keyword",
				"namespace": "k8s.namespace.keyword",
			},
		},
	}
	out := canonicalQueryAuthoringForRoutine("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", provider)

	assert.Contains(t, out, "this is the default for ROUTINE mode", "must state canonical JSON is the ROUTINE-mode default, not a hedged suggestion — hedged phrasing lost to the tool description's NL framing in live testing")
	assert.Contains(t, out, "unified query-planning call")
	assert.Contains(t, out, "app → deployment.keyword", "must advertise this account's real canonical mapping, not a generic placeholder")
	assert.Contains(t, out, "namespace → k8s.namespace.keyword")
	assert.Contains(t, out, "never invent a field name")
	assert.Contains(t, out, "not fully confident", "must tell the model it can still fall back to natural language")
}

func TestCanonicalQueryAuthoringForRoutine_NoLabelMappings_StillProducesGuidance(t *testing.T) {
	provider := services_server.ObservabilityProvider{Provider: "loki"}
	out := canonicalQueryAuthoringForRoutine("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", provider)
	assert.Contains(t, out, "this is the default for ROUTINE mode")
	assert.NotContains(t, out, "Canonical fields for this backend", "must not claim a canonical mapping when the provider has none")
}

// TestRoutinePromptAdvertisesCanonicalAndNativeQueries keeps the agent's
// instructions aligned with the unified planner's accepted inputs.
func TestRoutinePromptAdvertisesCanonicalAndNativeQueries(t *testing.T) {
	agent := newLogAgent("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", services_server.ObservabilityProvider{Provider: "loki"})
	ctx := security.NewRequestContextForSuperAdmin()
	prompt := agent.GetSystemPrompt(ctx, core.NBAgentRequest{Query: "recent logs for relay-server"})
	full := strings.Join(prompt.Instructions, "\n")
	assert.Contains(t, full, "canonical JSON")
	assert.Contains(t, full, "this is the default for ROUTINE mode")
	assert.Contains(t, full, "complete query matching the account's default log provider")
}

// TestFetchLogsDirectToolDescriptionsAdvertiseAllQueryForms verifies that the
// tool schema matches the unified planner's accepted inputs.
func TestFetchLogsDirectToolDescriptionsAdvertiseAllQueryForms(t *testing.T) {
	tool := &fetchLogsDirectTool{accountId: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
	assert.Contains(t, tool.Description(), "provider-native query")
	assert.Contains(t, tool.Description(), "canonical JSON")
	schema := tool.InputSchema()
	assert.Contains(t, schema.Properties["command"].Description, "provider-native query")
	assert.Contains(t, schema.Properties["command"].Description, "canonical JSON")
}

// TestBuildCanonicalLogQueryPromptV3 mirrors TestBuildCanonicalLogQueryPrompt
// (agent_log_fetch_v2_test.go) — same correctness-critical assertions,
// confirming the v3 copy didn't drop anything from the shared original —
// plus the duplicateLabelHeuristicHint addition this copy exists for.
func TestBuildCanonicalLogQueryPromptV3(t *testing.T) {
	t.Run("sparse label_mappings (this account's real shape): hint appears alongside the canonical-fields fallback list", func(t *testing.T) {
		provider := services_server.ObservabilityProvider{
			Provider: "loki",
			Capabilities: services_server.ProviderCapabilities{
				SupportedOperators: []string{"_eq", "_neq", "_ilike", "_contains"},
				LabelMappings:      map[string]string{"content": "log"},
			},
		}
		p := buildCanonicalLogQueryPromptV3(provider, []string{"app", "namespace", "pod", "k8s_deployment_name", "k8s_namespace_name", "k8s_pod_name"}, nil)

		assert.Contains(t, p, "Canonical fields for THIS backend")
		assert.Contains(t, p, "content → log")
		assert.Contains(t, p, "Backend labels (use ONLY when NO canonical_name above fits the concept)")
		assert.Contains(t, p, "prefer the SHORT Kubernetes-native form", "the sparse-mapping fallback list must carry the ambiguity heuristic")
		assert.Contains(t, p, "k8s_deployment_name")
	})

	t.Run("no label_mappings at all: hint appears alongside the AVAILABLE FIELDS list", func(t *testing.T) {
		provider := services_server.ObservabilityProvider{Provider: "loki"}
		p := buildCanonicalLogQueryPromptV3(provider, []string{"app", "k8s_deployment_name"}, nil)

		assert.Contains(t, p, "AVAILABLE FIELDS for query building")
		assert.Contains(t, p, "prefer the SHORT Kubernetes-native form")
	})

	t.Run("preserves the shared original's correctness-critical rules", func(t *testing.T) {
		provider := services_server.ObservabilityProvider{
			Provider: "loki",
			Capabilities: services_server.ProviderCapabilities{
				SupportedOperators: []string{"_eq", "_neq", "_ilike", "_like"},
				LabelMappings: map[string]string{
					"app": "app", "namespace": "namespace", "pod": "pod",
					"container": "container", "content": "log",
				},
			},
		}
		p := buildCanonicalLogQueryPromptV3(provider, []string{"app", "namespace", "pod", "container", "level"}, nil)

		assert.Contains(t, p, "do NOT emit `service_name`")
		assert.Contains(t, p, "A window in the question is a HARD constraint")
		assert.Contains(t, p, "NEVER widen or shrink a window")
		assert.Contains(t, p, "values come from the QUESTION, never from the examples")
		assert.Contains(t, p, "VERBATIM")
		assert.Contains(t, p, "_ilike")
	})
}

// The parent owns knowledge loading. Its chosen constraints must reach the leaf
// unchanged, whether it authors canonical JSON or asks the translator to do so.
// This does not assert that a live model selects the right knowledge guidance.
func TestKnowledgeDerivedCommandHandoff(t *testing.T) {
	for _, command := range []string{
		"Read index checkout-preprod for host preprod-01 from 2026-09-06T10:00:00Z to 2026-09-06T10:15:00Z; filter service=checkout",
		`{"where":{"host":{"eq":"preprod-01"}},"index":"checkout-preprod","start_time":"2026-09-06T10:00:00Z","end_time":"2026-09-06T10:15:00Z","limit":200}`,
	} {
		t.Run(command, func(t *testing.T) {
			ctx := toolcore.NbToolContext{AccountId: "preprod-account", OriginalQuery: "Investigate checkout in PreProd", QueryContext: "environment=PreProd"}
			request := buildFetchLogsDirectRequest(ctx, toolcore.NBToolCallRequest{Command: command})
			require.Equal(t, command, request.Query)
			require.Equal(t, ctx.AccountId, request.AccountId)
			require.Equal(t, ctx.QueryContext, request.QueryContext)
			canonical, fast := preBuiltCanonicalQuery(request.Query)
			if strings.HasPrefix(command, "{") {
				require.True(t, fast)
				require.Equal(t, command, canonical)
			} else {
				require.False(t, fast)
				messages := buildLogIntentMessages("translator", request)
				var text strings.Builder
				for _, part := range messages[len(messages)-1].Parts {
					if content, ok := part.(llms.TextContent); ok {
						text.WriteString(content.Text)
					}
				}
				require.Contains(t, text.String(), command)
				require.Contains(t, text.String(), ctx.OriginalQuery)
			}
		})
	}
}

// Delegated scope must reach the translator used by both the primary and
// kubectl fallback paths, not just remain on the request for tool execution.
func TestDelegatedContextReachesTranslator(t *testing.T) {
	scope := "Use namespace payments-preprod and host preprod-01; last 15m limit 25"
	request := buildFetchLogsDirectRequest(toolcore.NbToolContext{QueryContext: scope}, toolcore.NBToolCallRequest{Command: "fetch checkout logs"})
	messages := buildLogIntentMessages("translator", request)
	var human strings.Builder
	for _, part := range messages[len(messages)-1].Parts {
		if content, ok := part.(llms.TextContent); ok {
			human.WriteString(content.Text)
		}
	}
	require.Contains(t, human.String(), scope)
	require.Contains(t, human.String(), "fetch checkout logs")
	require.Empty(t, request.KBPrestepContent)
	require.Empty(t, request.SkillsContext)
}
