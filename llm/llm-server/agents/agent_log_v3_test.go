package agents

import (
	"os"
	"strings"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/config"
	"nudgebee/llm/security"
	"nudgebee/llm/services_server"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLogAgentV3_BuildToolList mirrors TestLogAgent_BuildToolList: the
// LLM-visible tool surface must be uniform across providers, and must expose
// fetch_logs_v3 (not the v1/v2 fetch_logs agent-as-tool) plus the plain
// resource_search_execute tool (not the resource_search agent-as-tool, which
// fans out to Datadog/cloud unnecessarily for a Kubernetes-only agent).
func TestLogAgentV3_BuildToolList(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()

	mustHave := []string{tools.ToolResourceSearch, FetchLogsV3ToolName, toolcore.ToolExecuteShellCommand}
	mustNotHave := []string{
		FetchLogsAgentName, // the v1/v2 agent-as-tool must not leak into v3's toolset
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
			agent := newLogAgentV3("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
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

// TestFetchLogsV3Tool_Registered ensures fetch_logs_v3 is in the system tool
// registry so LogAgentV3.GetSupportedTools can resolve it by name.
func TestFetchLogsV3Tool_Registered(t *testing.T) {
	tool, ok := toolcore.GetNBTool("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", FetchLogsV3ToolName)
	require.True(t, ok, "fetch_logs_v3 must be registered as a system tool")
	require.NotNil(t, tool)
	assert.Equal(t, FetchLogsV3ToolName, tool.Name())
	assert.Equal(t, toolcore.NBToolTypeTool, tool.GetType())
}

// TestLogAgentV3_Registered mirrors TestFetchLogsAgent_Registered for the
// top-level agent.
func TestLogAgentV3_Registered(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	agent, ok := core.GetNBAgent(ctx, LogsAgentV3Name, "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "")
	assert.True(t, ok, "logs_v3 must be registered as a system agent")
	assert.NotNil(t, agent)
	assert.Equal(t, LogsAgentV3Name, agent.GetName())
}

func TestLogAgentV3_RegisteredAsTool(t *testing.T) {
	tool, ok := toolcore.GetNBTool("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", LogsAgentV3Name)
	require.True(t, ok, "logs_v3 must be registered as a system tool so other agents can delegate to it")
	require.NotNil(t, tool)
	assert.Equal(t, LogsAgentV3Name, tool.Name())
}

func TestGetLogAgentV3(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	agent, err := getLogAgentV3(sc, os.Getenv("TEST_ACCOUNT"))
	assert.Nil(t, err)
	assert.NotNil(t, agent)
	assert.Equal(t, LogsAgentV3Name, agent.GetName())
}

// TestLogAgentV3_SystemPrompt_MatchesModeClassification confirms v3 reuses
// the same mode classifier and narrows the prompt to exactly one mode's
// workflow — same contract as LogAgent's TestSystemPrompt_NarrowsToOneMode,
// verified independently here since v3 has its own GetSystemPrompt body.
func TestLogAgentV3_SystemPrompt_MatchesModeClassification(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	agent := newLogAgentV3("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", services_server.ObservabilityProvider{Provider: "loki"})

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

func TestLogAgentV3_SystemPrompt_ReusesResolvedPodsAndExplainsArtifactFormat(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	agent := newLogAgentV3("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", services_server.ObservabilityProvider{Provider: "loki"})
	prompt := agent.GetSystemPrompt(ctx, core.NBAgentRequest{Query: "why is checkout failing"})
	body := strings.Join(prompt.Instructions, "\n")

	assert.Contains(t, body, "framework-generated `<resolved_targets>` block")
	assert.Contains(t, body, "A `candidate` target alone never suppresses discovery")
	assert.Contains(t, body, "Step 2a remains mandatory")
	assert.Contains(t, body, "logs_format_hint")
	assert.Contains(t, body, "never JSON-decode the whole file")
}

// TestLogAgentV3_FastPathAppAnchor_RoutineOnly pins the one point where v3's
// prompt diverges from v1's: the app-anchor fast-path override must appear
// for ROUTINE mode only, never for investigation/enumeration, so
// resource_search still runs first for those modes exactly as
// sharedHeaderAndWorkflow's step 1 requires.
func TestLogAgentV3_FastPathAppAnchor_RoutineOnly(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()
	agent := newLogAgentV3("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", services_server.ObservabilityProvider{Provider: "loki"})

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
				if strings.Contains(line, "logs_v3 refines workflow step 1c") {
					found = true
					break
				}
			}
			assert.Equal(t, tc.wantFound, found)
		})
	}
}

// TestBuildFetchLogsV3ToolResponse pins the NBAgentResponse -> NBToolResponse
// mapping fetchLogsV3Tool.Call() delegates to. In particular: a FAILED/
// TERMINATED/WAITING terminal status must propagate to the tool response
// status instead of being silently reported as success (the A2/A3 bug class
// from the v1 bespoke wrapper — see log_analysis_bugs notes).
func TestBuildFetchLogsV3ToolResponse(t *testing.T) {
	t.Run("Execute error maps to ERROR status and propagates the error", func(t *testing.T) {
		resp, err := buildFetchLogsV3ToolResponse(core.NBAgentResponse{}, assert.AnError)
		assert.ErrorIs(t, err, assert.AnError)
		assert.Equal(t, toolcore.NBToolResponseStatusError, resp.Status)
	})

	t.Run("ConversationStatusFailed maps to ERROR status with no Go error", func(t *testing.T) {
		agentResp := core.NBAgentResponse{Status: core.ConversationStatusFailed, Response: []string{"kubectl fetch failed: NotFound"}}
		resp, err := buildFetchLogsV3ToolResponse(agentResp, nil)
		require.NoError(t, err)
		assert.Equal(t, toolcore.NBToolResponseStatusError, resp.Status)
		assert.Equal(t, "kubectl fetch failed: NotFound", resp.Data)
	})

	t.Run("ConversationStatusTerminated propagates", func(t *testing.T) {
		agentResp := core.NBAgentResponse{Status: core.ConversationStatusTerminated}
		resp, err := buildFetchLogsV3ToolResponse(agentResp, nil)
		require.NoError(t, err)
		assert.Equal(t, toolcore.NBToolResponseStatusTerminated, resp.Status)
	})

	t.Run("ConversationStatusWaiting propagates", func(t *testing.T) {
		agentResp := core.NBAgentResponse{Status: core.ConversationStatusWaiting}
		resp, err := buildFetchLogsV3ToolResponse(agentResp, nil)
		require.NoError(t, err)
		assert.Equal(t, toolcore.NBToolResponseStatusWaiting, resp.Status)
	})

	t.Run("ConversationStatusCompleted maps to SUCCESS with data and references", func(t *testing.T) {
		agentResp := core.NBAgentResponse{
			Status:     core.ConversationStatusCompleted,
			Response:   []string{`{"query":"{app=\"x\"}","logs":"...","file_ref":"logs_loki_1.txt"}`},
			References: []toolcore.NBToolResponseReference{{Text: "logs_loki_1.txt", Url: "logs_loki_1.txt", Type: "file"}},
		}
		resp, err := buildFetchLogsV3ToolResponse(agentResp, nil)
		require.NoError(t, err)
		assert.Equal(t, toolcore.NBToolResponseStatusSuccess, resp.Status)
		assert.Equal(t, toolcore.NBToolResponseTypeJson, resp.Type)
		assert.Contains(t, resp.Data, "logs_loki_1.txt")
		require.Len(t, resp.References, 1)
		assert.Equal(t, "logs_loki_1.txt", resp.References[0].Url)
	})

	t.Run("empty Response does not panic, yields empty data", func(t *testing.T) {
		resp, err := buildFetchLogsV3ToolResponse(core.NBAgentResponse{Status: core.ConversationStatusCompleted}, nil)
		require.NoError(t, err)
		assert.Empty(t, resp.Data)
		assert.Equal(t, toolcore.NBToolResponseStatusSuccess, resp.Status)
	})
}

// buildLogIntentMessages depends on OriginalQuery/AccountPrompt reaching the
// translator call; this guards against silently dropping them again.
func TestBuildFetchLogsV3Request_PropagatesOriginalQueryAndAccountPrompt(t *testing.T) {
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

	request := buildFetchLogsV3Request(nbCtx, input)

	assert.Equal(t, "why is checkout-service throwing 500s", request.OriginalQuery)
	assert.Equal(t, "cluster is prod-us-east-1", request.AccountContext)
	assert.Equal(t, "log field for pod name is k8s_pod_name, not pod", request.AccountPrompt)
	assert.Equal(t, "get recent logs for checkout-service", request.Query)
	assert.Equal(t, "parent-agent-1", request.AgentId)
	assert.Equal(t, "parent-agent-1", request.ParentAgentId)
}

// TestFetchLogsV3Tool_InputSchema pins the tool's LLM-facing contract: a
// single required "command" string, matching the calling convention
// LogAgentV3's prompt (labelAnchorRules, sharedHeaderAndWorkflow) teaches.
func TestFetchLogsV3Tool_InputSchema(t *testing.T) {
	tool := &fetchLogsV3Tool{accountId: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
	schema := tool.InputSchema()
	assert.Equal(t, toolcore.ToolSchemaTypeObject, schema.Type)
	assert.Contains(t, schema.Properties, "command")
	assert.Equal(t, []string{"command"}, schema.Required)
}

// TestPreBuiltCanonicalQuery pins the detection rule fetchLogsV3Tool.Call uses
// to decide whether the model already supplied a ready-to-execute canonical
// query (skip the internal translation call) versus a natural-language
// question (unchanged path) — a natural-language command is never valid JSON
// with a top-level "where" key, so that shape is the unambiguous signal.
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

// TestCanonicalQueryAuthoringForRoutine checks the ROUTINE-mode fast-path
// instructions carry the same correctness-critical rules
// buildCanonicalLogQueryPrompt enforces for the internal translator (field
// names come only from the advertised list, namespace vs workload isn't
// conflated) — this prompt is the only guardrail once the model is trusted to
// produce canonical JSON directly.
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

	assert.Contains(t, out, "not an optional shortcut", "must state canonical JSON is the ROUTINE-mode default, not a hedged suggestion — hedged phrasing lost to the tool description's NL framing in live testing")
	assert.Contains(t, out, "app → deployment.keyword", "must advertise this account's real canonical mapping, not a generic placeholder")
	assert.Contains(t, out, "namespace → k8s.namespace.keyword")
	assert.Contains(t, out, "never invent a field name")
	assert.Contains(t, out, "not fully confident", "must tell the model it can still fall back to natural language")
}

func TestCanonicalQueryAuthoringForRoutine_NoLabelMappings_StillProducesGuidance(t *testing.T) {
	provider := services_server.ObservabilityProvider{Provider: "loki"}
	out := canonicalQueryAuthoringForRoutine("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", provider)
	assert.Contains(t, out, "not an optional shortcut")
	assert.NotContains(t, out, "Canonical fields for this backend", "must not claim a canonical mapping when the provider has none")
}

// TestLogsV3CanonicalFastPathFlag_GatesRoutinePromptContent covers
// LogsV3CanonicalFastPathEnabled (config/config.go): when false, ROUTINE
// mode's prompt must reproduce the original NL-only wording exactly — no
// mention of canonical JSON anywhere, since a stray mention with the code
// path disabled would tell the model to do something the tool can't
// fast-path. When true (the default), the fast-path wording must be present.
func TestLogsV3CanonicalFastPathFlag_GatesRoutinePromptContent(t *testing.T) {
	orig := config.Config.LogsV3CanonicalFastPathEnabled
	t.Cleanup(func() { config.Config.LogsV3CanonicalFastPathEnabled = orig })

	agent := newLogAgentV3("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", services_server.ObservabilityProvider{Provider: "loki"})
	ctx := security.NewRequestContextForSuperAdmin()

	t.Run("disabled: no canonical-JSON mention anywhere in ROUTINE prompt", func(t *testing.T) {
		config.Config.LogsV3CanonicalFastPathEnabled = false
		prompt := agent.GetSystemPrompt(ctx, core.NBAgentRequest{Query: "recent logs for relay-server"})
		full := strings.Join(prompt.Instructions, "\n")
		assert.NotContains(t, full, "canonical JSON")
		assert.NotContains(t, full, "fast path")
		assert.Contains(t, full, "Phrase the NL question using the right label", "must fall back to v1's exact original step-3 wording")
	})

	t.Run("enabled: canonical-JSON fast path present in ROUTINE prompt", func(t *testing.T) {
		config.Config.LogsV3CanonicalFastPathEnabled = true
		prompt := agent.GetSystemPrompt(ctx, core.NBAgentRequest{Query: "recent logs for relay-server"})
		full := strings.Join(prompt.Instructions, "\n")
		assert.Contains(t, full, "canonical JSON")
		assert.Contains(t, full, "not an optional shortcut")
	})
}

// TestLogsV3CanonicalFastPathFlag_GatesToolBehavior covers the code-path side
// of the same flag: disabled must make fetchLogsV3Tool.Call ignore a
// canonical-JSON tool_input entirely (never call ExecuteCanonicalDirect) and
// Description/InputSchema must drop back to NL-only wording — defense in
// depth so a stray canonical JSON in conversation history/memory can't
// resurrect the fast path once it's supposed to be off.
func TestLogsV3CanonicalFastPathFlag_GatesToolBehavior(t *testing.T) {
	orig := config.Config.LogsV3CanonicalFastPathEnabled
	t.Cleanup(func() { config.Config.LogsV3CanonicalFastPathEnabled = orig })

	tool := &fetchLogsV3Tool{accountId: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}

	config.Config.LogsV3CanonicalFastPathEnabled = false
	assert.NotContains(t, tool.Description(), "canonical JSON")
	schema := tool.InputSchema()
	assert.NotContains(t, schema.Properties["command"].Description, "canonical JSON")

	config.Config.LogsV3CanonicalFastPathEnabled = true
	assert.Contains(t, tool.Description(), "canonical JSON")
	schema = tool.InputSchema()
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
