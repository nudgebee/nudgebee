package flow_sources

import (
	"fmt"
	"log/slog"
	"net"
	"nudgebee/services/integrations"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/security"
	"strings"
	"time"
)

const (
	newRelicAPMSourceName = "newrelic-apm"
	// Cap NRQL FACET row count; saturation is logged so ops can spot it.
	nrqlFacetLimit = 2000
)

func init() {
	RegisterFlowSourceFactory(
		newRelicAPMSourceName,
		func(logger *slog.Logger) (core.FlowSourceInterface, error) {
			return NewNewRelicAPMFlowSource(logger), nil
		},
		"New Relic APM flow source — emits CALLS edges from NRQL Span data",
		string(core.FlowSourceCategoryTracing),
	)
}

// NewRelicAPMFlowSource creates flow relationships from New Relic span data.
//
// Strategy: NRQL aggregation on the Span table. We facet client-kind spans by
// (service.name, server.address, db.system, db.name, peer.service) and emit a
// CALLS edge for each (caller, target) pair we can resolve to existing graph
// nodes. Targets are matched in priority order:
//
//  1. peer.service (string)        → 4-strategy NodeMatcher via matchServiceToNode
//  2. db.system + db.name together → NodeTypeDatabase by (engine, name)
//  3. server.address (IP)          → cluster-scoped K8sServiceIPResolver
//  4. server.address (hostname)    → 4-strategy NodeMatcher
//
// NerdGraph entity-relationship traversal is not used — it only carries CALLS
// edges for tenants with NR APM agents installed, but those tenants emit Span
// data anyway, so the NRQL path subsumes them.
type NewRelicAPMFlowSource struct {
	*BaseFlowSource
}

// NewNewRelicAPMFlowSource creates a new New Relic APM flow source.
func NewNewRelicAPMFlowSource(logger *slog.Logger) *NewRelicAPMFlowSource {
	base := NewBaseFlowSource(
		newRelicAPMSourceName,
		core.FlowSourceCategoryTracing,
		true,
		logger,
	)
	return &NewRelicAPMFlowSource{BaseFlowSource: base}
}

// Validate validates the New Relic APM flow source configuration.
// Credentials are fetched dynamically per-build, so there is no static config.
func (s *NewRelicAPMFlowSource) Validate() error {
	return s.BaseFlowSource.Validate()
}

// nr_strategy values stamped on emitted edges to make the fetch path obvious in SQL.
const (
	nrStrategyNRQL                  = "nrql"
	nrStrategyNerdGraph             = "nerdgraph"
	nrStrategyNerdGraphFallbackNRQL = "nerdgraph_fallback_nrql"
)

// BuildFlowRelationships dispatches to either the NRQL path (default) or the
// NerdGraph path based on the tenant's `kg_source` integration config. The
// NerdGraph path falls back to NRQL on fetch error or zero relationships
// returned. See the Phase 2 plan for design rationale (D1, D2).
func (s *NewRelicAPMFlowSource) BuildFlowRelationships(
	reqCtx *security.RequestContext,
	req *core.FlowSourceBuildRequest,
) ([]*core.DbEdge, []*core.DbNode, error) {
	startTime := time.Now()
	defer s.TrackBuildTime(startTime)

	s.logger.Info("building flow relationships from New Relic APM",
		"source", s.GetName(),
		"tenant_id", req.TenantID,
		"existing_nodes", len(req.ExistingNodes))

	s.InitializeNodeMatcher(req.ExistingNodes)

	cloudAccountID := resolveCloudAccountID(req)
	if cloudAccountID == "" {
		s.logger.Info("no cloud account ID available; skipping New Relic APM flow source")
		return nil, nil, nil
	}

	apiKey, nrAccountID, region, err := s.getNewRelicConfig(req.TenantID, cloudAccountID)
	if err != nil {
		s.IncrementErrorCount()
		return nil, nil, fmt.Errorf("failed to get New Relic configuration: %w", err)
	}
	if apiKey == "" || nrAccountID == "" {
		s.logger.Warn("New Relic credentials not configured; skipping flow source",
			"cloud_account_id", cloudAccountID)
		return nil, nil, nil
	}

	ipResolver := NewK8sServiceIPResolver(req.ExistingNodes)

	mode := s.getKGSourceMode(req.TenantID, cloudAccountID)
	s.logger.Info("New Relic flow source mode resolved",
		"tenant_id", req.TenantID, "mode", mode)

	if mode == integrations.NewRelicKGSourceNerdGraph {
		// NerdGraph path. On fetch error or empty result, fall back to NRQL with
		// the appropriate strategy label so SQL inspection can distinguish the
		// recovery path from a default-mode tenant. Note: a non-zero relationships
		// fetch that resolves to zero matched nodes does NOT trigger fallback —
		// caller-side matcher and node set are identical for both paths, so source
		// mismatches are equivalently unmatched on NRQL. Coverage gaps surface in
		// the buildEdgesViaNerdGraph log line instead.
		rels, err := fetchNerdGraphRelationships(apiKey, nrAccountID, region, req.TenantID, s.logger)
		if err != nil {
			// Differentiate transient (5xx/429) from permanent (4xx/parse) for ops
			// observability. Both fall back to NRQL identically; only the log
			// classification differs so dashboards can split outage signal from
			// config errors.
			errClass := "permanent"
			if isTransientNerdGraphError(err) {
				errClass = "transient"
			}
			s.logger.Warn("NerdGraph fetch failed; falling back to NRQL",
				"tenant_id", req.TenantID, "err_class", errClass, "err", err)
			s.IncrementErrorCount()
			return s.buildEdgesViaNRQL(apiKey, nrAccountID, region, req, ipResolver, cloudAccountID, nrStrategyNerdGraphFallbackNRQL)
		}
		if len(rels) == 0 {
			s.logger.Info("NerdGraph returned no relationships; falling back to NRQL",
				"tenant_id", req.TenantID, "nr_account_id", nrAccountID)
			return s.buildEdgesViaNRQL(apiKey, nrAccountID, region, req, ipResolver, cloudAccountID, nrStrategyNerdGraphFallbackNRQL)
		}
		return s.buildEdgesViaNerdGraph(rels, req, ipResolver, cloudAccountID, nrAccountID)
	}

	return s.buildEdgesViaNRQL(apiKey, nrAccountID, region, req, ipResolver, cloudAccountID, nrStrategyNRQL)
}

// getKGSourceMode reads the per-tenant kg_source integration config. Defaults
// to "nrql" on lookup failure so a config-service hiccup never escalates to a
// flow-source error — Phase 1 behavior is the safe fallback.
//
// tenantID is required because integrations.ListIntegrationConfigs reads it
// from the security context to scope the query (integrations.tenant_id is
// uuid NOT NULL). NewRequestContextForSuperAdmin alone does not carry one.
func (s *NewRelicAPMFlowSource) getKGSourceMode(tenantID, cloudAccountID string) string {
	secCtx := security.NewRequestContextForTenantAdmin(tenantID, s.logger, nil, nil)
	mode, err := integrations.GetNewRelicKGSource(secCtx, cloudAccountID)
	if err != nil {
		s.logger.Warn("failed to read New Relic kg_source; defaulting to nrql",
			"err", err, "cloud_account_id", cloudAccountID)
		return integrations.NewRelicKGSourceNRQL
	}
	return mode
}

// isTransientNerdGraphError checks whether an error from fetchNerdGraphRelationships
// represents a recoverable upstream failure (5xx, 429) vs a permanent one. Used
// only for log severity; both fall back identically.
func isTransientNerdGraphError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "status 5") || strings.Contains(msg, "status 429")
}

// buildEdgesViaNRQL is the Phase 1 path, parameterized by the strategy label
// to stamp on emitted edges. Used both for default mode ("nrql") and as the
// NerdGraph-mode fallback ("nerdgraph_fallback_nrql").
func (s *NewRelicAPMFlowSource) buildEdgesViaNRQL(
	apiKey, nrAccountID, region string,
	req *core.FlowSourceBuildRequest,
	ipResolver *K8sServiceIPResolver,
	cloudAccountID, strategy string,
) ([]*core.DbEdge, []*core.DbNode, error) {
	edges := make([]*core.DbEdge, 0)

	results, err := s.fetchSpanFacets(apiKey, nrAccountID, region, req)
	if err != nil {
		s.IncrementErrorCount()
		return nil, nil, fmt.Errorf("failed to fetch New Relic span facets: %w", err)
	}

	s.logger.Info("fetched NRQL Span data from New Relic",
		"row_count", len(results),
		"nr_account_id", nrAccountID,
		"strategy", strategy)

	if len(results) >= nrqlFacetLimit {
		s.logger.Warn("NRQL FACET hit row limit; long-tail edges may be truncated. "+
			"Consider narrowing time range or filters.",
			"limit", nrqlFacetLimit)
	}

	unmatchedCallers := 0
	unmatchedTargets := 0

	for _, row := range results {
		callerName, targetVal, targetKind, dbSystem, count := parseSpanFacetRow(row)
		if callerName == "" || targetVal == "" {
			continue
		}

		callerNode, err := matchServiceToNode(
			s.GetNodeMatcher(),
			callerName,
			"Service",
			"", nil, cloudAccountID, s.logger,
		)
		if err != nil || callerNode == nil {
			unmatchedCallers++
			continue
		}

		callerCluster := stringProp(callerNode, "cluster")
		targetNode := s.resolveTarget(targetKind, targetVal, dbSystem, callerCluster, cloudAccountID, ipResolver)
		if targetNode == nil {
			unmatchedTargets++
			continue
		}

		props := s.buildNRQLEdgeProperties(callerName, targetVal, targetKind, dbSystem, nrAccountID, count, strategy)
		edge := s.CreateEdge(
			callerNode, targetNode, core.RelationshipCalls, props,
			req.TenantID, callerNode.CloudAccountID,
		)
		if edge != nil {
			edges = append(edges, edge)
		}
	}

	s.LogMetrics()
	s.logger.Info("completed New Relic NRQL edge build",
		"strategy", strategy,
		"edges_created", len(edges),
		"unmatched_callers", unmatchedCallers,
		"unmatched_targets", unmatchedTargets)
	return edges, nil, nil
}

// buildEdgesViaNerdGraph processes pre-fetched NerdGraph relationships into
// edges. Source-side mismatches are surfaced as unmatched_source_count rather
// than triggering NRQL fallback (see plan D2). Note: the 7-day staleness
// window from KGEdgeStaleAfterDays means edges from a prior mode persist
// until naturally tombstoned by MarkStaleEdgesInactive.
func (s *NewRelicAPMFlowSource) buildEdgesViaNerdGraph(
	rels []NerdGraphRelationship,
	req *core.FlowSourceBuildRequest,
	ipResolver *K8sServiceIPResolver,
	cloudAccountID, nrAccountID string,
) ([]*core.DbEdge, []*core.DbNode, error) {
	edges := make([]*core.DbEdge, 0, len(rels))
	unmatchedSrc := 0
	unmatchedTgt := 0

	for _, rel := range rels {
		callerNode, err := matchServiceToNode(
			s.GetNodeMatcher(),
			rel.CallerName,
			"Service",
			"", nil, cloudAccountID, s.logger,
		)
		if err != nil || callerNode == nil {
			unmatchedSrc++
			continue
		}
		callerCluster := stringProp(callerNode, "cluster")
		targetNode, fqdn := s.resolveNerdGraphTarget(rel, callerCluster, cloudAccountID, ipResolver)
		if targetNode == nil {
			unmatchedTgt++
			continue
		}
		props := s.buildNerdGraphEdgeProperties(rel, fqdn, nrAccountID)
		edge := s.CreateEdge(
			callerNode, targetNode, core.RelationshipCalls, props,
			req.TenantID, callerNode.CloudAccountID,
		)
		if edge != nil {
			edges = append(edges, edge)
		}
	}

	s.LogMetrics()
	s.logger.Info("completed New Relic NerdGraph edge build",
		"tenant_id", req.TenantID,
		"relationships_fetched", len(rels),
		"edges_emitted", len(edges),
		"unmatched_source_count", unmatchedSrc,
		"unmatched_target_count", unmatchedTgt)
	return edges, nil, nil
}

// resolveCloudAccountID picks a cloud account ID from the request, falling back
// to whatever is on the first existing node. Mirrors the Datadog flow source.
func resolveCloudAccountID(req *core.FlowSourceBuildRequest) string {
	if req.CloudAccountID != "" {
		return req.CloudAccountID
	}
	for _, node := range req.ExistingNodes {
		if node != nil && node.CloudAccountID != "" {
			return node.CloudAccountID
		}
	}
	return ""
}

// getNewRelicConfig fetches NR creds via the integrations layer.
//
// Uses the tenant-admin context (NOT plain super-admin) because
// integrations.ListIntegrationConfigs requires a non-empty tenant_id on the
// security context — integrations.tenant_id is uuid NOT NULL in Postgres,
// and the bare super-admin context returns "" for tenant_id.
//
// "Integration not configured for this cloud account" is treated as a soft
// skip (returns empty creds + nil error) rather than a hard error. A tenant
// can legitimately have NR linked to one cloud account but not another, and
// the build runs once per cloud account — the missing-link case must not
// escalate to error_count > 0 / ERROR-level log noise. Real failures
// (DB unreachable, decryption failure, etc.) still propagate.
func (s *NewRelicAPMFlowSource) getNewRelicConfig(tenantID, cloudAccountID string) (apiKey, nrAccountID, region string, err error) {
	secCtx := security.NewRequestContextForTenantAdmin(tenantID, s.logger, nil, nil)
	apiKey, nrAccountID, region, err = integrations.GetNewRelicConfigs(secCtx, cloudAccountID)
	if err != nil {
		if isIntegrationNotConfigured(err) {
			s.logger.Info("New Relic integration not linked to this cloud account; skipping",
				"cloud_account_id", cloudAccountID, "tenant_id", tenantID)
			return "", "", "", nil
		}
		return "", "", "", err
	}
	return apiKey, nrAccountID, region, nil
}

// isIntegrationNotConfigured detects the "integration not found for account"
// error returned by integrations.GetNewRelicConfigs when there is no row in
// integrations_cloud_accounts linking the NR integration to the build's
// cloud account. The integrations package returns this as a plain
// fmt.Errorf — no sentinel — so we match on the message substring. Brittle
// to wording changes but localized to this one call.
func isIntegrationNotConfigured(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "integration not found for account")
}

// fetchSpanFacets runs the aggregation NRQL and returns the raw rows.
func (s *NewRelicAPMFlowSource) fetchSpanFacets(
	apiKey, nrAccountID, region string,
	req *core.FlowSourceBuildRequest,
) ([]map[string]any, error) {
	now := time.Now().Unix()
	var fromTs, toTs int64
	if req.TimeRange != nil {
		fromTs = req.TimeRange.StartTime.Unix()
		toTs = req.TimeRange.EndTime.Unix()
	} else {
		fromTs = now - 86400
		toTs = now
	}

	nrql := fmt.Sprintf(
		"FROM Span SELECT count(*) "+
			"WHERE span.kind = 'client' AND service.name IS NOT NULL "+
			"FACET service.name, server.address, db.system, db.name, peer.service "+
			"SINCE %d UNTIL %d LIMIT %d",
		fromTs, toTs, nrqlFacetLimit,
	)
	return integrations.ExecuteNRQL(apiKey, nrAccountID, region, nrql)
}

// parseSpanFacetRow extracts (callerName, targetVal, targetKind, dbSystem, count) from
// one NRQL FACET row. Target priority: peer.service > (db.system AND db.name) > server.address.
// Skip rows with no usable target by returning empty target fields.
func parseSpanFacetRow(row map[string]any) (caller, target, targetKind, dbSystem string, count int64) {
	facet := facetSlice(row)
	if len(facet) < 5 {
		return "", "", "", "", 0
	}
	caller = stringFromAny(facet[0])
	serverAddr := stringFromAny(facet[1])
	dbSystem = stringFromAny(facet[2])
	dbName := stringFromAny(facet[3])
	peerService := stringFromAny(facet[4])

	count = int64(floatFromAny(row["count"]))

	switch {
	case peerService != "":
		return caller, peerService, "peer_service", dbSystem, count
	case dbSystem != "" && dbName != "":
		return caller, dbName, "database", dbSystem, count
	case serverAddr != "":
		if net.ParseIP(serverAddr) != nil {
			return caller, serverAddr, "cluster_ip", dbSystem, count
		}
		return caller, serverAddr, "hostname", dbSystem, count
	default:
		// db.system without db.name is intentionally rejected (CA2 guard).
		return caller, "", "", "", 0
	}
}

// resolveTarget dispatches to the right matcher based on target kind. Returns
// nil when no node matches — caller skips the row.
func (s *NewRelicAPMFlowSource) resolveTarget(
	targetKind, targetVal, dbSystem, callerCluster, cloudAccountID string,
	ipResolver *K8sServiceIPResolver,
) *core.DbNode {
	matcher := s.GetNodeMatcher()
	switch targetKind {
	case "peer_service":
		n, err := matchServiceToNode(matcher, targetVal, "Service", "", nil, cloudAccountID, s.logger)
		if err != nil {
			return nil
		}
		return n
	case "database":
		return s.findDatabaseNode(targetVal, dbSystem, cloudAccountID)
	case "cluster_ip":
		n, ok := ipResolver.Resolve(callerCluster, targetVal)
		if !ok {
			return nil
		}
		return n
	case "hostname":
		n, err := matchServiceToNode(matcher, targetVal, "Service", "", nil, cloudAccountID, s.logger)
		if err != nil {
			return nil
		}
		return n
	default:
		return nil
	}
}

// findDatabaseNode matches a NodeTypeDatabase node by (engine = dbSystem, name = dbName).
// Both are required — engine alone is structurally ambiguous (CA2).
func (s *NewRelicAPMFlowSource) findDatabaseNode(dbName, dbSystem, cloudAccountID string) *core.DbNode {
	if dbName == "" || dbSystem == "" {
		return nil
	}
	matcher := s.GetNodeMatcher()
	if matcher == nil {
		return nil
	}
	result, err := matcher.FindNode(MatchCriteria{
		AccountID: cloudAccountID,
		NodeType:  core.NodeTypeDatabase,
		PropertyMatches: []PropertyMatch{
			{
				PropertyPath:  "engine",
				Value:         dbSystem,
				MatchType:     core.MatchTypeExact,
				CaseSensitive: false,
			},
			{
				PropertyPath:  "name",
				Value:         dbName,
				MatchType:     core.MatchTypeExact,
				CaseSensitive: false,
			},
		},
	})
	if err != nil || !result.Matched {
		// Try cross-account as a fallback: a database referenced by service.name
		// can live in a different cloud account in real deployments.
		result, err = matcher.FindNode(MatchCriteria{
			NodeType: core.NodeTypeDatabase,
			PropertyMatches: []PropertyMatch{
				{
					PropertyPath:  "engine",
					Value:         dbSystem,
					MatchType:     core.MatchTypeExact,
					CaseSensitive: false,
				},
				{
					PropertyPath:  "name",
					Value:         dbName,
					MatchType:     core.MatchTypeExact,
					CaseSensitive: false,
				},
			},
		})
		if err != nil || !result.Matched {
			return nil
		}
	}
	return result.Node
}

// buildNRQLEdgeProperties assembles the per-edge property bag for an NRQL-derived
// edge. The strategy parameter distinguishes default mode ("nrql") from the
// NerdGraph-mode fallback ("nerdgraph_fallback_nrql") in SQL inspection.
func (s *NewRelicAPMFlowSource) buildNRQLEdgeProperties(
	caller, target, targetKind, dbSystem, nrAccountID string,
	count int64,
	strategy string,
) map[string]interface{} {
	props := map[string]interface{}{
		"nr_source_service":    caller,
		"nr_target_identifier": target,
		"nr_target_kind":       targetKind,
		"nr_account_id":        nrAccountID,
		"nr_protocol":          inferNewRelicProtocol(dbSystem),
		"nr_request_count":     count,
		"nr_strategy":          strategy,
	}
	if targetKind == "database" && dbSystem != "" {
		props["nr_db_system"] = dbSystem
	}
	return props
}

// buildNerdGraphEdgeProperties assembles the property bag for a NerdGraph-derived
// edge. nr_request_count is intentionally omitted — NerdGraph relatedEntities
// don't carry traffic counts, and stamping 0 would be a meaningful "no traffic"
// signal in disguise (see plan D8).
func (s *NewRelicAPMFlowSource) buildNerdGraphEdgeProperties(
	rel NerdGraphRelationship,
	resolvedFQDN, nrAccountID string,
) map[string]interface{} {
	targetKind := nerdGraphTargetKind(rel.TargetType)
	props := map[string]interface{}{
		"nr_source_service":     rel.CallerName,
		"nr_target_identifier":  rel.TargetName,
		"nr_target_kind":        targetKind,
		"nr_target_entity_type": rel.TargetType,
		"nr_account_id":         nrAccountID,
		"nr_protocol":           "http",
		"nr_strategy":           nrStrategyNerdGraph,
		"nr_caller_entity_guid": rel.CallerGUID,
		"nr_target_entity_guid": rel.TargetGUID,
	}
	if resolvedFQDN != "" {
		props["nr_target_resolved_fqdn"] = resolvedFQDN
	}
	return props
}

// nerdGraphTargetKind maps an NR entityType to the same nr_target_kind enum
// used by the NRQL path so SQL queries can filter uniformly across strategies.
func nerdGraphTargetKind(entityType string) string {
	switch entityType {
	case "THIRD_PARTY_SERVICE_ENTITY":
		return "peer_service"
	case "EBPFSERVER":
		return "hostname"
	case "KUBERNETES_POD", "KUBERNETES_DEPLOYMENT":
		return "workload"
	default:
		return strings.ToLower(entityType)
	}
}

// resolveNerdGraphTarget dispatches per entityType. Returns the matched node
// and (for EBPFSERVER targets) the parsed FQDN so the property builder can
// stamp nr_target_resolved_fqdn.
func (s *NewRelicAPMFlowSource) resolveNerdGraphTarget(
	rel NerdGraphRelationship,
	callerCluster, cloudAccountID string,
	ipResolver *K8sServiceIPResolver,
) (*core.DbNode, string) {
	matcher := s.GetNodeMatcher()
	switch rel.TargetType {
	case "THIRD_PARTY_SERVICE_ENTITY":
		n, _ := matchServiceToNode(matcher, rel.TargetName, "Service", "", nil, cloudAccountID, s.logger)
		return n, ""
	case "EBPFSERVER":
		t, ok := parseEBPFServerName(rel.TargetName)
		if !ok {
			s.logger.Debug("unparseable EBPFSERVER name; skipping",
				"name", rel.TargetName)
			return nil, ""
		}
		if n := s.matchK8sInternalDNS(t.FQDN, cloudAccountID); n != nil {
			return n, t.FQDN
		}
		if n, ok := ipResolver.Resolve(callerCluster, t.IP); ok {
			return n, t.FQDN
		}
		return nil, ""
	case "KUBERNETES_POD", "KUBERNETES_DEPLOYMENT":
		n, _ := matchServiceToNode(matcher, rel.TargetName, "Deployment", "", nil, cloudAccountID, s.logger)
		return n, ""
	default:
		s.logger.Debug("unknown NerdGraph target entityType; skipping",
			"entity_type", rel.TargetType, "name", rel.TargetName)
		return nil, ""
	}
}

// matchK8sInternalDNS parses an FQDN as a K8s internal-DNS name and matches
// it to an existing K8sService / Workload / Service node. Mirrors the strategy
// in TracesFlowSource.matchK8sInternalDNSToNode but uses NodeMatcher directly
// to avoid the cross-flow-source coupling.
func (s *NewRelicAPMFlowSource) matchK8sInternalDNS(fqdn, cloudAccountID string) *core.DbNode {
	parsed := parseK8sServiceDNS(fqdn)
	if parsed == nil {
		return nil
	}
	matcher := s.GetNodeMatcher()
	if matcher == nil {
		return nil
	}
	tries := []core.NodeType{core.NodeTypeK8sService, core.NodeTypeWorkload, core.NodeTypeService}
	for _, nodeType := range tries {
		// Strategy 1: same-account, namespace + name
		if parsed.Namespace != "" {
			if n := s.findByNameAndNamespace(matcher, parsed.ServiceName, parsed.Namespace, cloudAccountID, nodeType, true); n != nil {
				return n
			}
		}
		// Strategy 2: same-account, name only
		if n := s.findByNameAndNamespace(matcher, parsed.ServiceName, "", cloudAccountID, nodeType, true); n != nil {
			return n
		}
		// Strategy 3: any-account, namespace + name (cross-account fallback)
		if parsed.Namespace != "" {
			if n := s.findByNameAndNamespace(matcher, parsed.ServiceName, parsed.Namespace, "", nodeType, false); n != nil {
				return n
			}
		}
	}
	return nil
}

func (s *NewRelicAPMFlowSource) findByNameAndNamespace(
	matcher *NodeMatcher,
	name, namespace, accountID string,
	nodeType core.NodeType,
	filterByAccount bool,
) *core.DbNode {
	criteria := MatchCriteria{
		NodeType: nodeType,
		PropertyMatches: []PropertyMatch{
			{PropertyPath: "name", Value: name, MatchType: core.MatchTypeExact, CaseSensitive: false},
		},
	}
	if filterByAccount {
		criteria.AccountID = accountID
	}
	if namespace != "" {
		criteria.PropertyMatches = append(criteria.PropertyMatches, PropertyMatch{
			PropertyPath: "namespace", Value: namespace, MatchType: core.MatchTypeExact, CaseSensitive: false,
		})
	}
	result, err := matcher.FindNode(criteria)
	if err != nil || !result.Matched {
		return nil
	}
	return result.Node
}

// inferNewRelicProtocol maps a db.system value to a protocol string. For
// non-database targets (db.system empty) it defaults to "http", which is the
// dominant transport for span.kind=client traffic.
func inferNewRelicProtocol(dbSystem string) string {
	switch strings.ToLower(dbSystem) {
	case "":
		return "http"
	case "postgresql", "postgres":
		return "postgres"
	case "mysql":
		return "mysql"
	case "redis":
		return "redis"
	case "mongodb", "mongo":
		return "mongodb"
	case "cassandra":
		return "cassandra"
	case "elasticsearch":
		return "elasticsearch"
	default:
		return strings.ToLower(dbSystem)
	}
}

// facetSlice extracts the "facet" array from an NRQL row, returning nil when
// the row shape is unexpected.
func facetSlice(row map[string]any) []any {
	v, ok := row["facet"]
	if !ok {
		return nil
	}
	if arr, ok := v.([]any); ok {
		return arr
	}
	return nil
}

// stringFromAny coerces a JSON value to string, treating null and non-strings as empty.
func stringFromAny(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// floatFromAny coerces a JSON-decoded numeric to float64; non-numbers return 0.
func floatFromAny(v any) float64 {
	if v == nil {
		return 0
	}
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}
