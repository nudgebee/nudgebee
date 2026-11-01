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
			name:               "agent provider down, two hand-added providers -> choice error naming only those",
			activeIntegrations: []core.IntegrationDto{agentProm, userDatadog, userNewRelic},
			agentDetails:       &connectedAgent,
			builtIn:            down,
			expectErr:          true,
			expectedErrSubstr:  "multiple metrics providers are configured (datadog-user, newrelic-user)",
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
	assert.Equal(t, []string{"prometheusConnection"}, builtInHealthFlags("metrics", "prometheus"))
	assert.Equal(t, []string{"tracesEnabled"}, builtInHealthFlags("traces", "otel_clickhouse"))
	assert.Equal(t, []string{"tracesEnabled", "prometheusConnection"}, builtInHealthFlags("traces", "chronosphere"))
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
