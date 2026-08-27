package core

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"nudgebee/services/internal/database"
	"nudgebee/services/security"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// overrideCtx builds a tenant-admin request context without a database.
// NewSecurityContextForTenantAdmin resolves the tenant's accounts from the metastore
// and returns nil when that fails, so stub just that lookup — these tests are about
// the override, not about account resolution.
func overrideCtx(t *testing.T) *security.RequestContext {
	t.Helper()
	p := gomonkey.NewPatches()
	p.ApplyFunc(security.GetAccountIdsByTenantId, func(_ string) ([]string, error) {
		return []string{"acc-1", "acc-2"}, nil
	})
	sc := security.NewSecurityContextForTenantAdmin("tenant-1")
	p.Reset()
	require.NotNil(t, sc)
	return security.NewRequestContext(context.Background(), sc, slog.Default(), nil, nil)
}

// failDB makes any database access fail. A test that still returns config values
// under it proves the override answered without touching the database — which is the
// property that makes previewing an unsaved form possible at all.
func failDB(t *testing.T) *gomonkey.Patches {
	t.Helper()
	p := gomonkey.NewPatches()
	p.ApplyFunc(database.GetDatabaseManager, func(_ database.DatabaseManagerType) (*database.DatabaseManager, error) {
		return nil, errors.New("database must not be reached on the override path")
	})
	t.Cleanup(p.Reset)
	return p
}

// stubIntegration stands in for a registered provider. ListIntegrationConfigs checks
// the registry before anything else, and the real providers register from the parent
// `integrations` package, which this one cannot import.
type stubIntegration struct{ name string }

func (s stubIntegration) Name() string                  { return s.name }
func (s stubIntegration) Category() IntegrationCategory { return IntegrationCategoryLog }
func (s stubIntegration) ValidateConfig(_ *security.SecurityContext, _ []IntegrationConfigValue, _ string) []error {
	return nil
}
func (s stubIntegration) ConfigSchema() IntegrationSchema {
	return IntegrationSchema{Type: ToolSchemaTypeObject, Properties: map[string]IntegrationSchemaProperty{}}
}

func init() {
	RegisterIntegration(stubIntegration{name: "ES"})
	RegisterIntegration(stubIntegration{name: "pinot"})
}

func sampleOverride() ConfigOverride {
	return ConfigOverride{
		AccountId:       "acc-1",
		IntegrationName: "ES",
		Source:          "user",
		Values:          []IntegrationConfigValue{{Name: "url", Value: "https://localhost:9200"}},
	}
}

func TestListIntegrationConfigs_AnswersFromOverrideWithoutDB(t *testing.T) {
	ctx := WithConfigOverride(overrideCtx(t), sampleOverride())
	failDB(t)

	dtos, err := ListIntegrationConfigs(ctx, "acc-1", "ES")

	require.NoError(t, err)
	require.Len(t, dtos, 1)
	assert.Equal(t, "https://localhost:9200", dtos[0].Configs[0].Value)
}

// TestListIntegrationConfigs_OverrideCarriesSourceAndType pins the shape the provider
// config getters depend on: they filter on Source == "user" and read Configs, so a
// synthetic record missing either is invisible to every one of them.
func TestListIntegrationConfigs_OverrideCarriesSourceAndType(t *testing.T) {
	ctx := WithConfigOverride(overrideCtx(t), sampleOverride())
	failDB(t)

	dtos, err := ListIntegrationConfigs(ctx, "acc-1", "ES")

	require.NoError(t, err)
	assert.Equal(t, "user", dtos[0].Source)
	assert.Equal(t, "ES", dtos[0].Type)
	assert.NotEmpty(t, dtos[0].Configs)
	assert.Empty(t, dtos[0].Id, "a synthetic record has never been persisted and must not claim an id")
}

func TestWithConfigOverride_DefaultsSourceToUser(t *testing.T) {
	o := sampleOverride()
	o.Source = ""
	ctx := WithConfigOverride(overrideCtx(t), o)
	failDB(t)

	dtos, err := ListIntegrationConfigs(ctx, "acc-1", "ES")

	require.NoError(t, err)
	assert.Equal(t, "user", dtos[0].Source)
}

// TestListIntegrationConfigs_OverrideDoesNotLeak is the guardrail that matters. An
// override scoped by a bare "active" flag would also answer every other integration
// lookup the same request makes — a probe of one backend would start handing its
// credentials to another provider's config getter.
func TestListIntegrationConfigs_OverrideDoesNotLeak(t *testing.T) {
	for _, tc := range []struct{ name, accountId, integration string }{
		{"different account", "acc-2", "ES"},
		{"different integration", "acc-1", "pinot"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := WithConfigOverride(overrideCtx(t), sampleOverride())
			failDB(t)

			_, err := ListIntegrationConfigs(ctx, tc.accountId, tc.integration)

			// The override declined, so the call fell through to the (failing) DB.
			require.Error(t, err, "a non-matching lookup must not be answered by the override")
		})
	}
}

func TestListIntegrationConfigs_NoOverrideIsUnaffected(t *testing.T) {
	ctx := overrideCtx(t)
	failDB(t)

	_, err := ListIntegrationConfigs(ctx, "acc-1", "ES")

	require.Error(t, err, "without an override the normal database path must run")
}

// TestConfigOverride_IgnoredWhenIncomplete: a half-built override must be inert rather
// than matching everything with an empty account or integration name.
func TestConfigOverride_IgnoredWhenIncomplete(t *testing.T) {
	for _, o := range []ConfigOverride{
		{IntegrationName: "ES", Values: []IntegrationConfigValue{{Name: "url", Value: "x"}}},
		{AccountId: "acc-1", Values: []IntegrationConfigValue{{Name: "url", Value: "x"}}},
	} {
		ctx := WithConfigOverride(overrideCtx(t), o)
		failDB(t)
		_, err := ListIntegrationConfigs(ctx, "acc-1", "ES")
		require.Error(t, err)
	}
}

// TestConfigOverride_IntegrationNameIsCaseInsensitive: the registry treats names
// case-insensitively, so a probe naming "es" must still match a lookup for "ES".
func TestConfigOverride_IntegrationNameIsCaseInsensitive(t *testing.T) {
	o := sampleOverride()
	o.IntegrationName = "es"
	// The registry lookup happens before the override, so ask under the registered name.
	_, ok := configOverrideFor(WithConfigOverride(overrideCtx(t), o), "acc-1", "ES")
	assert.True(t, ok)
}

// TestWithConfigOverride_PreservesSecurityContext: the preview must not quietly widen
// who the caller is — same tenant, same permissions, only the config lookup changes.
func TestWithConfigOverride_PreservesSecurityContext(t *testing.T) {
	base := overrideCtx(t)
	ctx := WithConfigOverride(base, sampleOverride())

	assert.Equal(t, base.GetSecurityContext().GetTenantId(), ctx.GetSecurityContext().GetTenantId())
	assert.Same(t, base.GetSecurityContext(), ctx.GetSecurityContext())
}

func TestWithConfigOverride_NilContext(t *testing.T) {
	assert.Nil(t, WithConfigOverride(nil, sampleOverride()))
	_, ok := configOverrideFor(nil, "acc-1", "ES")
	assert.False(t, ok)
}
