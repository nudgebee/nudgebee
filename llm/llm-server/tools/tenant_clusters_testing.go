package tools

import (
	"fmt"

	"nudgebee/llm/security"
	"nudgebee/llm/tools/core"
)

// StubTenantClusters makes TenantClusterLister return configs for tenant (and
// an error for any other tenant) until the test ends. For tests only.
func StubTenantClusters(t interface {
	Helper()
	Cleanup(func())
}, tenant string, configs []core.ToolConfig) {
	t.Helper()
	previous := TenantClusterLister
	TenantClusterLister = func(ctx *security.RequestContext, _ core.NBTool) ([]core.ToolConfig, error) {
		if got := ctx.GetSecurityContext().GetTenantId(); got != tenant {
			return nil, fmt.Errorf("stub: no clusters for tenant %q", got)
		}
		return configs, nil
	}
	t.Cleanup(func() { TenantClusterLister = previous })
}
