package tools

import (
	"context"
	"encoding/json"
	"testing"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func knowledgeCtx(t *testing.T, wire string) core.NbToolContext {
	t.Helper()
	var sc security.SecurityContext
	require.NoError(t, json.Unmarshal([]byte(wire), &sc))
	return core.NbToolContext{AccountId: "acct-a", Ctx: security.NewRequestContext(context.Background(), &sc, nil, nil, nil)}
}

// Knowledge tools stay callable, but a person whose access comes from custom
// roles gets the tenant's knowledge only with ai_kbs:Read.
func TestKnowledgeWithheldWithoutAiKbsRead(t *testing.T) {
	without := knowledgeCtx(t, `{"TenantId":"t","UserId":"u","AccountIds":["acct-a"],"ScopedCustomPermissions":{"acct-a":{"k8s:Read":true}}}`)
	resp, withheld := knowledgeWithheld(without)
	assert.True(t, withheld)
	assert.Contains(t, resp.Data, "ai_kbs:Read")
	assert.Equal(t, core.NBToolResponseStatusSuccess, resp.Status)

	with := knowledgeCtx(t, `{"TenantId":"t","UserId":"u","AccountIds":["acct-a"],"ScopedCustomPermissions":{"acct-a":{"ai_kbs:Read":true}}}`)
	_, withheld = knowledgeWithheld(with)
	assert.False(t, withheld)

	admin := knowledgeCtx(t, `{"TenantId":"t","UserId":"u","AccountIds":["acct-a"],"Roles":["account_admin_readonly"],"ScopedEntityIds":{"account_admin_readonly":["acct-a"]}}`)
	_, withheld = knowledgeWithheld(admin)
	assert.False(t, withheld, "a built-in role keeps today's behaviour")
}
