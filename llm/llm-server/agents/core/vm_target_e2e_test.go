//go:build e2e

package core

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"nudgebee/llm/security"
	"nudgebee/llm/tools"
	toolcore "nudgebee/llm/tools/core"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A k8s cluster conversation lists and runs a read on the VMs of a connected
// VM account named as the call's target (provider "vm").
func TestCallNbTool_VMAccountTargetFromClusterConversation(t *testing.T) {
	RequireEnv(t, "TEST_ACCOUNT", "TEST_USER", "TEST_TENANT")
	accountId := os.Getenv("TEST_ACCOUNT")
	target := envOr("TEST_DELEGATE_VM_TARGET", "vulnerable-vms-test")
	sc := security.NewRequestContextForTenantAdminWithUser(t.Context(), os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"))
	call := func(t *testing.T, toolName string, args map[string]any) toolcore.NBToolResponse {
		tool, ok := toolcore.GetNBTool(accountId, toolName)
		require.True(t, ok)
		input, err := json.Marshal(args)
		require.NoError(t, err)
		req := NBAgentRequest{AccountId: accountId, UserId: os.Getenv("TEST_USER"), ConversationId: uuid.NewString(), MessageId: uuid.NewString(), AgentId: uuid.NewString()}
		resp, err := callNbTool(sc, req, "vm-target-e2e", tool, string(input), nil, "", uuid.NewString(), "")
		t.Logf("%s: status=%s err=%v data=%.600s details=%v", toolName, resp.Status, err, fmt.Sprint(resp.Data), resp.AdditionalDetails)
		return resp
	}
	vmTarget := map[string]any{"provider": "vm", "name": target}

	list := call(t, tools.ToolVMList, map[string]any{"target": vmTarget})
	require.NotEqual(t, toolcore.NBToolResponseStatusError, list.Status)
	var listed struct {
		VMs []struct {
			ResourceID string `json:"resource_id"`
			Name       string `json:"name"`
		} `json:"vms"`
	}
	require.NoError(t, json.Unmarshal([]byte(list.Data), &listed))
	require.NotEmpty(t, listed.VMs, "%s's VMs must be listed", target)
	ranOn, _ := list.AdditionalDetails[toolcore.MetadataExecutedTarget].(map[string]any)
	assert.Equal(t, "vm", ranOn["provider"], "the step must say it ran on the VM account")
	assert.Equal(t, target, ranOn["name"])

	run := call(t, tools.ToolVMExecute, map[string]any{"target": vmTarget, "resource_id": listed.VMs[0].ResourceID, "command": "uptime"})
	assert.NotEqual(t, toolcore.NBToolResponseStatusError, run.Status)
	assert.Contains(t, fmt.Sprint(run.Data), "load average", "uptime must run on %s", listed.VMs[0].Name)

	// Without a target the conversation's own account is a cluster, not a VM fleet.
	own := call(t, tools.ToolVMList, map[string]any{})
	assert.Equal(t, toolcore.NBToolResponseStatusError, own.Status)

	// A user who can read only the conversation's own account cannot reach
	// the VM account by naming it, and is not told it exists.
	sc = security.NewRequestContextForTenantAccountAdmin(os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"), []string{accountId})
	denied := call(t, tools.ToolVMList, map[string]any{"target": vmTarget})
	assert.Equal(t, toolcore.NBToolResponseStatusError, denied.Status)
	assert.Contains(t, fmt.Sprint(denied.Data), "not connected or you do not have access")
	assert.NotContains(t, fmt.Sprint(denied.Data), "available VM accounts: "+target)
	sc = security.NewRequestContextForTenantAdminWithUser(t.Context(), os.Getenv("TEST_TENANT"), os.Getenv("TEST_USER"))

	// A name that is not a connected VM account the user can read is refused.
	missing := call(t, tools.ToolVMList, map[string]any{"target": map[string]any{"provider": "vm", "name": "no-such-vms"}})
	assert.Equal(t, toolcore.NBToolResponseStatusError, missing.Status)
	assert.Contains(t, fmt.Sprint(missing.Data), "not connected or you do not have access")
}
