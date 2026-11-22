package flow_sources

import (
	"context"
	"fmt"
	"log/slog"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/security"
	"nudgebee/services/traces"
	"nudgebee/services/vmservicemap"
)

func init() {
	RegisterFlowSourceFactory(
		core.VMServiceMapSourceName,
		func(logger *slog.Logger) (core.FlowSourceInterface, error) {
			return NewVMServiceMapFlowSource(logger), nil
		},
		"VM service-map flow source: reads each self-hosted VM account's forager-built service map",
		string(core.FlowSourceCategoryTracing),
	)
}

// VMServiceMapFlowSource turns vmservicemap's per-account service map (the
// account's forager, resolved through the node-agent series — see
// api-server/services/vmservicemap/CONTRACT.md) into knowledge graph nodes and
// edges: one Service node per systemd unit/container, CALLS edges between
// them, and a RUNS_ON edge to the host's existing SelfHostedVM ComputeInstance
// node (sources/selfhosted). A destination vmservicemap could not place on a
// reporting host becomes an ExternalService node, which the centralized
// enrichment pass (CentralizedExternalServiceEnricher, run once after every
// flow source completes) then tries to name against cloud resources — scoped
// to the VM account's discovery-linked accounts, not the whole tenant (see
// prepareMatchingContext).
type VMServiceMapFlowSource struct {
	*BaseFlowSource
}

// NewVMServiceMapFlowSource creates a new VM service-map flow source.
func NewVMServiceMapFlowSource(logger *slog.Logger) *VMServiceMapFlowSource {
	return &VMServiceMapFlowSource{
		BaseFlowSource: NewBaseFlowSource(core.VMServiceMapSourceName, core.FlowSourceCategoryTracing, true, logger),
	}
}

// BuildFlowRelationships builds flow relationships from every self-hosted VM
// account's forager-built service map.
func (s *VMServiceMapFlowSource) BuildFlowRelationships(
	reqCtx *security.RequestContext,
	req *core.FlowSourceBuildRequest,
) ([]*core.DbEdge, []*core.DbNode, error) {
	ctx := reqCtx.GetContext()
	startTime := TimeNow()
	defer s.TrackBuildTime(startTime)

	s.InitializeNodeMatcher(req.ExistingNodes)

	vmAccounts, err := core.GetVMAccountsForTenant(req.TenantID, req.CloudAccountIDs)
	if err != nil {
		s.IncrementErrorCount()
		return nil, nil, fmt.Errorf("failed to get VM accounts: %w", err)
	}
	if len(vmAccounts) == 0 {
		s.logger.Info("no self-hosted VM accounts found", "tenant_id", req.TenantID)
		return nil, nil, nil
	}

	var edges []*core.DbEdge
	var nodes []*core.DbNode
	for _, account := range vmAccounts {
		accEdges, accNodes, err := s.processVMAccount(ctx, req, account)
		if err != nil {
			s.logger.Error("failed to process VM account",
				"cloud_account_id", account.CloudAccountID, "error", err)
			s.IncrementErrorCount()
			continue
		}
		edges = append(edges, accEdges...)
		nodes = append(nodes, accNodes...)
	}

	s.LogMetrics()
	s.logger.Info("completed building flow relationships from vm_service_map",
		"vm_accounts_processed", len(vmAccounts),
		"edges_created", len(edges),
		"nodes_created", len(nodes))

	return edges, nodes, nil
}

// processVMAccount fetches one VM account's forager answer and converts it.
func (s *VMServiceMapFlowSource) processVMAccount(
	ctx context.Context,
	req *core.FlowSourceBuildRequest,
	account core.VMAccount,
) ([]*core.DbEdge, []*core.DbNode, error) {
	store, err := vmservicemap.ResolveStore(account.CloudAccountID, req.TenantID)
	if err != nil {
		return nil, nil, fmt.Errorf("vm_service_map: resolve store: %w", err)
	}
	end := TimeNow()
	resp, err := vmservicemap.Fetch(account.CloudAccountID, store, end.Add(-vmservicemap.DefaultWindow), end)
	if err != nil {
		return nil, nil, fmt.Errorf("vm_service_map: fetch: %w", err)
	}
	serviceMap := vmservicemap.Build(resp, end)

	// The account itself, plus any account a discovery datasource it owns
	// targets — a cloud-identified host's selfhosted ComputeInstance node can
	// live in either (sources/selfhosted's own scope), so the RUNS_ON join
	// below has to check the same set.
	scopeAccounts, err := core.GetDiscoveryLinkedCloudAccounts(req.TenantID, account.CloudAccountID, "")
	if err != nil {
		s.logger.Warn("failed to resolve discovery-linked accounts, host join limited to the VM account itself",
			"cloud_account_id", account.CloudAccountID, "error", err)
		scopeAccounts = []string{account.CloudAccountID}
	}

	hostsByInstanceID, hostsByHostname := indexForagerHosts(resp.Hosts)

	appByID := make(map[traces.ServiceApplicationId]*traces.ServiceApplication, len(serviceMap.Applications))
	for i := range serviceMap.Applications {
		appByID[serviceMap.Applications[i].Id] = &serviceMap.Applications[i]
	}

	nodeByAppID := make(map[traces.ServiceApplicationId]*core.DbNode, len(serviceMap.Applications))
	nodes := make([]*core.DbNode, 0, len(serviceMap.Applications))
	getNode := func(app *traces.ServiceApplication) *core.DbNode {
		if n, ok := nodeByAppID[app.Id]; ok {
			return n
		}
		n := s.createServiceOrExternalNode(app, req.TenantID, account.CloudAccountID)
		nodeByAppID[app.Id] = n
		nodes = append(nodes, n)
		return n
	}

	edges := make([]*core.DbEdge, 0)
	hostNodeByNamespace := make(map[string]*core.DbNode)
	hostJoinAttempted := make(map[string]bool)

	for i := range serviceMap.Applications {
		app := &serviceMap.Applications[i]
		appNode := getNode(app)

		// RUNS_ON: only meaningful for a real per-host node (vmservicemap
		// namespaces every service/unknown-process it finds by its host).
		if appNode.NodeType == core.NodeTypeService && app.Id.Namespace != "" {
			namespace := app.Id.Namespace
			if !hostJoinAttempted[namespace] {
				hostJoinAttempted[namespace] = true
				if host, ok := foragerHostFor(app, hostsByInstanceID, hostsByHostname); ok {
					hostNodeByNamespace[namespace] = s.matchHostNode(scopeAccounts, host)
				}
			}
			if hostNode := hostNodeByNamespace[namespace]; hostNode != nil {
				edges = append(edges, s.CreateEdge(appNode, hostNode, core.RelationshipRunsOn,
					map[string]interface{}{}, req.TenantID, account.CloudAccountID))
			}
		}

		for _, up := range app.Upstreams {
			upNamespace, upName, upKind := traces.ParseUpstreamId(up.Id)
			upID := traces.ServiceApplicationId{Namespace: upNamespace, Name: upName, Kind: upKind}
			upApp, ok := appByID[upID]
			if !ok {
				// Build() always adds a node for every destination it resolves
				// (including external/unknown-internal), so every upstream id
				// should resolve here; skip rather than fabricate on a miss.
				s.logger.Warn("vm_service_map: upstream id not found in applications", "upstream_id", up.Id)
				continue
			}
			upNode := getNode(upApp)
			edges = append(edges, s.CreateEdge(appNode, upNode, core.RelationshipCalls,
				upstreamEdgeProperties(&up), req.TenantID, account.CloudAccountID))
		}
	}

	return edges, nodes, nil
}

// createServiceOrExternalNode builds a deterministically-keyed node for one
// vmservicemap application. The unique key's hierarchy slot is the VM host
// (app.Id.Namespace) for a Service node, so two VM hosts running identically-
// named units (e.g. two "nginx.service") get distinct nodes — the generic
// ConvertServiceMapToGraph adapter used by other flow sources keys solely on
// name+environment and would collapse them into one.
//
// Deliberately does not consult GetNodeMatcher(): a VM systemd unit/container
// has no other source authoritative over it to merge with (unlike K8s, where
// k8s_source pre-creates almost everything ebpf would otherwise match), so a
// name-only match here would only risk merging two different VM accounts'
// same-named services. The deterministic key already handles re-observation
// across builds via SaveNodes' upsert.
func (s *VMServiceMapFlowSource) createServiceOrExternalNode(
	app *traces.ServiceApplication,
	tenantID, cloudAccountID string,
) *core.DbNode {
	nodeType := core.NodeTypeService
	hierarchy := app.Id.Namespace
	if app.Id.Kind == vmservicemap.NodeKindExternal || app.Id.Kind == vmservicemap.NodeKindUnknownInternal {
		nodeType = core.NodeTypeExternalService
		hierarchy = "" // external services are not scoped to a VM host
	}

	uniqueKey := core.BuildUniqueKey(core.CloudProviderSelfHosted, cloudAccountID, "", nodeType, hierarchy, app.Id.Name)

	properties := map[string]interface{}{
		"name": app.Id.Name,
		"kind": app.Id.Kind,
	}
	if hierarchy != "" {
		properties["host"] = hierarchy
	}
	for k, v := range app.Labels {
		// vmservicemap's own labels never carry these keys today, but a label
		// map is forager-controlled data — don't let it silently override the
		// node's identity properties if that ever changes.
		if k == "name" || k == "kind" || k == "host" {
			continue
		}
		properties[k] = v
	}

	return core.NewNode(nodeType, uniqueKey, properties, tenantID, cloudAccountID, core.VMServiceMapSourceName)
}

// matchHostNode finds the existing sources/selfhosted ComputeInstance node for
// a forager-reported host, scoped to the given candidate accounts (the VM
// account and its discovery-linked accounts — never the whole tenant, which
// would risk matching a private IP against an unrelated account's host on a
// reused range). Tries the cloud instance id first (set when the node-agent
// resolved one), then falls back to the host's own IPs for an on-prem host
// with no cloud identity (selfhosted_source.go's HostKindOnPrem). Returns nil
// — never fabricates a host — when nothing matches.
func (s *VMServiceMapFlowSource) matchHostNode(scopeAccounts []string, host vmservicemap.Host) *core.DbNode {
	matcher := s.GetNodeMatcher()
	if matcher == nil {
		return nil
	}

	if host.InstanceID != "" {
		for _, accountID := range scopeAccounts {
			result, err := matcher.FindNode(MatchCriteria{
				AccountID: accountID,
				NodeType:  core.NodeTypeComputeInstance,
				PropertyMatches: []PropertyMatch{
					{PropertyPath: "instance_id", Value: host.InstanceID, MatchType: core.MatchTypeExact, CaseSensitive: false},
				},
			})
			if err == nil && result.Matched {
				return result.Node
			}
		}
	}

	for _, ip := range host.IPs {
		for _, accountID := range scopeAccounts {
			result, err := matcher.FindNode(MatchCriteria{
				AccountID: accountID,
				NodeType:  core.NodeTypeComputeInstance,
				PropertyMatches: []PropertyMatch{
					{PropertyPath: "private_ip", Value: ip, MatchType: core.MatchTypeExact, CaseSensitive: false},
				},
			})
			if err == nil && result.Matched {
				return result.Node
			}
		}
	}

	return nil
}

// indexForagerHosts indexes a forager response's hosts for foragerHostFor.
func indexForagerHosts(hosts []vmservicemap.Host) (byInstanceID, byHostname map[string]vmservicemap.Host) {
	byInstanceID = make(map[string]vmservicemap.Host, len(hosts))
	byHostname = make(map[string]vmservicemap.Host, len(hosts))
	for _, h := range hosts {
		if h.InstanceID != "" {
			byInstanceID[h.InstanceID] = h
		}
		if h.Hostname != "" {
			byHostname[h.Hostname] = h
		}
	}
	return byInstanceID, byHostname
}

// foragerHostFor recovers the raw forager Host (with its IPs — lost by the
// time vmservicemap.Build produces a traces.ServiceApplication) behind a
// per-host application. Build() stamps every per-host service's Labels with
// its host's instance_id, so that is tried first; on-prem hosts carry no
// instance_id, so the fallback matches the application's namespace against
// the host's own hostname (the same value vmservicemap.Build names that
// namespace after, in the common case of one host per hostname).
func foragerHostFor(
	app *traces.ServiceApplication,
	byInstanceID, byHostname map[string]vmservicemap.Host,
) (vmservicemap.Host, bool) {
	if iid := app.Labels["instance_id"]; iid != "" {
		if h, ok := byInstanceID[iid]; ok {
			return h, true
		}
	}
	h, ok := byHostname[app.Id.Namespace]
	return h, ok
}

// upstreamEdgeProperties carries a VM CALLS edge's forager-observed metrics.
func upstreamEdgeProperties(up *traces.UpstreamLink) map[string]interface{} {
	return map[string]interface{}{
		"protocol":       up.Protocol,
		"latency_ms":     up.Latency,
		"request_count":  up.RequestCount,
		"failure_count":  up.FailureCount,
		"bytes_sent":     up.BytesSent,
		"bytes_received": up.BytesReceived,
		"status":         up.Status,
	}
}
