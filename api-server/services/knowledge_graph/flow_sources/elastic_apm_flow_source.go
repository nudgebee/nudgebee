package flow_sources

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/observability"
	"nudgebee/services/security"
)

// ElasticAPMFlowSource builds CALLS edges from Elastic APM data in a customer's
// Elasticsearch — the dependencies Kibana's service map draws — using plain
// aggregations (observability/elasticsearch_apm_dependencies.go), so no Kibana or
// Platinum licence is involved.
//
// Identity is (namespace, pod), never service.name. An APM service becomes a graph
// node only through a pod that resolves to a workload in the account being built,
// so one Elasticsearch cluster shared by several clusters cannot draw another
// cluster's services into this account. A caller with no resolvable pod is
// skipped and counted, not invented.
//
// On by default; KG_ELASTIC_APM_FLOW_SOURCE_ENABLED=false turns it off. It runs
// for every connected K8s account whose Elasticsearch integration declares the
// elastic_apm trace schema, reading that account's configured trace index — the
// same declaration and index the Traces tab uses, so the two never disagree about
// what the cluster holds. Accounts declaring another layout are not queried.
type ElasticAPMFlowSource struct {
	*BaseFlowSource
}

const (
	elasticAPMSourceName = "elastic-apm"
	elasticAPMEnvEnabled = "KG_ELASTIC_APM_FLOW_SOURCE_ENABLED"
	// elasticAPMDependencyWindow bounds the pre-aggregated reads when the build
	// request carries no TimeRange.
	elasticAPMDependencyWindow = 24 * time.Hour
	// elasticAPMSampleWindow bounds the one read over raw span documents.
	elasticAPMSampleWindow = time.Hour
)

// Values of the apm_metrics_scope edge property.
const (
	// elasticAPMScopeAccount: the edge's counts belong to this account alone.
	elasticAPMScopeAccount = "account"
	// elasticAPMScopeShared: the service name is also seen outside this
	// account/namespace, so the service-level counts would be a cross-cluster or
	// cross-namespace total. The edge is kept; its counts are omitted.
	elasticAPMScopeShared = "shared"
)

// Test seams.
var (
	fetchElasticAPMDeps    = observability.FetchElasticAPMDependencies
	getElasticAPMESConfig  = observability.GetElasticsearchConfig
	getElasticAPMAccounts  = core.GetK8sAccountsForTenant
	newElasticAPMPodLookup = func(reqCtx *security.RequestContext, accountID string, nodes []*core.DbNode, logger *slog.Logger) *PodIPResolver {
		return NewPodIPResolver(reqCtx, accountID, nodes, logger)
	}
)

func init() {
	RegisterFlowSourceFactory(
		elasticAPMSourceName,
		func(logger *slog.Logger) (core.FlowSourceInterface, error) {
			return NewElasticAPMFlowSource(logger), nil
		},
		"Elastic APM flow source: service-to-dependency CALLS edges from Elastic APM service_destination metrics and the parent.id join",
		string(core.FlowSourceCategoryTracing),
	)
}

// NewElasticAPMFlowSource constructs the source. Enabled unless
// KG_ELASTIC_APM_FLOW_SOURCE_ENABLED reads as false: an account only reaches
// Elasticsearch once it has an integration declaring the elastic_apm trace schema,
// so the source is a no-op for everyone else and needs no opt-in. The env var
// remains the kill switch.
//
// Being a kill switch inverts the typo risk the opt-in flags in this package carry:
// a value they cannot parse leaves their source off, which costs nothing, while an
// unparsed value here would leave this source on and ignore an operator trying to
// stop it. So every spelling Go reads as false disables it ("false", "FALSE", "0",
// "f"), not only the literal word; unset means enabled.
func NewElasticAPMFlowSource(logger *slog.Logger) *ElasticAPMFlowSource {
	parsed, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(elasticAPMEnvEnabled)))
	enabled := err != nil || parsed
	return &ElasticAPMFlowSource{
		BaseFlowSource: NewBaseFlowSource(elasticAPMSourceName, core.FlowSourceCategoryTracing, enabled, logger),
	}
}

// elasticAPMAccount is one K8s account of an integration group with its pod index.
type elasticAPMAccount struct {
	id   string
	pods *PodIPResolver
}

// elasticAPMGroup is the accounts that read identical APM data: one Elasticsearch
// integration and one resolved trace index (an account may override the index).
type elasticAPMGroup struct {
	cfg      *observability.ElasticsearchConfig
	accounts []string
}

// BuildFlowRelationships fetches each integration's APM data once and resolves it
// per account.
func (s *ElasticAPMFlowSource) BuildFlowRelationships(
	reqCtx *security.RequestContext,
	req *core.FlowSourceBuildRequest,
) ([]*core.DbEdge, []*core.DbNode, error) {
	startTime := time.Now()
	defer s.TrackBuildTime(startTime)

	s.InitializeNodeMatcher(req.ExistingNodes)

	accounts, err := getElasticAPMAccounts(req.TenantID, req.CloudAccountIDs)
	if err != nil {
		s.IncrementErrorCount()
		return nil, nil, fmt.Errorf("failed to get K8s accounts: %w", err)
	}

	// Integration config needs a tenant on the security context (see
	// datadog_apm_flow_source.go getDatadogConfig).
	secCtx := security.NewRequestContextForTenantAdmin(req.TenantID, s.logger, nil, nil)
	groups := map[string]*elasticAPMGroup{}
	for _, acct := range accounts {
		cfg, cfgErr := getElasticAPMESConfig(secCtx, acct.CloudAccountID)
		if cfgErr != nil {
			if !errors.Is(cfgErr, observability.ErrNoElasticsearchIntegration) {
				s.logger.Warn("elastic-apm: elasticsearch config unavailable; skipping account",
					"cloud_account_id", acct.CloudAccountID, "error", cfgErr)
				s.IncrementErrorCount()
			}
			continue
		}
		if cfg.TraceSchema != observability.ESTraceSchemaElasticAPM {
			continue
		}
		key := cfg.IntegrationID + "|" + cfg.TraceIndex
		g, ok := groups[key]
		if !ok {
			g = &elasticAPMGroup{cfg: cfg}
			groups[key] = g
		}
		g.accounts = append(g.accounts, acct.CloudAccountID)
	}
	if len(groups) == 0 {
		return []*core.DbEdge{}, []*core.DbNode{}, nil
	}

	end := time.Now()
	depStart := end.Add(-elasticAPMDependencyWindow)
	if req.TimeRange != nil && !req.TimeRange.StartTime.IsZero() && req.TimeRange.EndTime.After(req.TimeRange.StartTime) {
		depStart, end = req.TimeRange.StartTime, req.TimeRange.EndTime
	}
	sampleStart := end.Add(-elasticAPMSampleWindow)
	if sampleStart.Before(depStart) {
		sampleStart = depStart
	}

	ipResolver := NewK8sServiceIPResolver(req.ExistingNodes)
	nodeIPResolver := NewK8sNodeIPResolver(req.ExistingNodes)

	edges := make([]*core.DbEdge, 0)
	nodes := make([]*core.DbNode, 0)
	for _, g := range sortedElasticAPMGroups(groups) {
		data, fetchErr := fetchElasticAPMDeps(reqCtx, g.cfg, depStart, sampleStart, end)
		if fetchErr != nil {
			s.logger.Warn("elastic-apm: fetching APM dependencies failed",
				"integration_id", g.cfg.IntegrationID, "accounts", g.accounts, "error", fetchErr)
			s.IncrementErrorCount()
			continue
		}
		if data == nil {
			continue
		}
		if len(data.Pods) == 0 {
			// Dependencies exist but the trace index holds no Elastic APM transactions
			// with pods: usually a declared elastic_apm schema whose Trace Index still
			// points at another layout. Nothing can be placed, so say why.
			s.logger.Warn("elastic-apm: APM dependencies found but no Elastic APM transactions with pods in the trace index; check the integration's Trace Index",
				"integration_id", g.cfg.IntegrationID, "trace_index", g.cfg.TraceIndex, "accounts", g.accounts,
				"dependencies", len(data.Dependencies))
			continue
		}
		groupAccounts := make([]elasticAPMAccount, 0, len(g.accounts))
		for _, id := range g.accounts {
			groupAccounts = append(groupAccounts, elasticAPMAccount{
				id:   id,
				pods: newElasticAPMPodLookup(reqCtx, id, req.ExistingNodes, s.logger),
			})
		}
		e, n := s.buildGroupGraph(data, groupAccounts, sampleStart, req.TenantID, ipResolver, nodeIPResolver)
		edges = append(edges, e...)
		nodes = append(nodes, n...)
	}

	s.LogMetrics()
	s.logger.Info("completed building flow relationships from Elastic APM",
		"groups", len(groups), "edges_created", len(edges), "external_nodes_created", len(nodes))
	return edges, nodes, nil
}

func sortedElasticAPMGroups(groups map[string]*elasticAPMGroup) []*elasticAPMGroup {
	out := make([]*elasticAPMGroup, 0, len(groups))
	for _, g := range groups {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].cfg, out[j].cfg
		return a.IntegrationID+"|"+a.TraceIndex < b.IntegrationID+"|"+b.TraceIndex
	})
	return out
}

// elasticAPMPair keys a read-1 dependency.
type elasticAPMPair struct{ service, resource string }

// buildGroupGraph turns one integration group's data into edges and ExternalService nodes
// for every account of the group. Pure apart from the node matcher and resolvers,
// so it is the unit under test.
func (s *ElasticAPMFlowSource) buildGroupGraph(
	data *observability.ElasticAPMDependencies,
	accounts []elasticAPMAccount,
	recentSince time.Time,
	tenantID string,
	ipResolver *K8sServiceIPResolver,
	nodeIPResolver *K8sNodeIPResolver,
) ([]*core.DbEdge, []*core.DbNode) {
	deps := make(map[elasticAPMPair]observability.ElasticAPMDependency, len(data.Dependencies))
	for _, d := range data.Dependencies {
		deps[elasticAPMPair{d.CallerService, d.Resource}] = d
	}
	metricsOwner := s.elasticAPMMetricsOwners(data.Pods, accounts, recentSince)
	calls := elasticAPMCallsWithFallback(data)

	var edges []*core.DbEdge
	var nodes []*core.DbNode
	seenEdge := map[string]bool{}
	seenNode := map[string]bool{}
	unresolvedCallers := map[string]int{}

	for _, c := range calls {
		resolvedAnywhere := false
		for _, acct := range accounts {
			caller := s.resolveElasticAPMWorkload(acct, c.CallerNamespace, c.CallerPod)
			if caller == nil {
				continue
			}
			resolvedAnywhere = true

			dest, props := s.resolveElasticAPMDestination(acct, c, deps[elasticAPMPair{c.CallerService, c.Resource}], tenantID, ipResolver, nodeIPResolver)
			if dest == nil || dest.ID == caller.ID {
				continue
			}
			if dest.NodeType == core.NodeTypeExternalService && !seenNode[dest.ID] {
				seenNode[dest.ID] = true
				nodes = append(nodes, dest)
			}

			edgeKey := caller.ID + "->" + dest.ID
			if seenEdge[edgeKey] {
				continue
			}
			seenEdge[edgeKey] = true

			if d, ok := deps[elasticAPMPair{c.CallerService, c.Resource}]; ok {
				if metricsOwner[c.CallerService] == acct.id {
					props["apm_metrics_scope"] = elasticAPMScopeAccount
					props["request_count"] = d.Calls
					props["failure_count"] = d.Failures
					if d.Calls > 0 {
						props["latency_ms"] = d.LatencySumUs / float64(d.Calls) / 1000
					}
				} else {
					props["apm_metrics_scope"] = elasticAPMScopeShared
				}
			}
			if edge := s.CreateEdge(caller, dest, core.RelationshipCalls, props, tenantID, caller.CloudAccountID); edge != nil {
				edges = append(edges, edge)
			}
		}
		if !resolvedAnywhere {
			unresolvedCallers[c.CallerService]++
		}
	}

	if len(unresolvedCallers) > 0 {
		s.logger.Info("elastic-apm: callers with no pod in any account of the group were skipped",
			"count", len(unresolvedCallers), "services", unresolvedCallers)
	}
	return edges, nodes
}

// elasticAPMCallsWithFallback returns the observed calls plus, for each read-1
// dependency the 1h sample missed (a quiet pair), one receiver-less call per
// namespace the caller runs in — using that namespace's most recently seen pod
// from the exhaustive read 4, the one most likely still in the inventory — so the
// pair still resolves through the hostname path.
func elasticAPMCallsWithFallback(data *observability.ElasticAPMDependencies) []observability.ElasticAPMCall {
	sampled := map[elasticAPMPair]bool{}
	for _, c := range data.Calls {
		sampled[elasticAPMPair{c.CallerService, c.Resource}] = true
	}
	// Newest pod per (service, namespace), indexed by service; namespaces keep
	// first-seen order so the output is deterministic.
	type svcNS struct{ service, namespace string }
	newest := map[svcNS]observability.ElasticAPMServicePod{}
	namespacesOf := map[string][]string{}
	for _, p := range data.Pods {
		k := svcNS{p.Service, p.Namespace}
		cur, ok := newest[k]
		if !ok {
			namespacesOf[p.Service] = append(namespacesOf[p.Service], p.Namespace)
		}
		if !ok || p.LastSeen.After(cur.LastSeen) {
			newest[k] = p
		}
	}
	calls := append([]observability.ElasticAPMCall(nil), data.Calls...)
	for _, d := range data.Dependencies {
		if sampled[elasticAPMPair{d.CallerService, d.Resource}] {
			continue
		}
		for _, ns := range namespacesOf[d.CallerService] {
			p := newest[svcNS{d.CallerService, ns}]
			calls = append(calls, observability.ElasticAPMCall{
				CallerService: d.CallerService, CallerNamespace: p.Namespace, CallerPod: p.Pod, Resource: d.Resource,
			})
		}
	}
	return calls
}

// elasticAPMMetricsOwners maps each APM service to the one account its
// service-level counts can be attributed to. read 1 is keyed by service.name
// alone, so the counts are a total over every pod reporting that name. They belong
// to an account only when every observed pod of the service resolves to one
// workload in that one account; a pod in another account, in none (an unconnected
// cluster writing to the same Elasticsearch), in a second namespace, or of a second
// workload (an API Deployment and its worker sharing a service name) makes the
// total shared.
//
// Only pods seen since recentSince are judged, falling back to all of a service's
// pods when none is that recent: the pod inventory holds active pods only, so a
// pod a rollout retired earlier in the window would otherwise read as "in no
// account" and mark every recently deployed service shared.
func (s *ElasticAPMFlowSource) elasticAPMMetricsOwners(pods []observability.ElasticAPMServicePod, accounts []elasticAPMAccount, recentSince time.Time) map[string]string {
	hasRecent := map[string]bool{}
	for _, p := range pods {
		if !p.LastSeen.Before(recentSince) {
			hasRecent[p.Service] = true
		}
	}
	type state struct {
		account    string
		namespaces map[string]bool
		workloads  map[string]bool
		shared     bool
	}
	states := map[string]*state{}
	for _, p := range pods {
		if hasRecent[p.Service] && p.LastSeen.Before(recentSince) {
			continue
		}
		st, ok := states[p.Service]
		if !ok {
			st = &state{namespaces: map[string]bool{}, workloads: map[string]bool{}}
			states[p.Service] = st
		}
		st.namespaces[p.Namespace] = true
		owner := ""
		for _, acct := range accounts {
			if node := s.resolveElasticAPMWorkload(acct, p.Namespace, p.Pod); node != nil {
				st.workloads[node.ID] = true
				if owner != "" {
					owner = "" // resolves in two accounts: ambiguous
					st.shared = true
					break
				}
				owner = acct.id
			}
		}
		switch {
		case owner == "":
			st.shared = true
		case st.account == "":
			st.account = owner
		case st.account != owner:
			st.shared = true
		}
	}
	out := map[string]string{}
	for svc, st := range states {
		if !st.shared && st.account != "" && len(st.namespaces) == 1 && len(st.workloads) == 1 {
			out[svc] = st.account
		}
	}
	return out
}

// resolveElasticAPMWorkload maps an APM (namespace, pod) to the workload node that
// owns it in this account, through the account-scoped pod index only.
//
// Deliberately no fallback from the pod name to its owner name: two clusters
// writing to one Elasticsearch commonly run the same deployment in the same
// namespace, so "frontend-86cc…" from an unconnected cluster would land on this
// account's demo/frontend. A pod name is unique to its cluster; an owner name is not.
func (s *ElasticAPMFlowSource) resolveElasticAPMWorkload(acct elasticAPMAccount, namespace, pod string) *core.DbNode {
	if pod == "" {
		return nil
	}
	if node, ok := acct.pods.ResolvePodName(namespace, pod); ok {
		return node
	}
	return nil
}

// resolveElasticAPMDestination resolves the called side of one call for one
// account, returning the node and the edge properties. nil means drop the call.
//
// Order: the receiving service observed by the parent.id join; a raw IP through
// the ClusterIP / pod / node resolvers; a hostname by Kubernetes DNS rules (a
// short name only in the caller's namespace); otherwise an ExternalService.
func (s *ElasticAPMFlowSource) resolveElasticAPMDestination(
	acct elasticAPMAccount,
	c observability.ElasticAPMCall,
	dep observability.ElasticAPMDependency,
	tenantID string,
	ipResolver *K8sServiceIPResolver,
	nodeIPResolver *K8sNodeIPResolver,
) (*core.DbNode, map[string]interface{}) {
	props := map[string]interface{}{
		"source_service":           c.CallerService,
		"connection_type":          "service",
		"apm_destination_resource": c.Resource,
	}
	if dep.TargetType != "" {
		props["protocol"] = dep.TargetType
	}

	if c.ReceiverService != "" {
		if node := s.resolveElasticAPMWorkload(acct, c.ReceiverNamespace, c.ReceiverPod); node != nil {
			props["dest_service"] = c.ReceiverService
			props["apm_receiver_service"] = c.ReceiverService
			props["dependency_type"] = "receiver"
			return node, props
		}
	}

	host := elasticAPMHost(c.Resource)
	if host == "" {
		return nil, nil // e.g. ":0" with no receiver: nothing to name
	}
	props["dest_service"] = host

	if isRawIPName(c.Resource) || net.ParseIP(host) != nil {
		if IsSpecialIPName(c.Resource) {
			return nil, nil
		}
		node, _, reason, source, ok := resolveIPNamedExternalService(c.Resource, acct.id, "", ipResolver, acct.pods, nodeIPResolver)
		if !ok {
			return nil, nil // never mint an IP-named node
		}
		props["dependency_type"] = "ip"
		props["resolved_from_ip"] = host
		props["resolution_source"] = source
		props["resolution_reason"] = reason
		return node, props
	}

	if node := s.resolveElasticAPMHostname(acct, c.CallerNamespace, host); node != nil {
		props["dependency_type"] = "hostname"
		return node, props
	}

	props["dependency_type"] = "external"
	return s.newElasticAPMExternalNode(acct.id, tenantID, c, dep), props
}

// elasticAPMHost strips the ":port" APM Server appends to a destination resource.
// Non host:port resources ("kafka/orders", "redis") are returned unchanged.
func elasticAPMHost(resource string) string {
	if h, _, err := net.SplitHostPort(resource); err == nil {
		return h
	}
	return resource
}

// resolveElasticAPMHostname resolves a hostname the way Kubernetes DNS does:
// "svc.ns[.svc.cluster.local]" names its namespace; a short name only resolves in
// the caller's own namespace. Same account only, and never a name-only match
// across namespaces.
func (s *ElasticAPMFlowSource) resolveElasticAPMHostname(acct elasticAPMAccount, callerNamespace, host string) *core.DbNode {
	types := []core.NodeType{core.NodeTypeK8sService, core.NodeTypeWorkload, core.NodeTypeService}
	if parsed := parseK8sServiceDNS(host); parsed != nil && parsed.Namespace != "" && parsed.ServiceName != "" {
		return s.findElasticAPMNode(acct.id, parsed.Namespace, parsed.ServiceName, types)
	}
	if strings.Contains(host, ".") || callerNamespace == "" {
		return nil
	}
	if node := s.findElasticAPMNode(acct.id, callerNamespace, host, types); node != nil {
		return node
	}
	// A bare pod hostname (StatefulSet ordinal such as "redis-master-0").
	if node, ok := acct.pods.ResolvePodName(callerNamespace, host); ok {
		return node
	}
	return nil
}

// findElasticAPMNode finds a node by exact namespace + name in one account, trying
// the node types in order.
func (s *ElasticAPMFlowSource) findElasticAPMNode(accountID, namespace, name string, types []core.NodeType) *core.DbNode {
	matcher := s.GetNodeMatcher()
	if matcher == nil || name == "" || namespace == "" {
		return nil
	}
	for _, nt := range types {
		result, err := matcher.FindNode(MatchCriteria{
			NodeType:  nt,
			AccountID: accountID,
			PropertyMatches: []PropertyMatch{
				{PropertyPath: "namespace", Value: namespace, MatchType: core.MatchTypeExact},
				{PropertyPath: "name", Value: name, MatchType: core.MatchTypeExact},
			},
		})
		if err == nil && result.Matched {
			return result.Node
		}
	}
	return nil
}

// newElasticAPMExternalNode creates the ExternalService for an unresolved
// dependency. A resource that is only a technology name ("redis", "postgresql",
// "elasticsearch" — what agents report when the span carries no host) says nothing
// about which instance was called, so it is keyed by the caller's namespace and
// marked ambiguous rather than merging every namespace's Redis into one node. That
// key does not merge with another source's stub of the same name; the central
// enricher matches by name, so both may still enrich to one cloud resource.
func (s *ElasticAPMFlowSource) newElasticAPMExternalNode(
	accountID, tenantID string,
	c observability.ElasticAPMCall,
	dep observability.ElasticAPMDependency,
) *core.DbNode {
	name := c.Resource
	namespace := ""
	ambiguous := !strings.ContainsAny(name, ":./")
	if ambiguous {
		namespace = c.CallerNamespace
	}
	uniqueKey := core.BuildUniqueKey(
		core.DeriveCloudProvider(elasticAPMSourceName, core.NodeTypeExternalService),
		accountID, "", core.NodeTypeExternalService, namespace, name,
	)
	props := map[string]interface{}{
		"name":        name,
		"kind":        "ExternalService",
		"is_external": true,
		"subtype":     "ExternalService",
	}
	if dep.TargetType != "" {
		props["original_kind"] = dep.TargetType
	}
	if ambiguous {
		props["ambiguous"] = true
		props["namespace"] = namespace
	}
	return core.NewNode(core.NodeTypeExternalService, uniqueKey, props, tenantID, accountID, elasticAPMSourceName)
}
