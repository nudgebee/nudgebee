package observability

import (
	"errors"
	"fmt"
	"testing"

	"nudgebee/services/account"
	"nudgebee/services/integrations/core"
	"nudgebee/services/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type resolveFn = func(*security.RequestContext, string, string, string, string) (string, string, *core.IntegrationDto, error)

// stubVMStatus swaps every seam CheckVMMetricsStatus uses and restores them afterwards.
func stubVMStatus(t *testing.T, resolve resolveFn,
	probe func(ctx *security.RequestContext, integrationID, accountID string) error,
	agents func(*security.RequestContext, string, string, string) ([]vmAgent, error),
	inventory func(*security.RequestContext, string) ([]vmInventoryEntry, error),
) {
	t.Helper()
	oResolve, oProbe, oAgents, oInv := resolveProviderForStatus, probeIntegrationForAccount, fetchVMAgents, listVMInventory
	t.Cleanup(func() {
		resolveProviderForStatus, probeIntegrationForAccount, fetchVMAgents, listVMInventory = oResolve, oProbe, oAgents, oInv
	})
	resolveProviderForStatus, probeIntegrationForAccount, fetchVMAgents, listVMInventory = resolve, probe, agents, inventory
}

func userProm() *core.IntegrationDto { return &core.IntegrationDto{Id: "int-1", Name: "vm-prom"} }

func resolveUserProm(*security.RequestContext, string, string, string, string) (string, string, *core.IntegrationDto, error) {
	return "prometheus", "user", userProm(), nil
}

func noProbe(t *testing.T) func(*security.RequestContext, string, string) error {
	return func(*security.RequestContext, string, string) error {
		t.Fatal("must not run the connection test")
		return nil
	}
}

func agentsOf(as ...vmAgent) func(*security.RequestContext, string, string, string) ([]vmAgent, error) {
	return func(*security.RequestContext, string, string, string) ([]vmAgent, error) { return as, nil }
}

func inventoryOf(vms ...vmInventoryEntry) func(*security.RequestContext, string) ([]vmInventoryEntry, error) {
	return func(*security.RequestContext, string) ([]vmInventoryEntry, error) { return vms, nil }
}

func TestCheckVMMetricsStatus_RequiresAccount(t *testing.T) {
	_, err := CheckVMMetricsStatus(nil, "")
	require.Error(t, err)
}

func TestCheckVMMetricsStatus_NotConfigured(t *testing.T) {
	t.Run("the account has no agent and no integration", func(t *testing.T) {
		stubVMStatus(t,
			func(*security.RequestContext, string, string, string, string) (string, string, *core.IntegrationDto, error) {
				return "", "", nil, fmt.Errorf("lookup: %w", account.ErrAgentNotConnected)
			}, noProbe(t), nil, nil)
		st, err := CheckVMMetricsStatus(nil, "acc")
		require.NoError(t, err)
		assert.Equal(t, VMMetricsNotConfigured, st.State)
		assert.NotNil(t, st.Reporting, "reporting must serialise as [] not null")
	})
	t.Run("only a Kubernetes agent serves it", func(t *testing.T) {
		stubVMStatus(t,
			func(*security.RequestContext, string, string, string, string) (string, string, *core.IntegrationDto, error) {
				return "prometheus", "agent", userProm(), nil
			}, noProbe(t), nil, nil)
		st, _ := CheckVMMetricsStatus(nil, "acc")
		assert.Equal(t, VMMetricsNotConfigured, st.State)
	})
}

func TestCheckVMMetricsStatus_ResolverFailureIsNotReportedAsNotConfigured(t *testing.T) {
	// "several providers, none marked default" and database errors both mean the user HAS an
	// integration. Telling them to add one is the wrong advice; the reason must reach the card.
	for name, resolveErr := range map[string]error{
		"database error":  errors.New("connection refused"),
		"no default set":  errors.New("observability: multiple metrics providers are configured (a, b), but none is marked as default"),
		"no tenant scope": ErrNoTenantContext,
	} {
		t.Run(name, func(t *testing.T) {
			stubVMStatus(t,
				func(*security.RequestContext, string, string, string, string) (string, string, *core.IntegrationDto, error) {
					return "", "", nil, resolveErr
				}, noProbe(t), nil, nil)
			st, err := CheckVMMetricsStatus(nil, "acc")
			require.NoError(t, err)
			assert.Equal(t, VMMetricsCheckFailed, st.State)
			assert.Equal(t, resolveErr.Error(), st.Error)
		})
	}
}

func TestCheckVMMetricsStatus_ConnectionTestRunsThroughThisAccount(t *testing.T) {
	// The plain test runs through the integration's first linked account, which for a store
	// reached through a forager decides whose forager answers. Account B must be tested as B.
	var gotIntegration, gotAccount string
	stubVMStatus(t, resolveUserProm,
		func(_ *security.RequestContext, integrationID, accountID string) error {
			gotIntegration, gotAccount = integrationID, accountID
			return errors.New("forager cannot reach 10.0.0.5:9090")
		},
		func(*security.RequestContext, string, string, string) ([]vmAgent, error) {
			return nil, errors.New("query failed")
		},
		inventoryOf())
	st, _ := CheckVMMetricsStatus(nil, "acc-B")
	assert.Equal(t, "int-1", gotIntegration)
	assert.Equal(t, "acc-B", gotAccount)
	assert.Equal(t, VMMetricsUnreachable, st.State)
	assert.Equal(t, "forager cannot reach 10.0.0.5:9090", st.Error, "the test's cause beats the query's generic error")
}

func TestCheckVMMetricsStatus_QueryFirstThenTestOnlyOnFailure(t *testing.T) {
	t.Run("a store that answers is not tested again", func(t *testing.T) {
		stubVMStatus(t, resolveUserProm, noProbe(t), agentsOf(vmAgent{Instance: "a", InstanceID: "i-1"}), inventoryOf(vmInventoryEntry{ResourceID: "i-1"}))
		st, _ := CheckVMMetricsStatus(nil, "acc")
		assert.Equal(t, VMMetricsConnected, st.State)
	})
	t.Run("an empty answer is still an answer", func(t *testing.T) {
		stubVMStatus(t, resolveUserProm, noProbe(t), agentsOf(), inventoryOf(vmInventoryEntry{ResourceID: "i-1"}))
		st, _ := CheckVMMetricsStatus(nil, "acc")
		assert.Equal(t, VMMetricsNoData, st.State)
	})
	t.Run("a failing query falls back to the test, and keeps the query error if the test passes", func(t *testing.T) {
		stubVMStatus(t, resolveUserProm,
			func(*security.RequestContext, string, string) error { return nil },
			func(*security.RequestContext, string, string, string) ([]vmAgent, error) {
				return nil, errors.New("query timed out")
			},
			inventoryOf())
		st, _ := CheckVMMetricsStatus(nil, "acc")
		assert.Equal(t, VMMetricsUnreachable, st.State)
		assert.Equal(t, "query timed out", st.Error)
	})
}

func TestCheckVMMetricsStatus_CountsOnlyThisAccountsVMs(t *testing.T) {
	// Three agents write to the shared store; the account owns two of them.
	stubVMStatus(t, resolveUserProm, noProbe(t),
		agentsOf(
			vmAgent{Instance: "a", Hostname: "web-1", InstanceID: "i-1", LocalIP: "10.0.0.1"},
			vmAgent{Instance: "b", Hostname: "web-2", InstanceID: "i-2", LocalIP: "10.0.0.2"},
			vmAgent{Instance: "c", Hostname: "other-acct", InstanceID: "i-9", LocalIP: "10.9.9.9"},
		),
		inventoryOf(
			vmInventoryEntry{ResourceID: "i-1", Name: "web-1"},
			vmInventoryEntry{ResourceID: "i-2", Name: "web-2"},
			vmInventoryEntry{ResourceID: "i-3", Name: "web-3"}, // owned but not writing
		))
	st, _ := CheckVMMetricsStatus(nil, "acc")
	assert.Equal(t, VMMetricsConnected, st.State)
	assert.Equal(t, VMScopeAccount, st.Scope)
	assert.Equal(t, 3, st.Inventory)
	assert.Equal(t, 1, st.OtherAgents)
	require.Len(t, st.Reporting, 2)
	assert.Equal(t, "web-1", st.Reporting[0].Hostname)
	assert.Equal(t, "web-2", st.Reporting[1].Hostname)
}

func TestCheckVMMetricsStatus_OtherAccountsAgentsDoNotMakeThisOneConnected(t *testing.T) {
	stubVMStatus(t, resolveUserProm, noProbe(t),
		agentsOf(vmAgent{Instance: "c", Hostname: "other-acct", InstanceID: "i-9"}),
		inventoryOf(vmInventoryEntry{ResourceID: "i-1", Name: "mine"}))
	st, _ := CheckVMMetricsStatus(nil, "acc")
	assert.Equal(t, VMMetricsNoData, st.State)
	assert.Empty(t, st.Reporting)
	assert.Equal(t, 1, st.OtherAgents, "the card can say agents write to the store, just not this account's")
}

func TestCheckVMMetricsStatus_NoInventoryFallsBackToTheStore(t *testing.T) {
	agent := vmAgent{Instance: "a", Hostname: "web-1"}
	t.Run("an empty inventory", func(t *testing.T) {
		stubVMStatus(t, resolveUserProm, noProbe(t), agentsOf(agent), inventoryOf())
		st, _ := CheckVMMetricsStatus(nil, "acc")
		assert.Equal(t, VMMetricsConnected, st.State)
		assert.Equal(t, VMScopeStore, st.Scope, "the answer is about the store, and says so")
		assert.Len(t, st.Reporting, 1)
	})
	t.Run("an inventory that cannot be read", func(t *testing.T) {
		stubVMStatus(t, resolveUserProm, noProbe(t), agentsOf(agent),
			func(*security.RequestContext, string) ([]vmInventoryEntry, error) { return nil, errors.New("db down") })
		st, _ := CheckVMMetricsStatus(security.NewRequestContextForSuperAdmin(nil, nil, nil), "acc")
		assert.Equal(t, VMMetricsCheckFailed, st.State, "a read error is not an empty inventory")
		assert.Contains(t, st.Error, "db down")
		assert.Empty(t, st.Reporting)
	})
}

func TestAttributeVMAgents(t *testing.T) {
	agents := []vmAgent{
		{Instance: "A", Hostname: "web", InstanceID: "i-1", LocalIP: "10.0.0.1"},
		{Instance: "B", Hostname: "web", InstanceID: "i-2", LocalIP: "10.0.0.2"}, // a clone: same hostname
		{Instance: "C", Hostname: "db", LocalIP: "10.0.0.3"},                     // not in a cloud: no instance id
	}
	instances := func(got []vmAgent) []string {
		var out []string
		for _, a := range got {
			out = append(out, a.Instance)
		}
		return out
	}

	assert.Equal(t, []string{"A"}, instances(attributeVMAgents(agents, []vmInventoryEntry{{ResourceID: "i-1"}})), "by cloud instance id")
	assert.Equal(t, []string{"C"}, instances(attributeVMAgents(agents, []vmInventoryEntry{{ResourceID: "vm-10.0.0.3", IP: "10.0.0.3"}})), "by private IP when there is no instance id")
	assert.Equal(t, []string{"C"}, instances(attributeVMAgents(agents, []vmInventoryEntry{{Name: "db"}})), "by hostname")
	assert.Empty(t, attributeVMAgents(agents, []vmInventoryEntry{{Name: "web"}}), "a hostname two clones share names neither")
	assert.Equal(t, []string{"A", "B"}, instances(attributeVMAgents(agents, []vmInventoryEntry{{ResourceID: "i-1", Name: "web"}, {ResourceID: "i-2", Name: "web"}})),
		"the stronger identifier tells clones apart")
	assert.Empty(t, attributeVMAgents(agents, []vmInventoryEntry{{ResourceID: "i-404", Name: "nope", IP: "1.2.3.4"}}))
	assert.Empty(t, attributeVMAgents(agents, []vmInventoryEntry{{}}), "an inventory row with no identifiers matches nothing")
	assert.Equal(t, []string{"A"}, instances(attributeVMAgents(agents, []vmInventoryEntry{{ResourceID: "i-1"}, {ResourceID: "i-1"}})), "one agent is counted once")
}

func TestVMAgentsFromResult_MergesLabelsPerMachine(t *testing.T) {
	out := OutputMetricQuery{Results: []QueryResult{
		{QueryKey: "vm_agent_info", Payload: []Result{
			{Metric: map[string]string{"instance": "b", "hostname": "web-2"}},
			{Metric: map[string]string{"instance": "a", "hostname": "web-1"}},
			{Metric: map[string]string{"hostname": "no-instance"}}, // unusable
		}},
		{QueryKey: "vm_agent_identity", Payload: []Result{
			{Metric: map[string]string{"instance": "a", "instance_id": "i-1", "local_ipv4": "10.0.0.1"}},
		}},
	}}
	got, err := vmAgentsFromResult(out)
	require.NoError(t, err)
	assert.Equal(t, []vmAgent{
		{Instance: "a", Hostname: "web-1", InstanceID: "i-1", LocalIP: "10.0.0.1"},
		{Instance: "b", Hostname: "web-2"},
	}, got)

	empty, err := vmAgentsFromResult(OutputMetricQuery{})
	require.NoError(t, err)
	assert.NotNil(t, empty)
	assert.Empty(t, empty)
}

func TestVMAgentsFromResult_QueryErrorIsNotNoAgents(t *testing.T) {
	msg := "parse error"
	_, err := vmAgentsFromResult(OutputMetricQuery{Results: []QueryResult{{QueryKey: "vm_agent_info", Error: &msg}}})
	require.EqualError(t, err, "parse error")
}

func TestVMAgentQueries_AreValidPromQL(t *testing.T) {
	for name, q := range map[string]string{"info": vmAgentInfoQuery, "identity": vmAgentIdentityQuery} {
		_, err := promQLParser.ParseExpr(q)
		require.NoError(t, err, name)
	}
}
