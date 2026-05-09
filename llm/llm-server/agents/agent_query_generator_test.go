package agents

import (
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestQueryGeneratorAgent_GetSystemPrompt(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()
	testCases := []struct {
		name                    string
		fields                  []string
		supportedOperators      []string
		examples                []core.NBAgentPromptExample
		expectedRole            string
		expectedSchema          string
		expectedFields          string
		expectedOperators       string
		expectedOutput          string
		expectedNumExamples     int
		expectedExampleQuestion string
	}{
		{
			name:                "DefaultAgent",
			fields:              nil,
			supportedOperators:  nil,
			examples:            nil,
			expectedRole:        "Query generation expert in generating JSON query",
			expectedSchema:      "{\"where\": {\"<field>\": {\"<operator>\": \"<value>\"}}, \"_or\": [ ... ], \"_and\": [ ... ]}, \"limit\": <number>, \"time_range\": \"<string>\", \"start_time\": \"<string>\", \"index\": \"<string>\"}",
			expectedFields:      "  - **Fields**: ",
			expectedOperators:   "  - **Operators**: _eq, _neq, _gt, _gte, _lt, _lte, _in, _nin, _like, _ilike, _nlike, _is_null, _or, _and",
			expectedOutput:      "A single, valid JSON query object, enclosed in triple backticks.",
			expectedNumExamples: 8,
		},
		{
			name:               "CustomAgent",
			fields:             []string{"custom_field_1", "custom_field_2"},
			supportedOperators: []string{"_eq", "_like"},
			examples: []core.NBAgentPromptExample{
				{
					Question: "q1",
					Answer:   "a1",
				},
			},
			expectedRole:            "Query generation expert in generating JSON query",
			expectedSchema:          "{\"where\": {\"<field>\": {\"<operator>\": \"<value>\"}}, \"_or\": [ ... ], \"_and\": [ ... ]}, \"limit\": <number>, \"time_range\": \"<string>\", \"start_time\": \"<string>\", \"index\": \"<string>\"}",
			expectedFields:          "  - **Fields**: custom_field_1, custom_field_2",
			expectedOperators:       "  - **Operators**: _eq, _like",
			expectedOutput:          "A single, valid JSON query object, enclosed in triple backticks.",
			expectedNumExamples:     1,
			expectedExampleQuestion: "q1",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			agent := newQueryGeneratorAgent("test_account", tc.fields, tc.supportedOperators, tc.examples, nil)
			prompt := agent.GetSystemPrompt(sc, core.NBAgentRequest{})

			assert.Equal(t, tc.expectedRole, prompt.Role)
			assert.Contains(t, prompt.Instructions[4], tc.expectedSchema)
			assert.Contains(t, prompt.Instructions[11], tc.expectedFields)
			assert.Contains(t, prompt.Instructions[12], tc.expectedOperators)
			assert.Equal(t, tc.expectedOutput, prompt.OutputFormat)
			assert.Len(t, prompt.Examples, tc.expectedNumExamples)
			if tc.expectedExampleQuestion != "" {
				assert.Equal(t, tc.expectedExampleQuestion, prompt.Examples[0].Question)
			}
		})
	}
}

// TestQueryGeneratorAgent_LabelBiasGuardrails verifies the constraints added
// in #28517 are emitted whenever the caller injects a Fields list. The model
// has a strong prior toward `service_name` / OTel naming; without these
// guardrails it ignores the injected Loki labels (`app`, `namespace`, …) and
// emits queries that return no data, silently degrading to kubectl fallback.
func TestQueryGeneratorAgent_LabelBiasGuardrails(t *testing.T) {
	sc := security.NewRequestContextForSuperAdmin()

	t.Run("guardrails_present_when_fields_provided", func(t *testing.T) {
		agent := newQueryGeneratorAgent("acct", []string{"app", "namespace"}, nil, nil, nil)
		prompt := agent.GetSystemPrompt(sc, core.NBAgentRequest{})
		joined := strings.Join(prompt.Constraints, "\n")
		assert.Contains(t, joined, "MUST use ONLY the labels/fields listed in the Fields section")
		assert.Contains(t, joined, "service_name")
		assert.Contains(t, joined, "kubernetes.*")
		assert.Contains(t, strings.ToLower(joined), "do not invent labels")
	})

	t.Run("guardrails_absent_when_no_fields_provided", func(t *testing.T) {
		// When no fields are injected the agent has no way to authoritatively
		// say what's allowed, so we must not emit the "MUST use ONLY" line —
		// it would be vacuous and confusing.
		agent := newQueryGeneratorAgent("acct", nil, nil, nil, nil)
		prompt := agent.GetSystemPrompt(sc, core.NBAgentRequest{})
		joined := strings.Join(prompt.Constraints, "\n")
		assert.NotContains(t, joined, "MUST use ONLY the labels/fields listed")
	})
}

// TestLogDefault_LokiExamples_NoServiceNameBias guards against regression of
// the underlying cause of #28517: a single Loki provider example had
// `service_name` in its Answer JSON, which the model imitated for prompts
// whose natural English used the word "service". Now the only references to
// `service_name` in Loki examples should appear inside Explanations that
// teach the model to AVOID it.
func TestLogDefault_LokiExamples_NoServiceNameBias(t *testing.T) {
	agent := &LogDefaultAgent{}
	agent.provider.Provider = "loki"
	examples := agent.getProviderSpecificExamples()
	assert.NotEmpty(t, examples, "Loki provider examples must exist")

	for _, ex := range examples {
		assert.NotContains(t, ex.Answer, "service_name",
			"Loki example Answer must not use `service_name` (not a real Loki label) — "+
				"prefer `app`, `job`, or `container`. Question: %q", ex.Question)
	}
}

func TestQueryGeneratorAgent_E2E(t *testing.T) {
	accountId := os.Getenv("TEST_ACCOUNT")
	userId := os.Getenv("TEST_USER")
	fields := []string{"service_name", "http.status_code", "duration_ns", "severity", "body"}
	agent := newQueryGeneratorAgent(accountId, fields, nil, nil, nil)
	sc := security.NewRequestContextForSuperAdmin()

	testCases :=
		[]struct {
			SessionId string
			Query     string
		}{
			{
				SessionId: "ut-qg-chain-2",
				Query:     "show me recent 504 failures for services abc?",
			},
			{
				SessionId: "ut-qg-chain-4",
				Query:     "get logs of llm server where severity is error or info",
			},
			{
				SessionId: "ut-qg-chain-5",
				Query:     "get logs of llm server where severity is error or info or body contains llm",
			},
			{
				SessionId: "ut-qg-chain-6",
				Query:     "get 10 error logs of services-server",
			},
		}

	for _, tc := range testCases {
		err := core.DeleteConversationBySession(tc.SessionId, accountId, userId)
		assert.Nil(t, err)

		resp, err := core.HandleConversationSessionRequest(sc, agent, userId, accountId, tc.SessionId, tc.Query)
		assert.Nil(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, resp.AgentName, agent.GetName())
		assert.NotEmpty(t, resp.Query)
		assert.NotNil(t, resp.AgentStepResponse)
		assert.Greater(t, len(resp.Response), 0)
	}
}
