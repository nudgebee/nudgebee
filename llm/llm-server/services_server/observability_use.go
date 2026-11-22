package services_server

import (
	"fmt"
	"strings"

	"nudgebee/llm/security"
)

// resolveProviderForUse is GetObservabilityProvider, replaceable in tests.
var resolveProviderForUse = func(accountId, providerType, requestedProvider string) (ObservabilityProvider, error) {
	tenantId, err := security.GetTenantIdFromAccountId(accountId)
	if err != nil {
		return ObservabilityProvider{}, err
	}
	// A lookup of the account's configuration, not an action of the caller's.
	lookup := security.NewRequestContextForTenantAccountAdmin(tenantId, "", []string{accountId})
	return GetObservabilityProvider(*lookup, accountId, providerType, requestedProvider)
}

// checkObservabilityUse refuses, in a grant-checked run (security.IsGrantCheckedRun:
// a bee's, or a person's without a built-in role on accountId), reading logs,
// metrics or traces of accountId, or their series and
// label names, that the principal may not read: Read on the logs, metrics or
// traces module (the grant services-server's query actions check), and for
// data from an integration also integrations.<type>:Read. The provider is
// resolved the way services-server resolves it, so a tool that names a
// provider is held to the integration that answers. Outside such a run it
// allows without a lookup.
func checkObservabilityUse(ctx security.RequestContext, accountId, providerType, provider, source string) error {
	if !security.IsGrantCheckedRun(&ctx, accountId) {
		return nil
	}
	resolved := ObservabilityProvider{Provider: provider, IntegrationSource: source}
	if strings.TrimSpace(source) == "" {
		var err error
		if resolved, err = resolveProviderForUse(accountId, providerType, provider); err != nil {
			return fmt.Errorf("permission denied: this run may read %s only from a source it is granted, and the %s provider could not be resolved: %w", providerType, providerType, err)
		}
	}
	if !security.ToolPathAccountAccess(&ctx, accountId, providerType, security.SecurityAccessTypeRead) {
		return fmt.Errorf("permission denied: this run needs %s:Read on account %s to read %s from %s", providerType, accountId, providerType, resolved.Provider)
	}
	// The cluster agent's own backend and a cloud provider's monitoring have
	// no integration behind them.
	if strings.EqualFold(resolved.IntegrationSource, "agent") || isCloudMonitoringProvider(resolved.Provider) {
		return nil
	}
	return security.CheckIntegrationUse(&ctx, accountId, resolved.Provider, false)
}

// isCloudMonitoringProvider reports a cloud provider's own monitoring (no
// integration behind it), as tools.IsCloudObservabilityProvider does.
func isCloudMonitoringProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "aws", "gcp", "azure":
		return true
	}
	return false
}
