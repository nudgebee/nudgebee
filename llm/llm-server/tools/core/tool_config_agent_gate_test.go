package core

import (
	"testing"

	"nudgebee/llm/security"

	"github.com/stretchr/testify/assert"
)

// schemaTool is a fakeNBTool with a fixed config schema.
type schemaTool struct {
	fakeNBTool
	schema ToolConfigSchema
}

func (s schemaTool) ConfigSchema(*security.RequestContext) ToolConfigSchema { return s.schema }

// TestIsToolConfigured_AgentGatedByLeafTools pins the semantics code_analyzer
// relies on: an agent-tool that declares leaf tools is offered only when one of
// them is configured, while one that declares none is offered on any account
// with any integration.
func TestIsToolConfigured_AgentGatedByLeafTools(t *testing.T) {
	const leaf = "fake_git_leaf_gate_test"
	RegisterNBToolFactory(leaf, func(string) (NBTool, error) {
		return schemaTool{fakeNBTool: fakeNBTool{name: leaf}, schema: ToolConfigSchema{ConfigSource: ToolConfigSourceTicket, ConfigType: "github"}}, nil
	})
	t.Cleanup(func() { delete(nbSystemTools, leaf) })

	agentTool := func(items map[string]any) NBTool {
		return schemaTool{fakeNBTool: fakeNBTool{name: "fake_agent_gate_test"}, schema: ToolConfigSchema{
			ConfigSource: ToolConfigSourceLLMAgent,
			Properties:   map[string]ToolSchemaProperty{"tools": {Type: ToolSchemaTypeArray, Items: items}},
		}}
	}
	gated := agentTool(map[string]any{leaf: true})
	ungated := agentTool(map[string]any{})

	ctx := security.NewRequestContextForSuperAdmin()
	slackOnly := AccountConfigSummary{IntegrationTypes: map[string]bool{"slack": true}, TenantIntegrationTypes: map[string]bool{"slack": true}, HasAgent: true}
	withGithub := AccountConfigSummary{IntegrationTypes: map[string]bool{}, TenantIntegrationTypes: map[string]bool{"github": true}}

	assert.True(t, isToolConfigured(ctx, "acct", ungated, slackOnly, nil), "no leaf tools: any integration offers it")
	assert.False(t, isToolConfigured(ctx, "acct", gated, slackOnly, nil), "leaf tools declared: unrelated integrations do not")
	assert.True(t, isToolConfigured(ctx, "acct", gated, withGithub, nil), "leaf tools declared: a configured leaf offers it")
}
