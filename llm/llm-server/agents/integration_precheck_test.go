package agents

import (
	"errors"
	"testing"

	"nudgebee/llm/agents/core"
	"nudgebee/llm/security"
	toolcore "nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
)

// configRequiringTool implements both NBTool (via the embedded mockTool from
// agent_delegate_test.go) and NBToolConfig, marking it as a tool whose use
// depends on an installed integration config.
type configRequiringTool struct {
	mockTool
}

func (c *configRequiringTool) ConfigSchema(_ *security.RequestContext) toolcore.ToolConfigSchema {
	return toolcore.ToolConfigSchema{Type: toolcore.ToolSchemaTypeObject}
}

type fakeAgent struct {
	name  string
	tools []toolcore.NBTool
}

func (f *fakeAgent) GetName() string                                                  { return f.name }
func (f *fakeAgent) GetNameAliases() []string                                         { return nil }
func (f *fakeAgent) GetDescription() string                                           { return "" }
func (f *fakeAgent) GetSupportedTools(_ *security.RequestContext) []toolcore.NBTool   { return f.tools }
func (f *fakeAgent) GetSystemPrompt(_ *security.RequestContext, _ core.NBAgentRequest) core.NBAgentPrompt {
	return core.NBAgentPrompt{}
}
func (f *fakeAgent) GetPlannerType() core.AgentPlannerType { return core.AgentPlannerTypeReAct }

func TestEnsureAgentIntegrations(t *testing.T) {
	ctx := security.NewRequestContextForSuperAdmin()

	configFreeTool := &mockTool{name: "websearch"}
	jiraTool := &configRequiringTool{mockTool{name: "jira"}}
	githubTool := &configRequiringTool{mockTool{name: "github"}}

	tests := []struct {
		name          string
		agent         core.NBAgent
		resolver      configResolver
		expectErr     bool
		expectMissing []string
	}{
		{
			name:  "agent with no tools always passes",
			agent: &fakeAgent{name: "help"},
		},
		{
			name:  "agent with a config-free tool always passes",
			agent: &fakeAgent{name: "search", tools: []toolcore.NBTool{configFreeTool}},
		},
		{
			name:  "agent with config tool and one config installed passes",
			agent: &fakeAgent{name: "tickets", tools: []toolcore.NBTool{jiraTool}},
			resolver: func(_ *security.RequestContext, _ string, _ toolcore.NBTool) ([]toolcore.ToolConfig, error) {
				return []toolcore.ToolConfig{{Name: "primary-jira"}}, nil
			},
		},
		{
			name:  "agent with multiple config tools passes if any has a config",
			agent: &fakeAgent{name: "tickets", tools: []toolcore.NBTool{jiraTool, githubTool}},
			resolver: func(_ *security.RequestContext, _ string, tool toolcore.NBTool) ([]toolcore.ToolConfig, error) {
				if tool.Name() == "github" {
					return []toolcore.ToolConfig{{Name: "primary-github"}}, nil
				}
				return nil, nil
			},
		},
		{
			name:  "resolver error fails open",
			agent: &fakeAgent{name: "tickets", tools: []toolcore.NBTool{jiraTool}},
			resolver: func(_ *security.RequestContext, _ string, _ toolcore.NBTool) ([]toolcore.ToolConfig, error) {
				return nil, errors.New("db down")
			},
		},
		{
			name:  "agent with only config tools and no configs fails",
			agent: &fakeAgent{name: "tickets", tools: []toolcore.NBTool{jiraTool, githubTool}},
			resolver: func(_ *security.RequestContext, _ string, _ toolcore.NBTool) ([]toolcore.ToolConfig, error) {
				return nil, nil
			},
			expectErr:     true,
			expectMissing: []string{"jira", "github"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ensureAgentIntegrations(ctx, tc.agent, "acct-1", tc.resolver)
			if !tc.expectErr {
				assert.NoError(t, err)
				return
			}
			assert.Error(t, err)
			var missing *MissingIntegrationError
			assert.True(t, errors.As(err, &missing), "expected MissingIntegrationError, got %T", err)
			assert.Equal(t, tc.agent.GetName(), missing.AgentName)
			assert.Equal(t, tc.expectMissing, missing.MissingTools)
		})
	}
}
