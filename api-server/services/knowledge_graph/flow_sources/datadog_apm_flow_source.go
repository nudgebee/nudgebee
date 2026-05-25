package flow_sources

import (
	"context"
	"fmt"
	"log/slog"
	"nudgebee/services/integrations"
	"nudgebee/services/knowledge_graph/core"
	"nudgebee/services/security"
	"nudgebee/services/traces"
	"strings"
	"time"
)

func init() {
	// Register the Datadog APM flow source factory in the global registry
	RegisterFlowSourceFactory(
		"datadog-apm",
		func(logger *slog.Logger) (core.FlowSourceInterface, error) {
			return NewDatadogAPMFlowSource(logger), nil
		},
		"Datadog APM service map flow source that fetches service-to-service call data",
		string(core.FlowSourceCategoryTracing),
	)
}

// DatadogAPMFlowSource creates flow relationships from Datadog APM service map
// It fetches service-to-service call data from Datadog and matches it to existing nodes in the graph
type DatadogAPMFlowSource struct {
	*BaseFlowSource
}

// NewDatadogAPMFlowSource creates a new Datadog APM flow source
func NewDatadogAPMFlowSource(logger *slog.Logger) *DatadogAPMFlowSource {
	base := NewBaseFlowSource(
		"datadog-apm",
		core.FlowSourceCategoryTracing,
		true,
		logger,
	)

	return &DatadogAPMFlowSource{
		BaseFlowSource: base,
	}
}

// Validate validates the Datadog APM flow source configuration
func (s *DatadogAPMFlowSource) Validate() error {
	if err := s.BaseFlowSource.Validate(); err != nil {
		return err
	}

	// Configuration will be fetched dynamically from integrations
	// No static validation needed
	return nil
}

// BuildFlowRelationships builds flow relationships from Datadog APM service map
func (s *DatadogAPMFlowSource) BuildFlowRelationships(reqCtx *security.RequestContext, req *core.FlowSourceBuildRequest) ([]*core.DbEdge, []*core.DbNode, error) {
	ctx := reqCtx.GetContext()
	startTime := TimeNow()
	defer s.TrackBuildTime(startTime)

	s.logger.Info("building flow relationships from Datadog APM",
		"source", s.GetName(),
		"tenant_id", req.TenantID,
		"existing_nodes", len(req.ExistingNodes))

	// Initialize node matcher
	s.InitializeNodeMatcher(req.ExistingNodes)

	edges := make([]*core.DbEdge, 0)
	nodes := make([]*core.DbNode, 0)

	// Get Datadog configuration from integrations
	apiKey, appKey, site, err := s.getDatadogConfig(ctx, req)
	if err != nil {
		s.IncrementErrorCount()
		return nil, nil, fmt.Errorf("failed to get Datadog configuration: %w", err)
	}

	if apiKey == "" || appKey == "" {
		s.logger.Warn("Datadog API credentials not configured, skipping flow source")
		return edges, nodes, nil
	}

	s.logger.Info("fetched Datadog configuration",
		"site", site,
		"has_api_key", apiKey != "",
		"has_app_key", appKey != "")

	// Fetch APM graph data from Datadog
	graphData, err := s.fetchAPMGraphData(apiKey, appKey, site, req)
	if err != nil {
		s.IncrementErrorCount()
		return nil, nil, fmt.Errorf("failed to fetch APM graph data: %w", err)
	}

	s.logger.Info("fetched APM graph data from Datadog",
		"entities_count", len(graphData.Entities),
		"edges_count", len(graphData.Edges))

	// Build entity ID to entity mapping
	entityByID := make(map[string]*traces.APMEntity)
	for i := range graphData.Entities {
		entity := &graphData.Entities[i]
		if entity.Type == "apm-entity" {
			entityByID[entity.ID] = entity
		}
	}

	// Track unmatched services for analysis
	unmatchedSources := make(map[string]int)
	unmatchedDestinations := make(map[string]int)

	// Resolve the build's cloud account once for matchEntityToNode's
	// same-account-preference strategy. resolveCloudAccountID is the package-
	// level helper shared with the NR flow source.
	cloudAccountID := resolveCloudAccountID(req)

	// Process each edge from Datadog
	for _, apmEdge := range graphData.Edges {
		if apmEdge.Type != "apm-entity-edge" {
			continue
		}

		// Get source and destination entities
		sourceEntity := entityByID[apmEdge.Relationships.Source.Data.ID]
		destEntity := entityByID[apmEdge.Relationships.Target.Data.ID]

		if sourceEntity == nil || destEntity == nil {
			s.logger.Debug("edge references missing entity",
				"source_id", apmEdge.Relationships.Source.Data.ID,
				"dest_id", apmEdge.Relationships.Target.Data.ID)
			continue
		}

		// Extract service names from entities
		sourceName, sourceKind := s.extractEntityNameAndKind(sourceEntity)
		destName, destKind := s.extractEntityNameAndKind(destEntity)

		// Match source and destination nodes in the knowledge graph
		sourceNode, sourceErr := s.matchEntityToNode(sourceEntity, sourceName, sourceKind, cloudAccountID)
		destNode, destErr := s.matchEntityToNode(destEntity, destName, destKind, cloudAccountID)

		// Track unmatched services
		if sourceErr != nil {
			unmatchedSources[sourceName]++
			s.logger.Debug("source service not found in knowledge graph",
				"service", sourceName,
				"kind", sourceKind,
				"dd_entity_id", sourceEntity.ID)
			continue
		}

		if destErr != nil {
			unmatchedDestinations[destName]++
			s.logger.Debug("destination service not found in knowledge graph",
				"service", destName,
				"kind", destKind,
				"dd_entity_id", destEntity.ID)
			continue
		}

		// Create edge with Datadog metadata
		properties := s.buildEdgeProperties(&apmEdge, sourceEntity, destEntity)

		edge := s.CreateEdge(
			sourceNode,
			destNode,
			core.RelationshipCalls,
			properties,
			req.TenantID,
			sourceNode.CloudAccountID, // Use source node's cloud account
		)

		if edge != nil {
			edges = append(edges, edge)
		}
	}

	// Log unmatched services for analysis
	if len(unmatchedSources) > 0 || len(unmatchedDestinations) > 0 {
		s.logger.Warn("found unmatched services in Datadog APM",
			"unmatched_sources_count", len(unmatchedSources),
			"unmatched_destinations_count", len(unmatchedDestinations),
			"unmatched_sources", unmatchedSources,
			"unmatched_destinations", unmatchedDestinations)
	}

	s.LogMetrics()

	s.logger.Info("completed building flow relationships from Datadog APM",
		"edges_created", len(edges),
		"unmatched_sources", len(unmatchedSources),
		"unmatched_destinations", len(unmatchedDestinations))

	return edges, nodes, nil
}

// getDatadogConfig fetches Datadog API configuration from integrations
func (s *DatadogAPMFlowSource) getDatadogConfig(
	ctx context.Context,
	req *core.FlowSourceBuildRequest,
) (apiKey, appKey, site string, err error) {
	// Try to get cloud account ID from request or existing nodes
	cloudAccountID := req.CloudAccountID

	// If no cloud account ID in request, try to get from first existing node
	if cloudAccountID == "" && len(req.ExistingNodes) > 0 {
		for _, node := range req.ExistingNodes {
			if node.CloudAccountID != "" {
				cloudAccountID = node.CloudAccountID
				break
			}
		}
	}

	if cloudAccountID == "" {
		return "", "", "", fmt.Errorf("cloud account ID not found in request or existing nodes")
	}

	s.logger.Info("fetching Datadog configuration",
		"cloud_account_id", cloudAccountID,
		"tenant_id", req.TenantID)

	// Use the tenant-admin context (NOT plain super-admin) because
	// integrations.ListIntegrationConfigs requires a non-empty tenant_id on
	// the security context — integrations.tenant_id is uuid NOT NULL in
	// Postgres, and the bare super-admin context returns "" for tenant_id.
	secCtx := security.NewRequestContextForTenantAdmin(req.TenantID, s.logger, nil, nil)

	// Get Datadog configuration from integrations
	apiKey, appKey, site, err = integrations.GetDatadogConfigs(secCtx, cloudAccountID)
	if err != nil {
		// "Integration not configured for this cloud account" is a soft skip,
		// not a hard error: a tenant can legitimately have Datadog linked to
		// one cloud account but not another, and the build runs once per cloud
		// account. Treat this case the same as missing creds (return empty
		// strings + nil error). Real failures (DB unreachable, decryption
		// failure, tenant_id missing, etc.) still propagate.
		if isDatadogIntegrationNotConfigured(err) {
			s.logger.Info("Datadog integration not linked to this cloud account; skipping",
				"cloud_account_id", cloudAccountID, "tenant_id", req.TenantID)
			return "", "", "", nil
		}
		return "", "", "", fmt.Errorf("failed to get Datadog configs: %w", err)
	}

	return apiKey, appKey, site, nil
}

// isDatadogIntegrationNotConfigured detects the "datadog integration not found"
// error returned by integrations.GetDatadogConfigs when no row in
// integrations_cloud_accounts links the Datadog integration to the build's
// cloud account. The integrations package returns this as a plain fmt.Errorf
// with no sentinel — match on the message substring. Brittle to wording
// changes but localized to this one call. Mirrors the pattern in
// newrelic_apm_flow_source.go.
func isDatadogIntegrationNotConfigured(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "datadog integration not found for account")
}

// fetchAPMGraphData fetches APM graph data from Datadog
func (s *DatadogAPMFlowSource) fetchAPMGraphData(
	apiKey, appKey, site string,
	req *core.FlowSourceBuildRequest,
) (*traces.APMGraphData, error) {
	// Create Datadog API config
	config := traces.NewDatadogAPIConfig(apiKey, appKey, site)

	// Determine time range
	now := time.Now().Unix()
	var fromTimestamp, toTimestamp int64

	if req.TimeRange != nil {
		fromTimestamp = req.TimeRange.StartTime.Unix()
		toTimestamp = req.TimeRange.EndTime.Unix()
	} else {
		// Default to last 24 hours
		fromTimestamp = now - 86400
		toTimestamp = now
	}

	// Build APM graph parameters
	params := traces.APMEntitiesGraphParams{
		FromTimestamp: fromTimestamp,
		ToTimestamp:   toTimestamp,
		Columns: []string{
			"OPERATION_NAME",
		},
		Include: []string{
			"entity.catalog_definition",
			"entity.service_health",
			"inferred_entities",
		},
		Datastore:            "metrics",
		PageSize:             0, // Get all
		ReturnLegacyFields:   false,
		HideServiceOverrides: false,
	}

	// Add environment filter if specified
	if env, ok := req.Filters["environment"]; ok && env != "" {
		params.Environment = env
		s.logger.Info("filtering by environment", "environment", env)
	}

	// Fetch graph data
	graphData, err := traces.FetchDatadogAPMGraphData(config, params)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch Datadog APM graph data: %w", err)
	}

	return graphData, nil
}

// extractEntityNameAndKind extracts service name and kind from Datadog entity
func (s *DatadogAPMFlowSource) extractEntityNameAndKind(entity *traces.APMEntity) (name string, kind string) {
	if entity == nil {
		return "", ""
	}

	// Check for service tag (normal service)
	if svc, ok := entity.Attributes.IDTags["service"]; ok {
		return svc, "Service"
	}

	// Check for database peer
	if dbSystem, ok := entity.Attributes.IDTags["peer.db.system"]; ok {
		if db, hasSystem := entity.Attributes.IDTags["peer.db.name"]; hasSystem && db != "" {
			return db, dbSystem
		}
		return dbSystem, dbSystem
	}

	// Check for RPC service peer
	if rpc, ok := entity.Attributes.IDTags["peer.rpc.service"]; ok {
		return rpc, "Service"
	}

	// Check for hostname peer
	if host, ok := entity.Attributes.IDTags["peer.hostname"]; ok {
		return host, "ExternalService"
	}

	// Check for Kafka
	if topic, ok := entity.Attributes.IDTags["peer.messaging.destination"]; ok {
		return topic, "kafka"
	}

	// Default to entity ID
	return entity.ID, "Service"
}

// matchEntityToNode matches a Datadog entity to a knowledge graph node.
//
// Layered as: shared matchServiceToNode (same 5 strategies NR + traces use)
// → legacy dd_service_name property fallback (kept for one release as a
// safety net; expected to never hit in production — no source currently sets
// this property) → contains-name fallback for inferred peer entities whose
// entity-name doesn't carry a recognizable service tag.
func (s *DatadogAPMFlowSource) matchEntityToNode(
	entity *traces.APMEntity,
	entityName string,
	entityKind string,
	cloudAccountID string,
) (*core.DbNode, error) {
	matcher := s.GetNodeMatcher()
	if matcher == nil {
		return nil, fmt.Errorf("node matcher not initialized")
	}

	serviceName := entity.Attributes.IDTags["service"]

	// Primary: shared matcher used by NR + traces flow sources. Pass
	// labels = {service: <ddServiceName>} so its strategy-5 (label-based)
	// match path can hit nodes tagged via Datadog unified service tagging.
	if serviceName != "" {
		labels := map[string]string{"service": serviceName}
		if n, err := matchServiceToNode(matcher, serviceName, entityKind, "", labels, cloudAccountID, s.logger); err == nil && n != nil {
			return n, nil
		}
	}

	// Datadog-specific safety net: legacy dd_service_name property match.
	// No source in this codebase currently writes dd_service_name as a node
	// property — strategy expected to never fire. WARN-log when it does so
	// telemetry can confirm before deletion in a follow-up PR.
	if serviceName != "" {
		result, err := matcher.FindNode(MatchCriteria{
			PropertyMatches: []PropertyMatch{{
				PropertyPath:  "dd_service_name",
				Value:         serviceName,
				MatchType:     core.MatchTypeExact,
				CaseSensitive: false,
			}},
		})
		if err == nil && result.Matched {
			s.logger.Warn("matched entity via legacy dd_service_name strategy — please report",
				"dd_service", serviceName,
				"node", result.Node.UniqueKey,
				"confidence", result.Confidence)
			return result.Node, nil
		}
	}

	// Final fallback: contains-match on entity name (preserves prior
	// behaviour for inferred peer entities whose entity-name doesn't line
	// up with a service tag — e.g. peer.hostname:foo, peer.db.name:bar).
	if entityName != "" && entityName != entity.ID {
		result, err := matcher.FindNode(MatchCriteria{
			PropertyMatches: []PropertyMatch{
				{
					PropertyPath:  "name",
					Value:         entityName,
					MatchType:     core.MatchTypeContains,
					CaseSensitive: false,
				},
			},
		})
		if err == nil && result.Matched {
			s.logger.Debug("matched entity by name (contains)",
				"entity_name", entityName,
				"node", result.Node.UniqueKey,
				"confidence", result.Confidence)
			return result.Node, nil
		}
	}

	return nil, fmt.Errorf("no matching node found for entity: %s (kind: %s)", entityName, entityKind)
}

// buildEdgeProperties builds edge properties from Datadog APM edge
func (s *DatadogAPMFlowSource) buildEdgeProperties(
	apmEdge *traces.APMEntityEdge,
	sourceEntity *traces.APMEntity,
	destEntity *traces.APMEntity,
) map[string]interface{} {
	properties := make(map[string]interface{})

	// Add operation information
	if apmEdge.Attributes.Operation != "" {
		properties["dd_operation"] = apmEdge.Attributes.Operation
	}

	if apmEdge.Attributes.SpanKind != "" {
		properties["dd_span_kind"] = apmEdge.Attributes.SpanKind
	}

	// Infer protocol from operation
	protocol := s.inferProtocol(apmEdge.Attributes.Operation)
	if protocol != "unknown" {
		properties["protocol"] = protocol
	}

	// Add Datadog entity IDs for reference
	properties["dd_source_entity_id"] = sourceEntity.ID
	properties["dd_dest_entity_id"] = destEntity.ID

	// Add edge ID
	properties["dd_edge_id"] = apmEdge.ID

	// Add source entity metadata
	if serviceName, ok := sourceEntity.Attributes.IDTags["service"]; ok {
		properties["dd_source_service"] = serviceName
	}

	// Add destination entity metadata
	if serviceName, ok := destEntity.Attributes.IDTags["service"]; ok {
		properties["dd_dest_service"] = serviceName
	}

	return properties
}

// inferProtocol infers the protocol from the operation name
func (s *DatadogAPMFlowSource) inferProtocol(operation string) string {
	operation = strings.ToLower(operation)

	protocolMap := map[string]string{
		"http.request":   "http",
		"grpc":           "grpc",
		"postgres.query": "postgres",
		"redis.command":  "redis",
		"kafka.consume":  "kafka",
		"kafka.produce":  "kafka",
		"mongo.query":    "mongo",
		"s3.command":     "s3",
		"universal.http": "http",
		"web.request":    "http",
		"http.client":    "http",
	}

	for pattern, protocol := range protocolMap {
		if strings.Contains(operation, pattern) {
			return protocol
		}
	}

	return "unknown"
}
