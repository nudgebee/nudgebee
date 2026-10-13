package observability

import (
	"errors"
	"testing"

	"nudgebee/services/account"
	"nudgebee/services/cloud"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// builtInState is what core.AgentIntegrationState reports for the account: whether
// the agent's own provider is configured there, and whether its agent reports it up.
type builtInState struct{ configured, usable bool }

func TestGetLogsMetricsTracesProvider_SingleActiveProviderDefault(t *testing.T) {
	promURL := "http://prometheus:9090"
	traceProvider := "otel_clickhouse"
	connectedAgent := account.AgentDetails{
		Status: "CONNECTED",
		Features: account.AgentDetailsFeatures{
			PrometheusUrl: &promURL,
		},
	}
	// The cached agent details can lag the agent row by minutes.
	staleAgent := connectedAgent
	staleAgent.Status = "NOT_CONNECTED"
	tracingAgent := connectedAgent
	tracingAgent.Features.TraceProvider = &traceProvider

	up := builtInState{configured: true, usable: true}
	down := builtInState{configured: true, usable: false}
	notConfigured := builtInState{}

	agentProm := core.IntegrationDto{Id: "int-1", Name: "prometheus", Type: "prometheus", Source: "agent"}
	// A datasource the relay registered: same "agent" source, its own name.
	relayProm := core.IntegrationDto{Id: "int-5", Name: "prometheus-vm", Type: "prometheus", Source: "agent"}
	userDatadog := core.IntegrationDto{Id: "int-2", Name: "datadog-user", Type: "datadog", Source: "user"}
	userNewRelic := core.IntegrationDto{Id: "int-3", Name: "newrelic-user", Type: "newrelic", Source: "user"}

	tests := []struct {
		name               string
		providerType       string
		defaultIntegration *core.IntegrationDto
		activeIntegrations []core.IntegrationDto
		agentDetails       *account.AgentDetails
		agentErr           error
		builtIn            builtInState
		noBuiltInCheck     bool
		foragerRegistered  bool // the relay-registered row is reached through the forager (vm_agent + datasource_key)
		cloudProvider      string
		expectErr          bool
		expectedErrSubstr  string
		expectedProvider   string
		expectedSource     string
	}{
		{
			name:               "agent provider up -> provider is prometheus",
			activeIntegrations: []core.IntegrationDto{agentProm},
			agentDetails:       &connectedAgent,
			builtIn:            up,
			expectedProvider:   "prometheus",
			expectedSource:     "agent",
		},
		{
			name:               "agent provider up AND a hand-added datadog -> agent prometheus takes priority",
			activeIntegrations: []core.IntegrationDto{agentProm, userDatadog},
			agentDetails:       &connectedAgent,
			builtIn:            up,
			expectedProvider:   "prometheus",
			expectedSource:     "agent",
		},
		{
			// Whether the agent is connected is read with the provider's state, not from
			// the cached details, so a stale cache cannot hide a provider that is up.
			name:               "agent provider up while the cached agent status is stale -> provider is prometheus",
			activeIntegrations: []core.IntegrationDto{agentProm},
			agentDetails:       &staleAgent,
			builtIn:            up,
			expectedProvider:   "prometheus",
			expectedSource:     "agent",
		},
		{
			name:               "single hand-added datadog without agent -> provider is datadog",
			activeIntegrations: []core.IntegrationDto{userDatadog},
			agentErr:           errors.New("no agent connected"),
			expectedProvider:   "datadog",
			expectedSource:     "user",
		},
		{
			name: "two hand-added integrations of same type without agent -> choice error naming both",
			activeIntegrations: []core.IntegrationDto{
				{Id: "int-1", Name: "prometheus-prod", Type: "prometheus", Source: "user"},
				{Id: "int-2", Name: "prometheus-staging", Type: "prometheus", Source: "user"},
			},
			agentErr:          errors.New("no agent connected"),
			expectErr:         true,
			expectedErrSubstr: "multiple metrics providers are configured (prometheus-prod, prometheus-staging)",
		},
		{
			name: "two different hand-added integrations without agent -> choice error naming both",
			activeIntegrations: []core.IntegrationDto{
				userDatadog,
				{Id: "int-4", Name: "prometheus-user", Type: "prometheus", Source: "user"},
			},
			agentErr:          errors.New("no agent connected"),
			expectErr:         true,
			expectedErrSubstr: "multiple metrics providers are configured (datadog-user, prometheus-user)",
		},
		{
			// Not linked to the agent's row, or a user disabled that row.
			name:               "agent provider not configured for this account, nothing else -> no provider",
			activeIntegrations: []core.IntegrationDto{},
			agentDetails:       &connectedAgent,
			builtIn:            notConfigured,
			expectedProvider:   "",
			expectedSource:     "",
		},
		{
			// The agent's row stays linked and enabled while its provider is down, so it
			// is still in the active list. It must not count as a second candidate.
			name:               "agent provider down, one hand-added provider -> the hand-added one",
			activeIntegrations: []core.IntegrationDto{agentProm, userDatadog},
			agentDetails:       &connectedAgent,
			builtIn:            down,
			expectedProvider:   "datadog",
			expectedSource:     "user",
		},
		{
			// Saying only "choose a default" here sent users to make Datadog or New
			// Relic the default for a cluster neither monitors.
			name:               "agent provider down, two hand-added providers -> error leads with the agent provider being down",
			activeIntegrations: []core.IntegrationDto{agentProm, userDatadog, userNewRelic},
			agentDetails:       &connectedAgent,
			builtIn:            down,
			expectErr:          true,
			expectedErrSubstr:  "the built-in metrics provider (prometheus) is configured on this cluster but is not reachable right now; the other metrics providers (datadog-user, newrelic-user) are not used because none is marked as default",
		},
		{
			name:               "agent provider is the account default but down, two hand-added providers -> error leads with the agent provider being down",
			defaultIntegration: &agentProm,
			activeIntegrations: []core.IntegrationDto{agentProm, userDatadog, userNewRelic},
			agentDetails:       &connectedAgent,
			builtIn:            down,
			expectErr:          true,
			expectedErrSubstr:  "the built-in metrics provider (prometheus) is configured on this cluster but is not reachable right now; the other metrics providers (datadog-user, newrelic-user)",
		},
		{
			name:               "agent provider not configured, two hand-added providers -> choice error naming both",
			activeIntegrations: []core.IntegrationDto{userDatadog, userNewRelic},
			agentDetails:       &connectedAgent,
			builtIn:            notConfigured,
			expectErr:          true,
			expectedErrSubstr:  "multiple metrics providers are configured (datadog-user, newrelic-user), but none is marked as default",
		},
		{
			name:               "agent provider down, nothing else -> error says it is configured but not reachable",
			activeIntegrations: []core.IntegrationDto{agentProm},
			agentDetails:       &connectedAgent,
			builtIn:            down,
			expectErr:          true,
			expectedErrSubstr:  "the built-in metrics provider (prometheus) is configured on this cluster but is not reachable right now",
		},
		{
			// The collector marks the agent's provider as the account default whenever
			// it is up, so this is the usual shape of a cluster whose provider then fails.
			name:               "agent provider is the account default but down, one hand-added provider -> the hand-added one",
			defaultIntegration: &agentProm,
			activeIntegrations: []core.IntegrationDto{agentProm, userDatadog},
			agentDetails:       &connectedAgent,
			builtIn:            down,
			expectedProvider:   "datadog",
			expectedSource:     "user",
		},
		{
			name:               "agent provider is the account default and up -> provider is prometheus",
			defaultIntegration: &agentProm,
			activeIntegrations: []core.IntegrationDto{agentProm, userDatadog},
			agentDetails:       &connectedAgent,
			builtIn:            up,
			expectedProvider:   "prometheus",
			expectedSource:     "agent",
		},
		{
			name:               "hand-added default -> used without asking about the agent's provider",
			defaultIntegration: &userDatadog,
			activeIntegrations: []core.IntegrationDto{agentProm, userDatadog},
			agentDetails:       &connectedAgent,
			builtIn:            down,
			noBuiltInCheck:     true,
			expectedProvider:   "datadog",
			expectedSource:     "user",
		},
		{
			// Only the agent's own provider follows the agent's health flags.
			name:               "relay-registered datasource is the account default -> used without asking about the agent's provider",
			defaultIntegration: &relayProm,
			activeIntegrations: []core.IntegrationDto{relayProm},
			agentErr:           errors.New("no agent connected"),
			noBuiltInCheck:     true,
			expectedProvider:   "prometheus",
			expectedSource:     "agent",
		},
		{
			name:               "relay-registered datasource is the only provider -> still picked from the list",
			activeIntegrations: []core.IntegrationDto{relayProm},
			agentErr:           errors.New("no agent connected"),
			expectedProvider:   "prometheus",
			expectedSource:     "agent",
		},
		{
			// A VM account has no agent; its forager registers the datasource, and that
			// row is served like a hand-added connection.
			name:               "forager-registered datasource is the account default -> served as a user source",
			defaultIntegration: &relayProm,
			activeIntegrations: []core.IntegrationDto{relayProm},
			agentErr:           errors.New("no agent connected"),
			foragerRegistered:  true,
			noBuiltInCheck:     true,
			expectedProvider:   "prometheus",
			expectedSource:     "user",
		},
		{
			name:               "forager-registered datasource is the only provider -> served as a user source",
			activeIntegrations: []core.IntegrationDto{relayProm},
			agentErr:           errors.New("no agent connected"),
			foragerRegistered:  true,
			expectedProvider:   "prometheus",
			expectedSource:     "user",
		},
		{
			name:               "no integrations and no agent -> returns lookup error",
			activeIntegrations: []core.IntegrationDto{},
			agentErr:           errors.New("no agent connected"),
			expectErr:          true,
		},
		{
			// Cloud Trace must win over the "configured but not reachable" error.
			name:               "GCP account, traces, agent trace provider down, nothing else -> Cloud Trace",
			providerType:       "traces",
			activeIntegrations: []core.IntegrationDto{},
			agentDetails:       &tracingAgent,
			builtIn:            down,
			cloudProvider:      "GCP",
			expectedProvider:   "gcp",
			expectedSource:     "user",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := gomonkey.NewPatches()
			defer patches.Reset()

			// 1. The integration flagged as the account's default for this signal, if any
			patches.ApplyFunc(core.GetIntegrationByConfigNameValues,
				func(_ *security.RequestContext, _, _, _ string) (*core.IntegrationDto, error) {
					return tt.defaultIntegration, nil
				})

			// 2. Active integrations linked to account
			patches.ApplyFunc(core.ListActiveIntegrationsForAccount,
				func(_ *security.RequestContext, _ string) ([]core.IntegrationDto, error) {
					return tt.activeIntegrations, nil
				})

			// 3. Agent connection details
			patches.ApplyFunc(account.GetAgentConnectionDetails,
				func(_ string) (account.AgentDetails, error) {
					if tt.agentErr != nil {
						return account.AgentDetails{}, tt.agentErr
					}
					if tt.agentDetails != nil {
						return *tt.agentDetails, nil
					}
					return account.AgentDetails{}, nil
				})

			// 4. Whether the agent's own provider is configured for the account and up
			builtInChecks := 0
			patches.ApplyFunc(core.AgentIntegrationState,
				func(_ *security.RequestContext, _, _ string, _ ...string) (bool, bool, error) {
					builtInChecks++
					return tt.builtIn.configured, tt.builtIn.usable, nil
				})

			// 4b. Whether an agent-source row under its own name is a datasource the forager registered
			stubForagerRegistered(t, func(core.IntegrationDto) (bool, error) { return tt.foragerRegistered, nil })

			// 5. Fallback source lookup
			patches.ApplyFunc(core.GetIntegrationByTypePreferringDefault,
				func(_ *security.RequestContext, _, _, _ string) (*core.IntegrationDto, error) {
					return nil, nil
				})

			// 6. Cloud provider of the account (only consulted for traces)
			patches.ApplyFunc(cloud.GetCloudAccountProvider,
				func(_, _ string) (string, error) {
					return tt.cloudProvider, nil
				})

			providerType := tt.providerType
			if providerType == "" {
				providerType = "metrics"
			}
			ctx := tenantCarryingContext("test-tenant")
			provider, source, _, err := getLogsMetricsTracesProviderWithIntegration(ctx, "acc-1", "", providerType, "")
			if tt.noBuiltInCheck {
				assert.Zero(t, builtInChecks, "a hand-added default must not depend on the agent's provider")
			}
			if tt.expectErr {
				require.Error(t, err)
				if tt.expectedErrSubstr != "" {
					assert.Contains(t, err.Error(), tt.expectedErrSubstr)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expectedProvider, provider)
			assert.Equal(t, tt.expectedSource, source)
		})
	}
}

// Chronosphere serves traces from the metrics backend, and older agents report its
// traces as off, so either flag has to count as "up" for it.
func TestBuiltInHealthFlags(t *testing.T) {
	assert.Equal(t, []string{"logsConnection"}, builtInHealthFlags("logs", "loki"))
	// logsConnection is the agent's Loki / Elasticsearch check, and the traces flag says
	// nothing about the logs table, so ClickHouse logs have a flag of their own.
	assert.Equal(t, []string{"clickhouseLogsConnection"}, builtInHealthFlags("logs", "otel_clickhouse"))
	assert.Equal(t, []string{"prometheusConnection"}, builtInHealthFlags("metrics", "prometheus"))
	assert.Equal(t, []string{"tracesEnabled"}, builtInHealthFlags("traces", "otel_clickhouse"))
	assert.Equal(t, []string{"tracesEnabled", "prometheusConnection"}, builtInHealthFlags("traces", "chronosphere"))
}

// The agent never makes ClickHouse the logs default, so a ClickHouse logs default is a
// user's pick. Where the cluster collects its logs there and ClickHouse cannot serve,
// the resolver says so instead of reading logs from Loki or any other provider in its
// place. Where it does not collect them, the pick is passed over like no default: the
// table is present on nearly every cluster but holds no container logs.
func TestGetLogsProvider_ClickHouseDefaultIsNotReplaced(t *testing.T) {
	loki := "loki"
	agent := account.AgentDetails{Status: "CONNECTED", Features: account.AgentDetailsFeatures{LogsConnectionProvider: &loki}}

	chRow := core.IntegrationDto{Id: "int-ch", Name: "otel_clickhouse", Type: "otel_clickhouse", Source: "agent"}
	agentLoki := core.IntegrationDto{Id: "int-loki", Name: "loki", Type: "loki", Source: "agent"}
	userLoki := core.IntegrationDto{Id: "int-ul", Name: "loki-hosted", Type: "loki", Source: "user"}
	userDatadog := core.IntegrationDto{Id: "int-dd", Name: "datadog-user", Type: "datadog", Source: "user"}

	tests := []struct {
		name               string
		defaultIntegration *core.IntegrationDto
		activeIntegrations []core.IntegrationDto
		usable             map[string]bool // per provider; configured for every provider listed
		collected          bool
		expectedProvider   string
		expectedErrSubstr  string
	}{
		{
			name:               "ClickHouse default up -> ClickHouse",
			defaultIntegration: &chRow,
			activeIntegrations: []core.IntegrationDto{chRow, agentLoki},
			usable:             map[string]bool{"otel_clickhouse": true, "loki": true},
			collected:          true,
			expectedProvider:   "otel_clickhouse",
		},
		{
			name:               "ClickHouse default down, the agent's Loki up -> error, not Loki",
			defaultIntegration: &chRow,
			activeIntegrations: []core.IntegrationDto{chRow, agentLoki},
			usable:             map[string]bool{"otel_clickhouse": false, "loki": true},
			collected:          true,
			expectedErrSubstr:  "the built-in logs provider (otel_clickhouse) is configured on this cluster but is not reachable right now",
		},
		{
			name:               "ClickHouse default down, one hand-added Loki -> error, not the hand-added Loki",
			defaultIntegration: &chRow,
			activeIntegrations: []core.IntegrationDto{chRow, userLoki},
			usable:             map[string]bool{"otel_clickhouse": false},
			collected:          true,
			expectedErrSubstr:  "the built-in logs provider (otel_clickhouse) is configured on this cluster but is not reachable right now",
		},
		{
			name:               "ClickHouse default up, logs not collected there -> the agent's Loki",
			defaultIntegration: &chRow,
			activeIntegrations: []core.IntegrationDto{chRow, agentLoki},
			usable:             map[string]bool{"otel_clickhouse": true, "loki": true},
			expectedProvider:   "loki",
		},
		{
			// An agent older than the collection switch, or collection turned off
			// after the pick: the pick no longer counts, so nothing is reported down.
			name:               "ClickHouse default down, logs not collected there -> the hand-added Loki",
			defaultIntegration: &chRow,
			activeIntegrations: []core.IntegrationDto{chRow, userLoki},
			usable:             map[string]bool{"otel_clickhouse": false},
			expectedProvider:   "loki",
		},
		{
			// The agent claims its own Loki as the default whenever Loki is up, so
			// this default is not a user's pick: falling back stays as it was.
			name:               "the agent's Loki default down, one hand-added provider -> the hand-added one",
			defaultIntegration: &agentLoki,
			activeIntegrations: []core.IntegrationDto{agentLoki, userDatadog},
			usable:             map[string]bool{"loki": false},
			expectedProvider:   "datadog",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patches := gomonkey.NewPatches()
			defer patches.Reset()

			patches.ApplyFunc(core.GetIntegrationByConfigNameValues,
				func(_ *security.RequestContext, _, _, _ string) (*core.IntegrationDto, error) {
					return tt.defaultIntegration, nil
				})
			patches.ApplyFunc(core.ListActiveIntegrationsForAccount,
				func(_ *security.RequestContext, _ string) ([]core.IntegrationDto, error) {
					return tt.activeIntegrations, nil
				})
			patches.ApplyFunc(account.GetAgentConnectionDetails,
				func(_ string) (account.AgentDetails, error) { return agent, nil })
			patches.ApplyFunc(core.AgentIntegrationState,
				func(_ *security.RequestContext, _, provider string, flags ...string) (bool, bool, error) {
					if provider == "otel_clickhouse" {
						assert.Equal(t, []string{"clickhouseLogsConnection"}, flags)
					}
					usable, configured := tt.usable[provider]
					return configured, usable, nil
				})
			withClickHouseLogsCollected(t, tt.collected)

			provider, source, _, err := getLogsMetricsTracesProviderWithIntegration(tenantCarryingContext("test-tenant"), "acc-1", "", "logs", "")
			if tt.expectedErrSubstr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedErrSubstr)
				var downErr *BuiltInProviderDownError
				require.ErrorAs(t, err, &downErr, "the status view reads the provider off the error")
				assert.Equal(t, "otel_clickhouse", downErr.Provider)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expectedProvider, provider)
			if tt.expectedProvider == "otel_clickhouse" {
				assert.Equal(t, "agent", source)
			}
		})
	}
}

// withClickHouseLogsCollected answers whether the cluster collects its logs into the
// agent's ClickHouse, for the rest of the test.
func withClickHouseLogsCollected(t *testing.T, collected bool) {
	t.Helper()
	prev := clickHouseLogsCollected
	clickHouseLogsCollected = func(_ *security.RequestContext, _ string) (bool, error) { return collected, nil }
	t.Cleanup(func() { clickHouseLogsCollected = prev })
}

// A caller that names the agent's ClickHouse for logs, as the logs page and nubi can,
// gets it only where the cluster collects its logs there; an empty table would read as
// "no errors in the logs".
func TestGetLogsProvider_NamedClickHouseNeedsCollection(t *testing.T) {
	for _, collected := range []bool{true, false} {
		withClickHouseLogsCollected(t, collected)
		provider, source, _, err := getLogsMetricsTracesProviderWithIntegration(tenantCarryingContext("test-tenant"), "acc-1", "otel_clickhouse", "logs", "agent")
		if !collected {
			require.ErrorIs(t, err, errClickHouseLogsNotCollected)
			continue
		}
		require.NoError(t, err)
		assert.Equal(t, "otel_clickhouse", provider)
		assert.Equal(t, "agent", source)
	}

	// Not knowing whether logs are collected is an error, not a guess either way.
	lookupErr := errors.New("metastore down")
	prev := clickHouseLogsCollected
	clickHouseLogsCollected = func(_ *security.RequestContext, _ string) (bool, error) { return false, lookupErr }
	t.Cleanup(func() { clickHouseLogsCollected = prev })
	_, _, _, err := getLogsMetricsTracesProviderWithIntegration(tenantCarryingContext("test-tenant"), "acc-1", "otel_clickhouse", "logs", "agent")
	require.ErrorIs(t, err, lookupErr)
}

// ClickHouse is offered as a logs provider only where the cluster collects its logs
// there. It stays offered for traces either way.
func TestListAvailableProviders_ClickHouseLogsNeedCollection(t *testing.T) {
	chRow := core.IntegrationDto{Id: "int-ch", Name: "otel_clickhouse", Type: "otel_clickhouse", Source: "agent"}
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyFunc(core.ListActiveIntegrationsForAccount,
		func(_ *security.RequestContext, _ string) ([]core.IntegrationDto, error) {
			return []core.IntegrationDto{chRow}, nil
		})
	patches.ApplyFunc(account.GetAgentConnectionDetails,
		func(_ string) (account.AgentDetails, error) { return account.AgentDetails{}, errors.New("no agent") })

	names := func(providerType string) []string {
		var out []string
		for _, p := range listAvailableProviders(tenantCarryingContext("test-tenant"), "acc-1", providerType) {
			out = append(out, p.Provider)
		}
		return out
	}

	withClickHouseLogsCollected(t, false)
	assert.Empty(t, names("logs"))
	assert.Equal(t, []string{"otel_clickhouse"}, names("traces"))

	withClickHouseLogsCollected(t, true)
	assert.Equal(t, []string{"otel_clickhouse"}, names("logs"))
}

// The no-fallback rule is for logs only. Chronosphere serves traces from the metrics
// backend, so a Chronosphere cluster whose otel_clickhouse traces default blips must
// still fall back to chronosphere.
func TestGetTracesProvider_ChronosphereFallbackUnchanged(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	chronoURL := "https://tenant.chronosphere.io/data/metrics"
	traceProvider := "otel_clickhouse"
	tracesOff := false
	promUp := true
	agent := account.AgentDetails{Status: "CONNECTED", Features: account.AgentDetailsFeatures{
		PrometheusUrl: &chronoURL, TraceProvider: &traceProvider, TracesEnabled: &tracesOff, PrometheusConnection: &promUp,
	}}
	chRow := core.IntegrationDto{Id: "int-ch", Name: "otel_clickhouse", Type: "otel_clickhouse", Source: "agent"}

	patches.ApplyFunc(core.GetIntegrationByConfigNameValues,
		func(_ *security.RequestContext, _, _, _ string) (*core.IntegrationDto, error) { return &chRow, nil })
	patches.ApplyFunc(core.ListActiveIntegrationsForAccount,
		func(_ *security.RequestContext, _ string) ([]core.IntegrationDto, error) {
			return []core.IntegrationDto{chRow}, nil
		})
	patches.ApplyFunc(account.GetAgentConnectionDetails,
		func(_ string) (account.AgentDetails, error) { return agent, nil })
	patches.ApplyFunc(core.AgentIntegrationState,
		func(_ *security.RequestContext, _, provider string, _ ...string) (bool, bool, error) {
			// otel_clickhouse traces down; chronosphere up through prometheusConnection.
			return true, provider == "chronosphere", nil
		})
	patches.ApplyFunc(cloud.GetCloudAccountProvider,
		func(_, _ string) (string, error) { return "K8s", nil })

	provider, source, _, err := getLogsMetricsTracesProviderWithIntegration(tenantCarryingContext("test-tenant"), "acc-1", "", "traces", "")
	require.NoError(t, err)
	assert.Equal(t, "chronosphere", provider)
	assert.Equal(t, "agent", source)
}

func TestGetTelemetrySourceForAccount_AmbiguousProviderError(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(core.GetIntegrationByConfigNameValues,
		func(_ *security.RequestContext, _, _, _ string) (*core.IntegrationDto, error) {
			return nil, nil
		})

	patches.ApplyFunc(account.GetAgentConnectionDetails,
		func(_ string) (account.AgentDetails, error) {
			return account.AgentDetails{}, errors.New("no agent connected")
		})

	patches.ApplyFunc(core.GetIntegrationByTypePreferringDefault,
		func(_ *security.RequestContext, _, _, _ string) (*core.IntegrationDto, error) {
			return nil, nil
		})

	ctx := tenantCarryingContext("test-tenant")

	// 1. Metrics query with ambiguous providers (two prometheus integrations)
	patches.ApplyFunc(core.ListActiveIntegrationsForAccount,
		func(_ *security.RequestContext, _ string) ([]core.IntegrationDto, error) {
			return []core.IntegrationDto{
				{Id: "int-1", Name: "prometheus-prod", Type: "prometheus", Source: "user"},
				{Id: "int-2", Name: "prometheus-staging", Type: "prometheus", Source: "user"},
			}, nil
		})
	_, err := getMetricsSourceForAccount(ctx, "acc-1", "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "observability: multiple metrics providers are configured (prometheus-prod, prometheus-staging), but none is marked as default")
	assert.Contains(t, err.Error(), "please specify metrics_provider or choose a default in Settings")

	// 2. Logs query when only datadog is configured without agent -> auto-defaults to datadog (no error)
	patches.ApplyFunc(core.ListActiveIntegrationsForAccount,
		func(_ *security.RequestContext, _ string) ([]core.IntegrationDto, error) {
			return []core.IntegrationDto{
				{Id: "int-1", Name: "datadog-logs", Type: "datadog", Source: "user"},
			}, nil
		})
	logSource, err := getLogSourceForAccount(ctx, "acc-1", "", "")
	require.NoError(t, err)
	assert.NotNil(t, logSource)

	// 3. Traces query when agent is connected without trace provider and only prometheus integration exists -> returns standard required error
	patches.ApplyFunc(account.GetAgentConnectionDetails,
		func(_ string) (account.AgentDetails, error) {
			return account.AgentDetails{Status: "CONNECTED"}, nil
		})
	patches.ApplyFunc(core.ListActiveIntegrationsForAccount,
		func(_ *security.RequestContext, _ string) ([]core.IntegrationDto, error) {
			return []core.IntegrationDto{
				{Id: "int-1", Name: "prometheus-prod", Type: "prometheus", Source: "user"},
			}, nil
		})
	_, err = getTraceSourceForAccount(ctx, "acc-1", "", "")
	require.Error(t, err)
	assert.Equal(t, "observability: trace provider (trace_provider) is required", err.Error())

	// 4. Multiple logs providers (datadog + loki) -> returns ambiguous error naming both
	patches.ApplyFunc(core.ListActiveIntegrationsForAccount,
		func(_ *security.RequestContext, _ string) ([]core.IntegrationDto, error) {
			return []core.IntegrationDto{
				{Id: "int-1", Name: "datadog-prod", Type: "datadog", Source: "user"},
				{Id: "int-2", Name: "loki-prod", Type: "loki", Source: "user"},
			}, nil
		})
	_, err = getLogSourceForAccount(ctx, "acc-1", "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "observability: multiple logs providers are configured (datadog-prod, loki-prod), but none is marked as default")
	assert.Contains(t, err.Error(), "please specify log_provider or choose a default in Settings")

	// 5. Multiple traces providers (datadog + jaeger) -> returns ambiguous error naming both
	patches.ApplyFunc(core.ListActiveIntegrationsForAccount,
		func(_ *security.RequestContext, _ string) ([]core.IntegrationDto, error) {
			return []core.IntegrationDto{
				{Id: "int-1", Name: "datadog-prod", Type: "datadog", Source: "user"},
				{Id: "int-2", Name: "jaeger-prod", Type: "jaeger", Source: "user"},
			}, nil
		})
	_, err = getTraceSourceForAccount(ctx, "acc-1", "", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "observability: multiple traces providers are configured (datadog-prod, jaeger-prod), but none is marked as default")
	assert.Contains(t, err.Error(), "please specify trace_provider or choose a default in Settings")
}

// A vm_agent integration is echoed back by the forager as an agent-sourced row with the
// same type and name. That row is the same log store, so it must not make the account
// look like it has two providers.
func TestGetTelemetrySourceForAccount_ForagerEchoIsNotASecondProvider(t *testing.T) {
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(core.GetIntegrationByConfigNameValues,
		func(_ *security.RequestContext, _, _, _ string) (*core.IntegrationDto, error) {
			return nil, nil
		})
	patches.ApplyFunc(account.GetAgentConnectionDetails,
		func(_ string) (account.AgentDetails, error) {
			return account.AgentDetails{}, errors.New("no agent connected")
		})
	patches.ApplyFunc(core.GetIntegrationByTypePreferringDefault,
		func(_ *security.RequestContext, _, _, _ string) (*core.IntegrationDto, error) {
			return nil, nil
		})
	ctx := tenantCarryingContext("test-tenant")

	t.Run("user row and its forager echo resolve to the user row", func(t *testing.T) {
		patches.ApplyFunc(core.ListActiveIntegrationsForAccount,
			func(_ *security.RequestContext, _ string) ([]core.IntegrationDto, error) {
				return []core.IntegrationDto{
					{Id: "agent-row", Name: "vm-loki", Type: "loki", Source: "agent"},
					{Id: "user-row", Name: "vm-loki", Type: "loki", Source: "user"},
				}, nil
			})
		provider, source, matched, err := resolveProviderForStatus(ctx, "acc-1", "", "logs", "")
		require.NoError(t, err)
		assert.Equal(t, "loki", provider)
		assert.Equal(t, "user", source)
		require.NotNil(t, matched)
		assert.Equal(t, "user-row", matched.Id)
	})

	t.Run("a differently named agent row is still a second provider", func(t *testing.T) {
		patches.ApplyFunc(core.ListActiveIntegrationsForAccount,
			func(_ *security.RequestContext, _ string) ([]core.IntegrationDto, error) {
				return []core.IntegrationDto{
					{Id: "agent-row", Name: "team-b-loki", Type: "loki", Source: "agent"},
					{Id: "user-row", Name: "vm-loki", Type: "loki", Source: "user"},
				}, nil
			})
		_, _, _, err := resolveProviderForStatus(ctx, "acc-1", "", "logs", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "multiple logs providers are configured (team-b-loki, vm-loki), but none is marked as default")
	})
}
