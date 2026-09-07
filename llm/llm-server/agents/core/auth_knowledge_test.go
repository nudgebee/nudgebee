package core

import (
	"fmt"
	"github.com/stretchr/testify/require"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"
	"testing"
)

func TestKnowledgeToolsPlannerAuthorizationParity(t *testing.T) {
	for _, name := range []string{"logs", "traces_clickhouse"} {
		for _, policy := range []KnowledgePolicy{KnowledgeAuto, KnowledgeAlways, KnowledgeLLMOnly, KnowledgeDisabled} {
			for _, tool := range []string{"search_skills", "load_skills"} {
				for _, mode := range []string{"unrestricted", "denied", "allow-listed", "excluded", "query-denied"} {
					t.Run(name+"/"+string(policy)+"/"+tool+"/"+mode, func(t *testing.T) {
						agent := mockPlainAgent{name: name}
						request := NBAgentRequest{AccountId: "account", KnowledgePolicy: policy, KnowledgePolicyResolved: true}
						switch mode {
						case "denied":
							request.Capabilities.DisabledTools = []string{tool}
						case "allow-listed":
							request.Capabilities.AllowedTools = []string{tool}
						case "excluded":
							request.Capabilities.AllowedTools = []string{"unrelated"}
						case "query-denied":
							request.QueryConfig.Capabilities.DisabledTools = []string{tool}
						}
						caps := request.Capabilities.Merge(request.QueryConfig.Capabilities)
						advertised := FilterAndInjectDefaultTools(request.AccountId, agent, "", nil, caps, policy)
						hasTool := false
						for _, candidate := range advertised {
							if candidate.Name() == tool {
								hasTool = true
							}
						}
						want := policy != KnowledgeDisabled && (mode == "unrestricted" || mode == "allow-listed")
						require.Equal(t, want, hasTool)
						finish, _, err := IsAgentToolAuthorizedToProcessRequest(security.NewRequestContextForSuperAdmin(), agent, request, NBAgentPlannerToolAction{Tool: tool, ToolInput: `{"query":"trace fields","skill_name":"guide"}`})
						if want {
							require.NoError(t, err)
							require.Nil(t, finish)
						} else {
							require.Error(t, err)
						}
					})
				}
			}
		}
	}
}

// Declared and previously discovered tools must not bypass current restrictions.
func TestKnowledgeAuthorizationRestrictionsOverrideDiscovery(t *testing.T) {
	for _, name := range []string{"search_skills", "load_skills"} {
		for _, disabledPolicy := range []bool{false, true} {
			t.Run(name+fmt.Sprint(disabledPolicy), func(t *testing.T) {
				tool, ok := toolcore.GetNBTool("account", name)
				require.True(t, ok)
				agent := knowledgeDeclaredAgent{mockPlainAgent: mockPlainAgent{name: "traces_clickhouse"}, tool: tool}
				request := NBAgentRequest{AccountId: "account", ConversationId: t.Name(), KnowledgePolicy: KnowledgeAuto}
				if disabledPolicy {
					request.KnowledgePolicy = KnowledgeDisabled
				} else {
					request.Capabilities.DisabledTools = []string{name}
				}
				RecordDiscoveredTools(request.ConversationId, []string{name})
				_, _, err := IsAgentToolAuthorizedToProcessRequest(security.NewRequestContextForSuperAdmin(), agent, request, NBAgentPlannerToolAction{Tool: name})
				require.Error(t, err)
			})
		}
	}
}

type knowledgeDeclaredAgent struct {
	mockPlainAgent
	tool toolcore.NBTool
}

func (a knowledgeDeclaredAgent) GetSupportedTools(*security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{a.tool}
}

type nilToolAgent struct {
	mockPlainAgent
}

func (a nilToolAgent) GetSupportedTools(*security.RequestContext) []toolcore.NBTool {
	return []toolcore.NBTool{nil}
}

func TestAuthAgentNilToolResilience(t *testing.T) {
	agent := nilToolAgent{mockPlainAgent: mockPlainAgent{name: "nil_tool_agent"}}
	request := NBAgentRequest{AccountId: "account", ConversationId: t.Name(), KnowledgePolicy: KnowledgeDisabled}
	require.NotPanics(t, func() {
		_, _, err := IsAgentToolAuthorizedToProcessRequest(
			security.NewRequestContextForSuperAdmin(),
			agent,
			request,
			NBAgentPlannerToolAction{Tool: "any_tool"},
		)
		require.Error(t, err)
	})

	require.NotPanics(t, func() {
		tools := FilterAndInjectDefaultTools("account", agent, "", []toolcore.NBTool{nil}, toolcore.AgentCapabilities{}, KnowledgeAuto)
		require.NotNil(t, tools)
	})
}
