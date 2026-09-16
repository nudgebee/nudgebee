package tools

import (
	"fmt"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

type testingCleanup interface {
	Helper()
	Cleanup(func())
}

// StubTenantAccounts makes TenantAccountLister return byTool[tool name] for
// tenant (and an error for any other tenant) until the test ends. For tests only.
func StubTenantAccounts(t testingCleanup, tenant string, byTool map[string][]core.ToolConfig) {
	t.Helper()
	previous := TenantAccountLister
	TenantAccountLister = func(ctx *security.RequestContext, tool core.NBTool) ([]core.ToolConfig, error) {
		if got := ctx.GetSecurityContext().GetTenantId(); got != tenant {
			return nil, fmt.Errorf("stub: no accounts for tenant %q", got)
		}
		return byTool[tool.Name()], nil
	}
	t.Cleanup(func() { TenantAccountLister = previous })
}

// StubTenantClusters is StubTenantAccounts for kubectl clusters only.
func StubTenantClusters(t testingCleanup, tenant string, configs []core.ToolConfig) {
	t.Helper()
	StubTenantAccounts(t, tenant, map[string][]core.ToolConfig{ToolExecuteKubectlCommand: configs})
}
