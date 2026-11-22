package observability

import (
	"errors"
	"fmt"
	"sort"

	"nudgebee/services/account"
	"nudgebee/services/integrations/core"
	"nudgebee/services/internal/database"
	"nudgebee/services/security"
)

// VM metrics connection status.
//
// A self-hosted VM account has no Kubernetes agent whose own telemetry can vouch
// for its metrics, and CheckProviderStatus stamps its result onto that agent row, so
// it never reaches a VM account. This answers the question for such an account
// directly: is there a metrics store, can Nudgebee reach it, and are THIS account's
// VMs actually writing to it?

// VM metrics states.
const (
	// VMMetricsNotConfigured: no metrics integration serves the account.
	VMMetricsNotConfigured = "not_configured"
	// VMMetricsCheckFailed: the check itself could not run (a database error, several
	// providers and none marked default). Error says why. Reporting it as
	// not_configured would send a user who has an integration to add another.
	VMMetricsCheckFailed = "check_failed"
	// VMMetricsUnreachable: an integration exists but the store cannot be queried.
	VMMetricsUnreachable = "unreachable"
	// VMMetricsNoData: the store answers, but none of the account's VMs is writing to it.
	VMMetricsNoData = "no_data"
	// VMMetricsConnected: the store answers and at least one of the account's VMs is writing.
	VMMetricsConnected = "connected"
)

// What Reporting counts.
const (
	// VMScopeAccount: the agents were matched to this account's VM inventory.
	VMScopeAccount = "account"
	// VMScopeStore: every agent writing to the store. Used when the account has no VM
	// inventory to attribute them to, so the answer is about the store, not the account.
	VMScopeStore = "store"
)

// vmAgentLookback is how recent a VM agent's sample must be to count as reporting.
// The agent writes every scrape interval, so ten minutes tolerates a few missed
// writes (a store restart, the agent's on-disk spool draining) without flapping.
const vmAgentLookback = "10m"

// The two node-agent series that identify a machine. node_info is emitted once per host
// and carries the hostname, so counting its series counts machines, not containers or
// interfaces. node_cloud_info carries the cloud instance id and private IP; a machine
// outside a cloud has no such series and is matched by hostname.
const (
	vmAgentInfoQuery     = `last_over_time(node_info{job="nudgebee-node-agent"}[` + vmAgentLookback + `])`
	vmAgentIdentityQuery = `last_over_time(node_cloud_info{job="nudgebee-node-agent"}[` + vmAgentLookback + `])`
)

// VMReportingAgent is one node-agent seen writing to the store.
type VMReportingAgent struct {
	Instance string `json:"instance"`
	Hostname string `json:"hostname,omitempty"`
}

// VMMetricsStatus is the metrics connection state of a self-hosted VM account.
type VMMetricsStatus struct {
	State           string `json:"state"`
	Provider        string `json:"provider,omitempty"`
	IntegrationId   string `json:"integration_id,omitempty"`
	IntegrationName string `json:"integration_name,omitempty"`
	Error           string `json:"error,omitempty"`
	// Scope says what Reporting counts: this account's VMs, or every agent in the store.
	Scope string `json:"scope,omitempty"`
	// Inventory is how many VMs the account has on record.
	Inventory int `json:"inventory"`
	// OtherAgents counts agents writing to the same store that are not this account's VMs.
	OtherAgents int                `json:"other_agents"`
	Reporting   []VMReportingAgent `json:"reporting"`
}

// vmAgent is one node-agent in the store, with every label that can identify its machine.
type vmAgent struct {
	Instance   string
	Hostname   string
	InstanceID string
	LocalIP    string
}

// vmInventoryEntry is one VM the account has on record.
type vmInventoryEntry struct {
	ResourceID string `db:"resourse_id"`
	Name       string `db:"name"`
	IP         string `db:"ip"`
}

// Seams so tests need no metrics backend or database.
var (
	// probeIntegrationForAccount runs the connection test through THIS account. The plain
	// TestIntegrationConnection tests through the integration's first linked account, and
	// for a store reached through a forager that decides whose forager answers.
	probeIntegrationForAccount = core.TestIntegrationConnectionForAccount

	fetchVMAgents = func(ctx *security.RequestContext, accountId, provider, source string) ([]vmAgent, error) {
		out, err := FetchMetricsQuery(ctx, FetchMetricsRequest{
			AccountId:            accountId,
			MetricProvider:       provider,
			MetricProviderSource: source,
			Queries:              map[string]string{"vm_agent_info": vmAgentInfoQuery, "vm_agent_identity": vmAgentIdentityQuery},
			Instant:              true,
		})
		if err != nil {
			return nil, err
		}
		return vmAgentsFromResult(out)
	}

	listVMInventory = func(ctx *security.RequestContext, accountId string) ([]vmInventoryEntry, error) {
		dbms, err := database.GetDatabaseManager(database.Metastore)
		if err != nil {
			return nil, err
		}
		var rows []vmInventoryEntry
		err = dbms.Db.SelectContext(ctx.GetContext(), &rows, `
			SELECT resourse_id,
			       COALESCE(name, '') AS name,
			       COALESCE(meta->>'private_ip', meta->>'PrivateIpAddress', '') AS ip
			FROM cloud_resourses
			WHERE account = $1::uuid AND COALESCE(is_active, true)
			  AND type IN ('VM', 'compute-instance')`, accountId)
		return rows, err
	}
)

// vmAgentsFromResult reads the agents out of the two identity queries, merging the labels
// of one machine into one entry. A failing query does not fail the call: its error rides on
// the result, so an unanswered query would otherwise read as "no agents are writing".
func vmAgentsFromResult(out OutputMetricQuery) ([]vmAgent, error) {
	for _, r := range out.Results {
		if r.Error != nil && *r.Error != "" {
			return nil, fmt.Errorf("%s", *r.Error)
		}
	}
	byInstance := map[string]*vmAgent{}
	for _, r := range out.Results {
		for _, series := range r.Payload {
			instance := series.Metric["instance"]
			if instance == "" {
				continue
			}
			a := byInstance[instance]
			if a == nil {
				a = &vmAgent{Instance: instance}
				byInstance[instance] = a
			}
			if v := series.Metric["hostname"]; v != "" {
				a.Hostname = v
			}
			if v := series.Metric["instance_id"]; v != "" {
				a.InstanceID = v
			}
			if v := series.Metric["local_ipv4"]; v != "" {
				a.LocalIP = v
			}
		}
	}
	agents := make([]vmAgent, 0, len(byInstance))
	for _, a := range byInstance {
		agents = append(agents, *a)
	}
	sortVMAgents(agents)
	return agents, nil
}

func sortVMAgents(agents []vmAgent) {
	sort.Slice(agents, func(i, j int) bool {
		if agents[i].Hostname != agents[j].Hostname {
			return agents[i].Hostname < agents[j].Hostname
		}
		return agents[i].Instance < agents[j].Instance
	})
}

// attributeVMAgents returns the agents that belong to the account's VMs. A VM is matched to
// an agent by its cloud instance id, then its private IP, then its hostname, the same order
// the per-VM metrics lookup uses. An identifier that names more than one agent matches none:
// attributing a machine another VM happens to share an IP or hostname with would count it
// for the wrong account.
func attributeVMAgents(agents []vmAgent, inventory []vmInventoryEntry) []vmAgent {
	index := func(key func(vmAgent) string) map[string][]int {
		m := map[string][]int{}
		for i, a := range agents {
			if k := key(a); k != "" {
				m[k] = append(m[k], i)
			}
		}
		return m
	}
	byID := index(func(a vmAgent) string { return a.InstanceID })
	byIP := index(func(a vmAgent) string { return a.LocalIP })
	byHost := index(func(a vmAgent) string { return a.Hostname })
	only := func(m map[string][]int, k string) (int, bool) {
		if idx := m[k]; k != "" && len(idx) == 1 {
			return idx[0], true
		}
		return 0, false
	}

	matched := map[int]bool{}
	for _, vm := range inventory {
		if i, ok := only(byID, vm.ResourceID); ok {
			matched[i] = true
		} else if i, ok := only(byIP, vm.IP); ok {
			matched[i] = true
		} else if i, ok := only(byHost, vm.Name); ok {
			matched[i] = true
		}
	}
	out := make([]vmAgent, 0, len(matched))
	for i := range agents {
		if matched[i] {
			out = append(out, agents[i])
		}
	}
	return out
}

func toReporting(agents []vmAgent) []VMReportingAgent {
	out := make([]VMReportingAgent, 0, len(agents))
	for _, a := range agents {
		out = append(out, VMReportingAgent{Instance: a.Instance, Hostname: a.Hostname})
	}
	return out
}

// CheckVMMetricsStatus reports whether a self-hosted VM account has working metrics. It
// reuses the provider resolver behind the Kubernetes provider status, then asks the store
// which node-agents are writing and attributes them to this account's VMs.
//
// The store is queried first: a query that answers proves the store is reachable, so the
// connection test (a second round trip through a forager) only runs when the query fails, to
// give the clearer error.
func CheckVMMetricsStatus(ctx *security.RequestContext, accountId string) (VMMetricsStatus, error) {
	if accountId == "" {
		return VMMetricsStatus{}, fmt.Errorf("observability: account_id is required")
	}
	status := VMMetricsStatus{Reporting: []VMReportingAgent{}}

	provider, source, integration, err := resolveProviderForStatus(ctx, accountId, "", "metrics", "")
	switch {
	case err != nil && !errors.Is(err, account.ErrAgentNotConnected):
		// The check could not run. Say so rather than telling a user who has an
		// integration to add one.
		status.State = VMMetricsCheckFailed
		status.Error = err.Error()
		return status, nil
	case err != nil || integration == nil || source == "agent":
		// Nothing a VM account can read from: no default metrics integration, or only a
		// Kubernetes agent's (which a VM account does not have).
		status.State = VMMetricsNotConfigured
		return status, nil
	}

	status.Provider = provider
	status.IntegrationId = integration.Id
	status.IntegrationName = integration.Name

	agents, queryErr := fetchVMAgents(ctx, accountId, provider, source)
	if queryErr != nil {
		status.State = VMMetricsUnreachable
		status.Error = queryErr.Error()
		// The connection test usually names the cause better (the forager cannot reach
		// the store, the credentials were refused) than the query's own error.
		if probeErr := probeIntegrationForAccount(ctx, integration.Id, accountId); probeErr != nil {
			status.Error = probeErr.Error()
		}
		return status, nil
	}

	inventory, invErr := listVMInventory(ctx, accountId)
	if invErr != nil {
		// An unreadable inventory is not an empty one: treating it as empty would fall back
		// to counting every agent in a shared store and report this account Connected.
		ctx.GetLogger().Warn("observability: could not read the VM inventory", "account_id", accountId, "error", invErr)
		status.State = VMMetricsCheckFailed
		status.Error = fmt.Sprintf("could not read this account's VM inventory: %s", invErr)
		return status, nil
	}
	status.Inventory = len(inventory)

	reporting := agents
	status.Scope = VMScopeStore
	if len(inventory) > 0 {
		reporting = attributeVMAgents(agents, inventory)
		status.Scope = VMScopeAccount
		status.OtherAgents = len(agents) - len(reporting)
	}
	status.Reporting = toReporting(reporting)

	if len(reporting) == 0 {
		status.State = VMMetricsNoData
		return status, nil
	}
	status.State = VMMetricsConnected
	return status, nil
}
