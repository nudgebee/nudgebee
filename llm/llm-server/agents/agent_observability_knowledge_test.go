package agents

import (
	"github.com/stretchr/testify/require"
	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"testing"
)

// Exercise actual wrapper dispatch rather than reconstructing its context in a test.
// Provider execution is intercepted before network/LLM access.
func TestObservabilityKnowledgeHandoff(t *testing.T) {
	for _, policy := range []core.KnowledgePolicy{core.KnowledgeAuto, core.KnowledgeAlways, core.KnowledgeLLMOnly, core.KnowledgeDisabled} {
		for _, kind := range []string{"metrics", "traces"} {
			t.Run(kind+"/"+string(policy), func(t *testing.T) {
				request := core.NBAgentRequest{
					Query:         "checkout in PreProd from 10:00 to 10:15 UTC",
					QueryContext:  "host=preprod-01; index=checkout-preprod",
					OriginalQuery: "Investigate checkout failures in PreProd from 10:00 to 10:15 UTC",
					AccountId:     "account", ConversationId: "conversation", MessageId: "message",
					KnowledgePolicy: policy, KnowledgePolicyResolved: true,
					InheritSkillsFromAgents: []string{"k8s_orchestrator"},
					SelectedSkillIds:        []string{"kb-id"},
				}
				calls := 0
				dispatch := func(ctx toolcore.NbToolContext, _ core.NBAgent, input toolcore.NBToolCallRequest) (core.NBAgentResponse, error) {
					calls++
					require.Equal(t, string(policy), ctx.KnowledgePolicy)
					require.True(t, ctx.KnowledgePolicyResolved, "child must not repeat policy lookup")
					require.Equal(t, request.OriginalQuery, ctx.OriginalQuery)
					require.Equal(t, request.SelectedSkillIds, ctx.SelectedSkillIds)
					require.Equal(t, []string{"k8s_orchestrator", kind}, ctx.InheritSkillsFromAgents)
					require.Equal(t, request.Query, input.Command)
					require.Equal(t, request.QueryContext, input.Context)
					require.Equal(t, request.AccountId, ctx.AccountId)
					if kind == "traces" && calls == 1 {
						return core.NBAgentResponse{Status: core.ConversationStatusCompleted, Response: []string{"No traces found"}}, nil
					}
					return core.NBAgentResponse{Status: core.ConversationStatusCompleted, Response: []string{"observed matching data"}}, nil
				}
				ctx := security.NewRequestContextForSuperAdmin()
				if kind == "metrics" {
					agent := &metricsAgent{accountId: "account", agent: &mockTracesSubAgent{name: "provider"}, executor: dispatch}
					_, err := agent.Execute(ctx, request)
					require.NoError(t, err)
					require.Equal(t, 1, calls)
				} else {
					agent := &fallbackTracesAgent{accountId: "account", agents: []core.NBAgent{&mockTracesSubAgent{name: "primary"}, &mockTracesSubAgent{name: "fallback"}}, executor: dispatch}
					_, err := agent.Execute(ctx, request)
					require.NoError(t, err)
					require.Equal(t, 2, calls)
				}
			})
		}
	}
}
