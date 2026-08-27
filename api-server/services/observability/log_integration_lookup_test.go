package observability

import (
	"testing"

	"nudgebee/services/integrations/core"
	"nudgebee/services/security"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Both cases below were found by calling the action against real dev data, not by
// reading the code — the resolver and the listing disagree in ways that only show up
// with a real account behind them. They are pinned here so they cannot come back.

// patchIntegrationLookup stubs the two DB-backed calls lookupLogIntegrationConfigs
// makes: the provider resolver and the integration listing.
func patchIntegrationLookup(t *testing.T, provider, source string, dto *core.IntegrationDto, dtos []core.IntegrationDto) {
	t.Helper()
	patches := gomonkey.NewPatches()
	patches.ApplyFunc(getLogsMetricsTracesProviderWithIntegration,
		func(_ *security.RequestContext, _, _, _, _ string) (string, string, *core.IntegrationDto, error) {
			return provider, source, dto, nil
		})
	patches.ApplyFunc(core.ListIntegrationConfigs,
		func(_ *security.RequestContext, _ string, _ string) ([]core.IntegrationDto, error) {
			return dtos, nil
		})
	t.Cleanup(patches.Reset)
}

// TestLookupLogIntegrationConfigs_FindsAgentSourceIntegration.
//
// The lookup used to fall back to `Source == "user"` only. Every agent-source log
// provider — loki, ES-agent, pinot-agent — therefore resolved to "no integration", so a
// per-account setting saved on one of those was written to the DB and never read back.
// Observed live: a Loki account reported integration_saved=false despite having a saved
// agent integration.
func TestLookupLogIntegrationConfigs_FindsAgentSourceIntegration(t *testing.T) {
	patchIntegrationLookup(t, "loki", "agent", nil, []core.IntegrationDto{
		{Id: "int-agent", Source: "agent", Type: "loki", Configs: []core.IntegrationConfigValue{
			{Name: core.LogLabelMappingsConfigName, Value: `[{"accountId":"acc-1","mappings":{"pod":"agent_pod"}}]`},
		}},
	})

	configs, found := lookupLogIntegrationConfigs(newLogLabelCtx(), "acc-1", "loki", "agent")

	require.True(t, found, "an agent-source integration must be found")
	require.Len(t, configs, 1)
	assert.Equal(t, core.LogLabelMappingsConfigName, configs[0].Name)
}

// TestLookupLogIntegrationConfigs_PrefersResolvedSource covers the case that makes the
// source-aware match matter: one account carrying the same provider from both an agent
// and a user integration. Reading the wrong one would apply another integration's field
// names to this query.
func TestLookupLogIntegrationConfigs_PrefersResolvedSource(t *testing.T) {
	dtos := []core.IntegrationDto{
		{Id: "int-user", Source: "user", Type: "pinot", Configs: []core.IntegrationConfigValue{{Name: "marker", Value: "user"}}},
		{Id: "int-agent", Source: "agent", Type: "pinot", Configs: []core.IntegrationConfigValue{{Name: "marker", Value: "agent"}}},
	}

	patchIntegrationLookup(t, "pinot", "agent", nil, dtos)
	configs, found := lookupLogIntegrationConfigs(newLogLabelCtx(), "acc-1", "pinot", "agent")
	require.True(t, found)
	assert.Equal(t, "agent", configs[0].Value, "the resolved source must win over the user fallback")
}

// TestLookupLogIntegrationConfigs_PrefersResolverDto keeps the strongest signal first:
// when the resolver names an exact integration, that one wins regardless of source.
func TestLookupLogIntegrationConfigs_PrefersResolverDto(t *testing.T) {
	dtos := []core.IntegrationDto{
		{Id: "int-a", Source: "user", Type: "pinot", Configs: []core.IntegrationConfigValue{{Name: "marker", Value: "a"}}},
		{Id: "int-b", Source: "user", Type: "pinot", Configs: []core.IntegrationConfigValue{{Name: "marker", Value: "b"}}},
	}

	patchIntegrationLookup(t, "pinot", "user", &core.IntegrationDto{Id: "int-b"}, dtos)
	configs, found := lookupLogIntegrationConfigs(newLogLabelCtx(), "acc-1", "pinot", "user")
	require.True(t, found)
	assert.Equal(t, "b", configs[0].Value)
}

// TestLookupLogIntegrationConfigs_FoundWithNilResolverDto is the second live finding.
//
// getLogsMetricsTracesProviderWithIntegration short-circuits when the caller pins BOTH
// provider and source — which the integration form always does — and returns a nil DTO
// even though the integration exists. Deriving "is this saved?" from that DTO reported
// every saved integration as unsaved, so the panel labelled live mappings "(unsaved)".
func TestLookupLogIntegrationConfigs_FoundWithNilResolverDto(t *testing.T) {
	patchIntegrationLookup(t, "pinot", "user", nil, []core.IntegrationDto{
		{Id: "int-user", Source: "user", Type: "pinot", Configs: []core.IntegrationConfigValue{{Name: "marker", Value: "user"}}},
	})

	_, found := lookupLogIntegrationConfigs(newLogLabelCtx(), "acc-1", "pinot", "user")
	assert.True(t, found, "a nil resolver DTO must not be read as 'no integration'")
}

// TestLookupLogIntegrationConfigs_NoIntegration keeps the negative honest: with nothing
// listed, found must be false rather than defaulting to the first row of an empty slice.
func TestLookupLogIntegrationConfigs_NoIntegration(t *testing.T) {
	patchIntegrationLookup(t, "pinot", "user", nil, nil)

	configs, found := lookupLogIntegrationConfigs(newLogLabelCtx(), "acc-1", "pinot", "user")
	assert.False(t, found)
	assert.Nil(t, configs)
}

// TestReadLogIntegrationConfigValue_MissingNameIsEmpty verifies a found integration that
// simply has not set this key reads as empty rather than as some other key's value.
func TestReadLogIntegrationConfigValue_MissingNameIsEmpty(t *testing.T) {
	patchIntegrationLookup(t, "pinot", "user", nil, []core.IntegrationDto{
		{Id: "int-user", Source: "user", Type: "pinot", Configs: []core.IntegrationConfigValue{{Name: "url", Value: "http://pinot:9000"}}},
	})

	assert.Empty(t, readLogIntegrationConfigValue(newLogLabelCtx(), "acc-1", "pinot", "user", core.LogLabelMappingsConfigName))
}
