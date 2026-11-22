package core

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"nudgebee/llm/security"
	"nudgebee/llm/security/toolapproval"
	toolcore "nudgebee/llm/tools/core"
)

type integrationScopedTool struct {
	MockContextCapturingTool
	integrationID string
}

func (t *integrationScopedTool) IntegrationID() string { return t.integrationID }

// An MCP tool's call is matched against its integration, so an organisation
// rule pinned to that integration applies (#40399).
func TestIntegrationToolApprovalTargetIsItsIntegration(t *testing.T) {
	request := NBAgentRequest{AccountId: "cccccccc-cccc-cccc-cccc-cccccccccccc", AgentId: uuid.NewString()}
	ctx := security.NewRequestContextForTenantAccountAdmin("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", security.GetSystemUserId(), []string{request.AccountId})
	tool := &integrationScopedTool{MockContextCapturingTool: MockContextCapturingTool{NameVal: "jira_create_issue"}, integrationID: "integration-1"}
	executor := newPlannerExecutor(ctx, &mockNBAgentPlanner{}, &MockAgent{SupportedTools: []toolcore.NBTool{tool}}, request, 3)

	target := executor.toolApprovalTarget(tool, NBAgentPlannerToolAction{Tool: tool.Name(), ToolInput: "{}"})
	assert.Equal(t, toolApprovalTarget{ConfigID: "integration-1", AccountID: request.AccountId}, target)

	tool.integrationID = ""
	target = executor.toolApprovalTarget(tool, NBAgentPlannerToolAction{Tool: tool.Name(), ToolInput: "{}"})
	assert.Equal(t, toolapproval.UnresolvedTargetID, target.ConfigID, "an integration tool without an integration never matches")
}
