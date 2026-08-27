package api

import (
	"context"
	"log/slog"
	"testing"

	"nudgebee/services/integrations/core"
	"nudgebee/services/observability"
	"nudgebee/services/security"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// logs_list_labels takes an OPTIONAL `integration_config_values`: with it, the fields
// returned describe the configuration IN THE REQUEST rather than the one saved for the
// account, which is what lets the integration form list a backend's fields before the
// integration exists.
//
// The field is privileged — honouring it makes the server fetch from an endpoint the
// caller names — so these tests pin both halves: the gate holds, and omitting the field
// leaves the existing behaviour untouched.

func ctxWith(sc *security.SecurityContext) *security.RequestContext {
	return security.NewRequestContext(context.Background(), sc, slog.Default(), nil, nil)
}

// tenantAdminCtx builds the realistic caller — a tenant admin with a tenant — without a
// metastore. NewSecurityContextForTenantAdmin resolves the tenant's accounts from the DB
// and returns nil when that fails, so stub just that lookup.
func tenantAdminCtx(t *testing.T) *security.RequestContext {
	t.Helper()
	p := gomonkey.NewPatches()
	p.ApplyFunc(security.GetAccountIdsByTenantId, func(_ string) ([]string, error) {
		return []string{"acc-1"}, nil
	})
	sc := security.NewSecurityContextForTenantAdmin("tenant-1")
	p.Reset()
	require.NotNil(t, sc)
	return ctxWith(sc)
}

func probeValues() []core.IntegrationConfigValue {
	return []core.IntegrationConfigValue{{Name: "url", Value: "http://probe.invalid:9200"}}
}

// TestLogLabelProbeContext_NoValuesIsUntouched is the backward-compatibility guarantee:
// every pre-existing caller — the query builder, llm-server, the internal constructors —
// omits the field and must keep the caller's own context and the saved-integration path.
func TestLogLabelProbeContext_NoValuesIsUntouched(t *testing.T) {
	base := ctxWith(security.NewSecurityContextForAuditRelay("tenant-1", "user-1"))
	req := &observability.FetchLogLabelRequest{AccountId: "acc-1", LogProvider: "ES"}

	got, status, err := logLabelProbeContext(base, req)

	require.NoError(t, err)
	assert.Equal(t, 200, status)
	assert.Same(t, base, got, "no override context should be built when no values are supplied")
}

// TestLogLabelProbeContext_RefusesBelowTenantAdmin is the gate. logs_list_labels is
// reachable by account_admin_readonly and tenant_usage; without this check any of them
// could make the server fetch from an endpoint they name.
func TestLogLabelProbeContext_RefusesBelowTenantAdmin(t *testing.T) {
	base := ctxWith(security.NewSecurityContextForAuditRelay("tenant-1", "user-1"))
	req := &observability.FetchLogLabelRequest{
		AccountId:               "acc-1",
		LogProvider:             "ES",
		IntegrationConfigValues: probeValues(),
	}

	got, status, err := logLabelProbeContext(base, req)

	require.Error(t, err)
	assert.Equal(t, 403, status)
	assert.Nil(t, got)
	assert.Contains(t, err.Error(), "tenant admin")
	assert.NotEmpty(t, req.IntegrationConfigValues, "a refused request must not be silently downgraded to the saved config")
}

// TestLogLabelProbeContext_AllowsSuperAdmin — super admin is above tenant admin, so the
// gate must not lock it out.
func TestLogLabelProbeContext_AllowsSuperAdmin(t *testing.T) {
	base := ctxWith(security.NewSecurityContextForSuperAdmin())
	req := &observability.FetchLogLabelRequest{
		AccountId:               "acc-1",
		LogProvider:             "ES",
		IntegrationConfigValues: probeValues(),
	}

	got, status, err := logLabelProbeContext(base, req)

	require.NoError(t, err)
	assert.Equal(t, 200, status)
	require.NotNil(t, got)
	assert.NotSame(t, base, got, "an override context should have been built")
	assert.Empty(t, req.IntegrationConfigValues, "transport only — the values must not travel past the handler")
}

// TestLogLabelProbeContext_OverrideIsScopedToTheRequest: the override answers only the
// (account, integration) named, so probing one backend cannot feed its values to another
// provider's config getter in the same request.
func TestLogLabelProbeContext_OverrideIsScopedToTheRequest(t *testing.T) {
	base := tenantAdminCtx(t)
	req := &observability.FetchLogLabelRequest{
		AccountId:               "acc-1",
		LogProvider:             "ES",
		LogProviderSource:       "user",
		IntegrationConfigValues: probeValues(),
	}

	got, _, err := logLabelProbeContext(base, req)
	require.NoError(t, err)

	dtos, err := core.ListIntegrationConfigs(got, "acc-1", "ES")
	require.NoError(t, err)
	require.Len(t, dtos, 1)
	assert.Equal(t, "http://probe.invalid:9200", dtos[0].Configs[0].Value)
	assert.Equal(t, "user", dtos[0].Source, "provider config getters filter on source")
}

// TestLogLabelProbeContext_RequiresProvider: without a provider there is nothing to scope
// the override to, and an unscoped one would answer every integration lookup the request
// makes.
func TestLogLabelProbeContext_RequiresProvider(t *testing.T) {
	base := ctxWith(security.NewSecurityContextForSuperAdmin())
	req := &observability.FetchLogLabelRequest{
		AccountId:               "acc-1",
		IntegrationConfigValues: probeValues(),
	}

	_, status, err := logLabelProbeContext(base, req)

	require.Error(t, err)
	assert.Equal(t, 400, status)
	assert.Contains(t, err.Error(), "log_provider is required")
}
